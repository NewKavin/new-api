package openaicompat

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

func TestChatCompletionsResponseToResponsesResponse_Text(t *testing.T) {
	chat := &dto.OpenAITextResponse{
		Id:      "chatcmpl_1",
		Object:  "chat.completion",
		Created: int64(1700000000),
		Model:   "MiniMax-M2.7",
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index: 0,
				Message: dto.Message{
					Role:    "assistant",
					Content: "hello world",
				},
				FinishReason: "stop",
			},
		},
		Usage: dto.Usage{
			PromptTokens:     10,
			CompletionTokens: 6,
			TotalTokens:      16,
		},
	}

	resp, usage, err := ChatCompletionsResponseToResponsesResponse(chat, "resp_req_1", "resp_parent_1", nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("ChatCompletionsResponseToResponsesResponse returned error: %v", err)
	}
	if resp == nil {
		t.Fatalf("response is nil")
	}
	if resp.ID != "resp_req_1" {
		t.Fatalf("response id = %q, want resp_req_1", resp.ID)
	}
	if usage == nil {
		t.Fatalf("usage is nil")
	}
	if usage.PromptTokens != 10 || usage.CompletionTokens != 6 || usage.TotalTokens != 16 {
		t.Fatalf("usage mismatch: %+v", *usage)
	}
	if len(resp.Output) != 1 {
		t.Fatalf("output length = %d, want 1", len(resp.Output))
	}
	if resp.Output[0].Type != "message" || resp.Output[0].Role != "assistant" {
		t.Fatalf("unexpected output[0]: %+v", resp.Output[0])
	}
	if len(resp.Output[0].Content) != 1 || resp.Output[0].Content[0].Type != "output_text" || resp.Output[0].Content[0].Text != "hello world" {
		t.Fatalf("unexpected output text payload: %+v", resp.Output[0].Content)
	}
}

func TestChatCompletionsResponseToResponsesResponse_ToolCall(t *testing.T) {
	toolCallsRaw, _ := common.Marshal([]map[string]any{
		{
			"id":   "call_1",
			"type": "function",
			"function": map[string]any{
				"name":      "list_files",
				"arguments": "{\"path\":\".\"}",
			},
		},
	})
	chat := &dto.OpenAITextResponse{
		Id:      "chatcmpl_2",
		Object:  "chat.completion",
		Created: int64(1700000001),
		Model:   "glm-5.1",
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index: 0,
				Message: dto.Message{
					Role:      "assistant",
					Content:   "",
					ToolCalls: toolCallsRaw,
				},
				FinishReason: "tool_calls",
			},
		},
	}

	resp, _, err := ChatCompletionsResponseToResponsesResponse(chat, "resp_req_2", "", nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("ChatCompletionsResponseToResponsesResponse returned error: %v", err)
	}
	if len(resp.Output) != 1 {
		t.Fatalf("output length = %d, want 1", len(resp.Output))
	}
	out := resp.Output[0]
	if out.Type != "function_call" {
		t.Fatalf("output type = %q, want function_call", out.Type)
	}
	if out.CallId != "call_1" {
		t.Fatalf("call_id = %q, want call_1", out.CallId)
	}
	if out.Name != "list_files" {
		t.Fatalf("name = %q, want list_files", out.Name)
	}
	if out.ArgumentsString() != "{\"path\":\".\"}" {
		t.Fatalf("arguments = %q, want {\"path\":\".\"}", out.ArgumentsString())
	}
}
