package relay

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func ResponsesCompactLocalHelper(c *gin.Context, info *relaycommon.RelayInfo, req *dto.OpenAIResponsesRequest) *types.NewAPIError {
	if req == nil {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("request is nil"),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	parentID := strings.TrimSpace(req.PreviousResponseID)
	if parentID == "" {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("previous_response_id is required for local compaction"),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	store := service.GetResponsesStateStore()
	parent, found, err := store.LoadByResponseID(parentID)
	if err != nil {
		return types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if !found {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("previous_response_id %q not found", parentID),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	plan, err := service.BuildResponsesCompactionPlan(parent.Messages, service.DefaultResponsesCompactionRetainTurns)
	if err != nil {
		return types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	retained := append([]dto.Message(nil), plan.RetainedMessages...)
	summaryText := ""
	usage := &dto.Usage{}
	if plan.NeedsSummary && len(plan.OlderMessages) > 0 {
		summaryModel := strings.TrimSpace(req.Model)
		if summaryModel == "" {
			summaryModel = parent.Model
		}
		var compactErr *types.NewAPIError
		summaryText, usage, compactErr = summarizeMessagesForCompact(c, info, summaryModel, plan.OlderMessages)
		if compactErr != nil {
			return compactErr
		}
		if strings.TrimSpace(summaryText) != "" {
			retained = append([]dto.Message{
				{
					Role:    "system",
					Content: "[COMPACT SUMMARY]\n" + summaryText,
				},
			}, retained...)
		}
	}

	promptCacheKey := extractPromptCacheKey(req.PromptCacheKey)
	if promptCacheKey == "" {
		promptCacheKey = parent.PromptCacheKey
	}
	successorID := helper.GetResponsesID(c)
	nowUnix := time.Now().Unix()
	snapshot := &service.ResponsesStateSnapshot{
		ResponseID:        successorID,
		ParentResponseID:  parent.ResponseID,
		PromptCacheKey:    promptCacheKey,
		Model:             strings.TrimSpace(req.Model),
		CreatedAt:         nowUnix,
		Instructions:      chooseRaw(req.Instructions, parent.Instructions),
		Tools:             chooseRaw(req.Tools, parent.Tools),
		ToolChoice:        chooseRaw(req.ToolChoice, parent.ToolChoice),
		ParallelToolCalls: chooseRaw(req.ParallelToolCalls, parent.ParallelToolCalls),
		Reasoning:         chooseReasoning(req.Reasoning, parent.Reasoning),
		Metadata:          chooseRaw(req.Metadata, parent.Metadata),
		Messages:          retained,
		CompactSummary:    summaryText,
		ChainDepth:        parent.ChainDepth + 1,
	}
	if snapshot.Model == "" {
		snapshot.Model = parent.Model
	}
	if snapshot.ChainDepth <= 1 {
		snapshot.ChainDepth = 2
	}
	if err := store.Save(snapshot); err != nil {
		logger.LogError(c, fmt.Sprintf("save compacted snapshot failed: %v", err))
	}

	outputRaw, _ := common.Marshal(map[string]any{
		"summary":                summaryText,
		"retained_message_count": len(retained),
		"previous_response_id":   parentID,
		"new_response_id":        successorID,
	})
	compactionResp := dto.OpenAIResponsesCompactionResponse{
		ID:        successorID,
		Object:    "response.compaction",
		CreatedAt: int(nowUnix),
		Output:    outputRaw,
		Usage:     usage,
	}
	respJSON, err := common.Marshal(compactionResp)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}
	service.IOCopyBytesGracefully(c, nil, respJSON)

	if usage != nil && service.ValidUsage(usage) {
		service.PostTextConsumeQuota(c, info, usage, nil)
	}
	return nil
}

func summarizeMessagesForCompact(c *gin.Context, info *relaycommon.RelayInfo, model string, messages []dto.Message) (string, *dto.Usage, *types.NewAPIError) {
	oldMessagesRaw, _ := common.Marshal(messages)
	summaryReq := &dto.GeneralOpenAIRequest{
		Model: model,
		Messages: []dto.Message{
			{
				Role: "system",
				Content: "Summarize older conversation context for coding agent continuation. " +
					"Keep repo facts, user constraints, completed decisions, unresolved TODOs, and tool-derived facts. " +
					"Do not invent files, commands, or test results.",
			},
			{
				Role:    "user",
				Content: string(oldMessagesRaw),
			},
		},
		MaxTokens: lo.ToPtr(uint(1024)),
	}
	httpResp, newAPIErr := doResponsesViaChatUpstreamRequest(c, info, summaryReq)
	if newAPIErr != nil {
		return "", nil, newAPIErr
	}
	defer service.CloseResponseBodyGracefully(httpResp)

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	var chatResp dto.OpenAITextResponse
	if err := common.Unmarshal(body, &chatResp); err != nil {
		return "", nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiErr := chatResp.GetOpenAIError(); oaiErr != nil && oaiErr.Type != "" {
		return "", nil, types.WithOpenAIError(*oaiErr, httpResp.StatusCode)
	}
	summaryText := ""
	if len(chatResp.Choices) > 0 {
		summaryText = strings.TrimSpace(chatResp.Choices[0].Message.StringContent())
	}

	usage := &dto.Usage{
		PromptTokens:     chatResp.Usage.PromptTokens,
		CompletionTokens: chatResp.Usage.CompletionTokens,
		TotalTokens:      chatResp.Usage.TotalTokens,
		InputTokens:      chatResp.Usage.PromptTokens,
		OutputTokens:     chatResp.Usage.CompletionTokens,
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return summaryText, usage, nil
}

func chooseRaw(current []byte, parent []byte) []byte {
	if len(current) > 0 {
		return current
	}
	return parent
}

func chooseReasoning(current *dto.Reasoning, parent *dto.Reasoning) *dto.Reasoning {
	if current != nil {
		return current
	}
	return parent
}
