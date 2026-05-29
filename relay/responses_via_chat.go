package relay

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func ResponsesViaChatHelper(c *gin.Context, info *relaycommon.RelayInfo, req *dto.OpenAIResponsesRequest) *types.NewAPIError {
	if req == nil {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("request is nil"),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	store := service.GetResponsesStateStore()
	var parent *service.ResponsesStateSnapshot
	if prevID := strings.TrimSpace(req.PreviousResponseID); prevID != "" {
		loaded, found, err := store.LoadByResponseID(prevID)
		if err != nil {
			return types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		if !found {
			return types.NewErrorWithStatusCode(
				fmt.Errorf("previous_response_id %q not found", prevID),
				types.ErrorCodeInvalidRequest,
				http.StatusBadRequest,
				types.ErrOptionWithSkipRetry(),
			)
		}
		parent = loaded
	}

	chatReq, err := service.ResponsesRequestToChatCompletionsRequest(req)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	normalizedParent := normalizeResponsesStateParentSnapshot(parent)
	mergedMessages, turnMessages := mergeResponsesStateMessages(normalizedParent, req.Instructions, chatReq.Messages)
	chatReq.Messages = mergedMessages
	info.ShouldIncludeUsage = chatReq.StreamOptions != nil && chatReq.StreamOptions.IncludeUsage

	responseID := helper.GetResponsesID(c)
	promptCacheKey := extractPromptCacheKey(req.PromptCacheKey)
	startCreatedAt := time.Now().Unix()
	if normalizedParent != nil && promptCacheKey == "" {
		promptCacheKey = normalizedParent.PromptCacheKey
	}

	var usage *dto.Usage
	var assistantMsg dto.Message
	var createdAt int64

	if lo.FromPtrOr(chatReq.Stream, false) {
		streamUsage, streamMsg, streamCreatedAt, newAPIErr := doResponsesViaChatStream(c, info, req, chatReq, responseID, startCreatedAt)
		if newAPIErr != nil {
			return newAPIErr
		}
		usage = streamUsage
		assistantMsg = streamMsg
		createdAt = streamCreatedAt
	} else {
		textResp, textUsage, newAPIErr := doResponsesViaChatNonStream(c, info, req, chatReq, responseID, startCreatedAt)
		if newAPIErr != nil {
			return newAPIErr
		}
		usage = textUsage
		assistantMsg = textResp.Choices[0].Message
		createdAt = int64(extractCreatedAt(textResp.Created))
	}
	if createdAt == 0 {
		createdAt = startCreatedAt
	}

	snapshotMessages := make([]dto.Message, 0, len(turnMessages)+1)
	snapshotMessages = append(snapshotMessages, turnMessages...)
	snapshotMessages = append(snapshotMessages, assistantMsg)
	snapshot, err := store.BuildContinuationSnapshot(normalizedParent, req, snapshotMessages, responseID, promptCacheKey, createdAt)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("build responses state snapshot failed: %v", err))
	} else if err := store.Save(snapshot); err != nil {
		logger.LogError(c, fmt.Sprintf("save responses state snapshot failed: %v", err))
	}

	if usage != nil {
		service.PostTextConsumeQuota(c, info, usage, nil)
	}
	return nil
}

func doResponsesViaChatNonStream(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	req *dto.OpenAIResponsesRequest,
	chatReq *dto.GeneralOpenAIRequest,
	responseID string,
	defaultCreatedAt int64,
) (*dto.OpenAITextResponse, *dto.Usage, *types.NewAPIError) {
	httpResp, newAPIErr := doResponsesViaChatUpstreamRequest(c, info, chatReq)
	if newAPIErr != nil {
		return nil, nil, newAPIErr
	}
	defer service.CloseResponseBodyGracefully(httpResp)

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	var chatResp dto.OpenAITextResponse
	if err := common.Unmarshal(body, &chatResp); err != nil {
		return nil, nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiErr := chatResp.GetOpenAIError(); oaiErr != nil && oaiErr.Type != "" {
		return nil, nil, types.WithOpenAIError(*oaiErr, httpResp.StatusCode)
	}
	if len(chatResp.Choices) == 0 {
		return nil, nil, types.NewOpenAIError(
			fmt.Errorf("chat response choices are empty"),
			types.ErrorCodeBadResponseBody,
			http.StatusInternalServerError,
		)
	}

	respObj, usage, err := service.ChatCompletionsResponseToResponsesResponse(
		&chatResp,
		responseID,
		req.PreviousResponseID,
		req.Instructions,
		req.Tools,
		req.ToolChoice,
		req.ParallelToolCalls,
		req.Reasoning,
		req.Metadata,
		req.Truncation,
		req.User,
	)
	if err != nil {
		return nil, nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if respObj.CreatedAt == 0 {
		respObj.CreatedAt = int(defaultCreatedAt)
	}
	respJSON, err := common.Marshal(respObj)
	if err != nil {
		return nil, nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}
	service.IOCopyBytesGracefully(c, httpResp, respJSON)
	return &chatResp, usage, nil
}

func doResponsesViaChatStream(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	req *dto.OpenAIResponsesRequest,
	chatReq *dto.GeneralOpenAIRequest,
	responseID string,
	defaultCreatedAt int64,
) (*dto.Usage, dto.Message, int64, *types.NewAPIError) {
	httpResp, newAPIErr := doResponsesViaChatUpstreamRequest(c, info, chatReq)
	if newAPIErr != nil {
		return nil, dto.Message{}, 0, newAPIErr
	}
	return ResponsesViaChatStreamHandler(c, info, httpResp, req, responseID, defaultCreatedAt)
}

func doResponsesViaChatUpstreamRequest(c *gin.Context, info *relaycommon.RelayInfo, chatReq *dto.GeneralOpenAIRequest) (*http.Response, *types.NewAPIError) {
	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return nil, types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)

	savedRelayMode := info.RelayMode
	savedRequestPath := info.RequestURLPath
	savedStream := info.IsStream
	defer func() {
		info.RelayMode = savedRelayMode
		info.RequestURLPath = savedRequestPath
		info.IsStream = savedStream
	}()

	info.RelayMode = relayconstant.RelayModeChatCompletions
	info.RequestURLPath = "/v1/chat/completions"
	info.IsStream = lo.FromPtrOr(chatReq.Stream, false)

	// Apply model mapping based on downstream channel type
	applyModelMapping(info, chatReq)

	convertedRequest, err := adaptor.ConvertOpenAIRequest(c, info, chatReq)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)

	jsonData, err := common.Marshal(convertedRequest)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
		if err != nil {
			return nil, newAPIErrorFromParamOverride(err)
		}
	}

	body, size, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	defer closer.Close()
	info.UpstreamRequestBodySize = size

	resp, err := adaptor.DoRequest(c, info, body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	httpResp := resp.(*http.Response)
	if httpResp.StatusCode != http.StatusOK {
		statusCodeMappingStr := c.GetString("status_code_mapping")
		newAPIErr := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		service.ResetStatusCode(newAPIErr, statusCodeMappingStr)
		return nil, newAPIErr
	}
	return httpResp, nil
}

func mergeResponsesStateMessages(parent *service.ResponsesStateSnapshot, requestInstructions []byte, current []dto.Message) ([]dto.Message, []dto.Message) {
	turnMessages := append([]dto.Message(nil), current...)
	requestInstructionText := parseResponsesInstructionText(requestInstructions)
	if len(requestInstructions) > 0 {
		turnMessages = stripLeadingInstructionMessage(turnMessages, requestInstructionText)
	}

	merged := make([]dto.Message, 0, len(turnMessages))
	activeInstructions := requestInstructions
	if len(activeInstructions) == 0 && parent != nil {
		activeInstructions = parent.Instructions
	}
	if parent != nil {
		merged = append(merged, parent.Messages...)
	}
	merged = append(merged, turnMessages...)

	activeInstructionText := parseResponsesInstructionText(activeInstructions)
	if activeInstructionText != "" {
		merged = append([]dto.Message{
			{
				Role:    "system",
				Content: activeInstructionText,
			},
		}, merged...)
	}
	return merged, turnMessages
}

func normalizeResponsesStateParentSnapshot(parent *service.ResponsesStateSnapshot) *service.ResponsesStateSnapshot {
	if parent == nil {
		return nil
	}
	cloned := *parent
	cloned.Messages = append([]dto.Message(nil), parent.Messages...)
	cloned.Messages = stripLeadingInstructionMessage(cloned.Messages, parseResponsesInstructionText(parent.Instructions))
	return &cloned
}

func stripLeadingInstructionMessage(messages []dto.Message, instruction string) []dto.Message {
	if len(messages) == 0 || strings.TrimSpace(instruction) == "" {
		return messages
	}
	if messages[0].Role != "system" {
		return messages
	}
	if strings.TrimSpace(messages[0].StringContent()) != strings.TrimSpace(instruction) {
		return messages
	}
	return append([]dto.Message(nil), messages[1:]...)
}

func applyModelMapping(info *relaycommon.RelayInfo, chatReq *dto.GeneralOpenAIRequest) {
	if info == nil || chatReq == nil {
		return
	}

	// Apply configured model mapping from channel settings
	if info.ChannelSetting.ModelMapping != nil && len(info.ChannelSetting.ModelMapping) > 0 {
		if mappedModel, ok := info.ChannelSetting.ModelMapping[chatReq.Model]; ok {
			chatReq.Model = mappedModel
		}
	}

	// Codex prefers lowercase model names
	if info.ChannelMeta != nil && info.ChannelMeta.ChannelType == constant.ChannelTypeCodex {
		chatReq.Model = strings.ToLower(chatReq.Model)
	}
}

func parseResponsesInstructionText(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if common.GetJsonType(raw) == "string" {
		var instruction string
		if err := common.Unmarshal(raw, &instruction); err == nil {
			return strings.TrimSpace(instruction)
		}
	}
	return strings.TrimSpace(string(raw))
}

func extractPromptCacheKey(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if common.GetJsonType(raw) == "string" {
		var key string
		if err := common.Unmarshal(raw, &key); err == nil {
			return strings.TrimSpace(key)
		}
	}
	return ""
}

func extractCreatedAt(v any) int {
	switch vv := v.(type) {
	case int:
		return vv
	case int64:
		return int(vv)
	case float64:
		return int(vv)
	default:
		return 0
	}
}
