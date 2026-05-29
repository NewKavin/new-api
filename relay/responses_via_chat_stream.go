package relay

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type streamToolCallState struct {
	CallID   string
	Name     string
	Args     strings.Builder
	Added    bool
	ItemID   string
	OutIndex int
}

func ResponsesViaChatStreamHandler(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	resp *http.Response,
	req *dto.OpenAIResponsesRequest,
	responseID string,
	defaultCreatedAt int64,
) (*dto.Usage, dto.Message, int64, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, dto.Message{}, 0, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	usage := &dto.Usage{}
	model := info.UpstreamModelName
	createdAt := defaultCreatedAt
	finishReason := "stop"

	messageItemID := fmt.Sprintf("msg_%s", responseID)
	messageAdded := false
	createdSent := false
	completedSent := false

	assistantText := strings.Builder{}
	toolCallsByIndex := make(map[int]*streamToolCallState)
	toolCallOrder := make([]int, 0, 4)

	sendEvent := func(event dto.ResponsesStreamResponse) error {
		payload, err := common.Marshal(event)
		if err != nil {
			return err
		}
		helper.ResponseChunkData(c, event, string(payload))
		return nil
	}

	sendCreated := func() error {
		if createdSent {
			return nil
		}
		prevRaw := common.StringToByteSlice("null")
		if strings.TrimSpace(req.PreviousResponseID) != "" {
			prevRaw, _ = common.Marshal(req.PreviousResponseID)
		}
		err := sendEvent(dto.ResponsesStreamResponse{
			Type: "response.created",
			Response: &dto.OpenAIResponsesResponse{
				ID:                 responseID,
				Object:             "response",
				CreatedAt:          int(createdAt),
				Model:              model,
				Status:             common.StringToByteSlice(`"in_progress"`),
				PreviousResponseID: prevRaw,
			},
		})
		if err == nil {
			createdSent = true
		}
		return err
	}

	sendMessageAdded := func() error {
		if messageAdded {
			return nil
		}
		if err := sendCreated(); err != nil {
			return err
		}
		outIdx := 0
		contentIdx := 0
		err := sendEvent(dto.ResponsesStreamResponse{
			Type: dto.ResponsesOutputTypeItemAdded,
			Item: &dto.ResponsesOutput{
				Type:   "message",
				ID:     messageItemID,
				Status: "in_progress",
				Role:   "assistant",
				Content: []dto.ResponsesOutputContent{
					{Type: "output_text", Text: ""},
				},
			},
			OutputIndex:  &outIdx,
			ContentIndex: &contentIdx,
		})
		if err == nil {
			messageAdded = true
		}
		return err
	}

	getToolCall := func(idx int) *streamToolCallState {
		if state, ok := toolCallsByIndex[idx]; ok {
			return state
		}
		state := &streamToolCallState{
			CallID:   fmt.Sprintf("%s_call_%d", responseID, idx),
			ItemID:   fmt.Sprintf("%s_call_item_%d", responseID, idx),
			OutIndex: idx + 1,
		}
		toolCallsByIndex[idx] = state
		toolCallOrder = append(toolCallOrder, idx)
		return state
	}

	sendToolCallAdded := func(state *streamToolCallState) error {
		if state.Added {
			return nil
		}
		if err := sendCreated(); err != nil {
			return err
		}
		outIdx := state.OutIndex
		err := sendEvent(dto.ResponsesStreamResponse{
			Type: dto.ResponsesOutputTypeItemAdded,
			Item: &dto.ResponsesOutput{
				Type:      "function_call",
				ID:        state.ItemID,
				Status:    "in_progress",
				CallId:    state.CallID,
				Name:      state.Name,
				Arguments: common.StringToByteSlice(state.Args.String()),
			},
			OutputIndex: &outIdx,
		})
		if err == nil {
			state.Added = true
		}
		return err
	}

	streamErr := error(nil)
	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if streamErr != nil {
			sr.Stop(streamErr)
			return
		}

		var chunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
			sr.Error(err)
			return
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Created != 0 {
			createdAt = chunk.Created
		}
		if chunk.Usage != nil && service.ValidUsage(chunk.Usage) {
			usage = chunk.Usage
		}

		for _, choice := range chunk.Choices {
			if delta := choice.Delta.GetContentString(); delta != "" {
				if err := sendMessageAdded(); err != nil {
					streamErr = err
					sr.Stop(err)
					return
				}
				assistantText.WriteString(delta)
				outIdx := 0
				contentIdx := 0
				if err := sendEvent(dto.ResponsesStreamResponse{
					Type:         "response.output_text.delta",
					Delta:        delta,
					OutputIndex:  &outIdx,
					ContentIndex: &contentIdx,
				}); err != nil {
					streamErr = err
					sr.Stop(err)
					return
				}
			}

			if len(choice.Delta.ToolCalls) > 0 {
				for i, tc := range choice.Delta.ToolCalls {
					callIndex := i
					if tc.Index != nil {
						callIndex = *tc.Index
					}
					state := getToolCall(callIndex)
					if id := strings.TrimSpace(tc.ID); id != "" {
						state.CallID = id
						state.ItemID = id
					}
					if name := strings.TrimSpace(tc.Function.Name); name != "" {
						state.Name = name
					}
					if err := sendToolCallAdded(state); err != nil {
						streamErr = err
						sr.Stop(err)
						return
					}
					if argDelta := tc.Function.Arguments; argDelta != "" {
						state.Args.WriteString(argDelta)
						outIdx := state.OutIndex
						if err := sendEvent(dto.ResponsesStreamResponse{
							Type:        "response.function_call_arguments.delta",
							ItemID:      state.ItemID,
							Delta:       argDelta,
							OutputIndex: &outIdx,
						}); err != nil {
							streamErr = err
							sr.Stop(err)
							return
						}
					}
				}
			}

			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finishReason = *choice.FinishReason
			}
		}
	})

	if streamErr != nil {
		return nil, dto.Message{}, 0, types.NewOpenAIError(streamErr, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	if !createdSent {
		if err := sendCreated(); err != nil {
			return nil, dto.Message{}, 0, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
	}
	if messageAdded {
		outIdx := 0
		if err := sendEvent(dto.ResponsesStreamResponse{
			Type: dto.ResponsesOutputTypeItemDone,
			Item: &dto.ResponsesOutput{
				Type:   "message",
				ID:     messageItemID,
				Status: "completed",
				Role:   "assistant",
				Content: []dto.ResponsesOutputContent{
					{Type: "output_text", Text: assistantText.String()},
				},
			},
			OutputIndex: &outIdx,
		}); err != nil {
			return nil, dto.Message{}, 0, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
	}

	toolCalls := make([]dto.ToolCallRequest, 0, len(toolCallOrder))
	for _, idx := range toolCallOrder {
		state := toolCallsByIndex[idx]
		if state == nil {
			continue
		}
		outIdx := state.OutIndex
		if err := sendEvent(dto.ResponsesStreamResponse{
			Type:        "response.function_call_arguments.done",
			ItemID:      state.ItemID,
			Delta:       state.Args.String(),
			OutputIndex: &outIdx,
		}); err != nil {
			return nil, dto.Message{}, 0, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
		if err := sendEvent(dto.ResponsesStreamResponse{
			Type: dto.ResponsesOutputTypeItemDone,
			Item: &dto.ResponsesOutput{
				Type:      "function_call",
				ID:        state.ItemID,
				Status:    "completed",
				CallId:    state.CallID,
				Name:      state.Name,
				Arguments: common.StringToByteSlice(state.Args.String()),
			},
			OutputIndex: &outIdx,
		}); err != nil {
			return nil, dto.Message{}, 0, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
		toolCalls = append(toolCalls, dto.ToolCallRequest{
			ID:   state.CallID,
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      state.Name,
				Arguments: state.Args.String(),
			},
		})
	}

	assistantMsg := dto.Message{
		Role:    "assistant",
		Content: assistantText.String(),
	}
	if len(toolCalls) > 0 {
		assistantMsg.SetToolCalls(toolCalls)
		if strings.TrimSpace(assistantMsg.StringContent()) == "" {
			assistantMsg.Content = ""
		}
	}

	if !service.ValidUsage(usage) {
		fallbackText := assistantText.String()
		for _, tc := range toolCalls {
			fallbackText += tc.Function.Name + tc.Function.Arguments
		}
		usage = service.ResponseText2Usage(c, fallbackText, info.UpstreamModelName, info.GetEstimatePromptTokens())
	}

	completedResp, _, err := service.ChatCompletionsResponseToResponsesResponse(
		&dto.OpenAITextResponse{
			Id:      responseID,
			Object:  "chat.completion",
			Created: createdAt,
			Model:   model,
			Choices: []dto.OpenAITextResponseChoice{
				{
					Index:        0,
					Message:      assistantMsg,
					FinishReason: finishReason,
				},
			},
			Usage: *usage,
		},
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
		logger.LogError(c, fmt.Sprintf("build completed response failed: %v", err))
	}
	if completedResp != nil && !completedSent {
		completedResp.Status = common.StringToByteSlice(`"completed"`)
		if err := sendEvent(dto.ResponsesStreamResponse{
			Type:     "response.completed",
			Response: completedResp,
		}); err != nil {
			return nil, dto.Message{}, 0, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
		completedSent = true
	}
	if !completedSent {
		fallbackCompleted := &dto.OpenAIResponsesResponse{
			ID:                 responseID,
			Object:             "response",
			CreatedAt:          int(createdAt),
			Status:             common.StringToByteSlice(`"completed"`),
			Model:              model,
			Usage:              usage,
			PreviousResponseID: previousResponseIDRaw(req.PreviousResponseID),
		}
		if err := sendEvent(dto.ResponsesStreamResponse{
			Type:     "response.completed",
			Response: fallbackCompleted,
		}); err != nil {
			return nil, dto.Message{}, 0, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
	}

	helper.Done(c)
	return usage, assistantMsg, createdAt, nil
}

func previousResponseIDRaw(id string) []byte {
	id = strings.TrimSpace(id)
	if id == "" {
		return common.StringToByteSlice("null")
	}
	b, _ := common.Marshal(id)
	return b
}
