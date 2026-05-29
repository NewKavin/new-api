package service

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/service/openaicompat"
)

func ChatCompletionsRequestToResponsesRequest(req *dto.GeneralOpenAIRequest) (*dto.OpenAIResponsesRequest, error) {
	return openaicompat.ChatCompletionsRequestToResponsesRequest(req)
}

func ResponsesResponseToChatCompletionsResponse(resp *dto.OpenAIResponsesResponse, id string) (*dto.OpenAITextResponse, *dto.Usage, error) {
	return openaicompat.ResponsesResponseToChatCompletionsResponse(resp, id)
}

func ExtractOutputTextFromResponses(resp *dto.OpenAIResponsesResponse) string {
	return openaicompat.ExtractOutputTextFromResponses(resp)
}

func ResponsesRequestToChatCompletionsRequest(req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	return openaicompat.ResponsesRequestToChatCompletionsRequest(req)
}

func ChatCompletionsResponseToResponsesResponse(
	chatResp *dto.OpenAITextResponse,
	responseID string,
	previousResponseID string,
	instructions json.RawMessage,
	tools json.RawMessage,
	toolChoice json.RawMessage,
	parallelToolCalls json.RawMessage,
	reasoning *dto.Reasoning,
	metadata json.RawMessage,
	truncation json.RawMessage,
	user json.RawMessage,
) (*dto.OpenAIResponsesResponse, *dto.Usage, error) {
	return openaicompat.ChatCompletionsResponseToResponsesResponse(
		chatResp,
		responseID,
		previousResponseID,
		instructions,
		tools,
		toolChoice,
		parallelToolCalls,
		reasoning,
		metadata,
		truncation,
		user,
	)
}
