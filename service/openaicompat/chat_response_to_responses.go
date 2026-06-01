package openaicompat

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

func ChatCompletionsResponseToResponsesResponse(
	chatResp *dto.OpenAITextResponse,
	responseID string,
	previousResponseID string,
	instructions json.RawMessage,
	toolsRaw json.RawMessage,
	toolChoice json.RawMessage,
	parallelToolCalls json.RawMessage,
	reasoning *dto.Reasoning,
	metadata json.RawMessage,
	truncation json.RawMessage,
	user json.RawMessage,
) (*dto.OpenAIResponsesResponse, *dto.Usage, error) {
	if chatResp == nil {
		return nil, nil, errors.New("chat response is nil")
	}
	if len(chatResp.Choices) == 0 {
		return nil, nil, errors.New("chat response choices are empty")
	}
	if strings.TrimSpace(responseID) == "" {
		return nil, nil, errors.New("response id is required")
	}

	usage := copyUsageFromChat(chatResp)
	tools := parseResponsesOutputTools(toolsRaw)
	parallel := parseParallelToolCalls(parallelToolCalls)
	prevRaw := marshalPreviousResponseID(previousResponseID)

	output := buildResponsesOutputFromChatMessage(chatResp.Choices[0].Message)
	resp := &dto.OpenAIResponsesResponse{
		ID:                 responseID,
		Object:             "response",
		CreatedAt:          extractCreatedAt(chatResp.Created),
		Status:             common.StringToByteSlice(`"completed"`),
		Instructions:       instructions,
		Model:              chatResp.Model,
		Output:             output,
		ParallelToolCalls:  parallel,
		PreviousResponseID: prevRaw,
		Reasoning:          reasoning,
		ToolChoice:         toolChoice,
		Tools:              tools,
		Truncation:         truncation,
		Usage:              usage,
		User:               user,
		Metadata:           metadata,
	}
	return resp, usageAsBillable(usage), nil
}

func buildResponsesOutputFromChatMessage(msg dto.Message) []dto.ResponsesOutput {
	output := make([]dto.ResponsesOutput, 0, 2)

	content := strings.TrimSpace(msg.StringContent())
	if content != "" {
		output = append(output, dto.ResponsesOutput{
			Type:   "message",
			Role:   "assistant",
			Status: "completed",
			Content: []dto.ResponsesOutputContent{
				{
					Type: "output_text",
					Text: content,
				},
			},
		})
	}

	for _, call := range msg.ParseToolCalls() {
		if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Function.Name) == "" {
			continue
		}
		output = append(output, dto.ResponsesOutput{
			Type:      "function_call",
			Status:    "completed",
			CallId:    call.ID,
			Name:      call.Function.Name,
			Arguments: responsesFunctionCallArgumentsRaw(call.Function.Arguments),
		})
	}
	return output
}

func responsesFunctionCallArgumentsRaw(arguments string) json.RawMessage {
	raw, err := common.Marshal(arguments)
	if err != nil {
		return common.StringToByteSlice(`""`)
	}
	return raw
}

func parseResponsesOutputTools(raw json.RawMessage) []map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var tools []map[string]any
	if err := common.Unmarshal(raw, &tools); err != nil {
		return nil
	}
	return tools
}

func parseParallelToolCalls(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var v bool
	if err := common.Unmarshal(raw, &v); err != nil {
		return false
	}
	return v
}

func marshalPreviousResponseID(id string) json.RawMessage {
	id = strings.TrimSpace(id)
	if id == "" {
		return common.StringToByteSlice("null")
	}
	b, _ := common.Marshal(id)
	return b
}

func usageAsBillable(usage *dto.Usage) *dto.Usage {
	if usage == nil {
		return &dto.Usage{}
	}
	return &dto.Usage{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      usage.TotalTokens,
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: usage.PromptTokensDetails.CachedTokens,
			ImageTokens:  usage.PromptTokensDetails.ImageTokens,
			AudioTokens:  usage.PromptTokensDetails.AudioTokens,
		},
		CompletionTokenDetails: dto.OutputTokenDetails{
			ReasoningTokens: usage.CompletionTokenDetails.ReasoningTokens,
		},
	}
}

func copyUsageFromChat(chatResp *dto.OpenAITextResponse) *dto.Usage {
	u := &dto.Usage{}
	if chatResp == nil {
		return u
	}
	promptTokens := chatResp.Usage.PromptTokens
	if promptTokens == 0 {
		promptTokens = chatResp.Usage.InputTokens
	}
	completionTokens := chatResp.Usage.CompletionTokens
	if completionTokens == 0 {
		completionTokens = chatResp.Usage.OutputTokens
	}
	u.PromptTokens = promptTokens
	u.CompletionTokens = completionTokens
	u.TotalTokens = chatResp.Usage.TotalTokens
	if u.TotalTokens == 0 {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	u.InputTokens = u.PromptTokens
	u.OutputTokens = u.CompletionTokens
	u.PromptTokensDetails = chatResp.Usage.PromptTokensDetails
	u.CompletionTokenDetails = chatResp.Usage.CompletionTokenDetails
	return u
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
