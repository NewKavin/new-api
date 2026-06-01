package openaicompat

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/samber/lo"
)

func TestResponsesRequestToChatCompletionsRequest_BasicMapping(t *testing.T) {
	inputRaw, _ := common.Marshal([]map[string]any{
		{
			"role":    "user",
			"content": "hi",
		},
		{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "output_text", "text": "hello"},
			},
		},
		{
			"type":      "function_call",
			"call_id":   "call_1",
			"name":      "list_files",
			"arguments": "{\"path\":\".\"}",
		},
		{
			"type":    "function_call_output",
			"call_id": "call_1",
			"output":  "{\"ok\":true}",
		},
	})

	toolsRaw, _ := common.Marshal([]map[string]any{
		{
			"type": "function",
			"name": "list_files",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
			},
		},
	})
	toolChoiceRaw, _ := common.Marshal("required")
	instructionsRaw, _ := common.Marshal("follow repo constraints")

	req := &dto.OpenAIResponsesRequest{
		Model:           "MiniMax-M2.7",
		Input:           inputRaw,
		Instructions:    instructionsRaw,
		Tools:           toolsRaw,
		ToolChoice:      toolChoiceRaw,
		MaxOutputTokens: lo.ToPtr(uint(0)),
		Reasoning: &dto.Reasoning{
			Effort: "high",
		},
		Temperature: lo.ToPtr(0.5),
		TopP:        lo.ToPtr(0.9),
		Stream:      lo.ToPtr(true),
		StreamOptions: &dto.StreamOptions{
			IncludeUsage: true,
		},
		Metadata: common.StringToByteSlice(`{"source":"codex-cli"}`),
	}

	out, err := ResponsesRequestToChatCompletionsRequest(req)
	if err != nil {
		t.Fatalf("ResponsesRequestToChatCompletionsRequest returned error: %v", err)
	}
	if out == nil {
		t.Fatalf("converted request is nil")
	}

	if out.Model != req.Model {
		t.Fatalf("model = %q, want %q", out.Model, req.Model)
	}
	if out.MaxTokens == nil {
		t.Fatalf("max_tokens should preserve explicit zero")
	}
	if *out.MaxTokens != 0 {
		t.Fatalf("max_tokens = %d, want 0", *out.MaxTokens)
	}
	if out.Stream == nil || !*out.Stream {
		t.Fatalf("stream should be true")
	}
	if out.StreamOptions == nil || !out.StreamOptions.IncludeUsage {
		t.Fatalf("stream_options.include_usage should be true")
	}
	if out.Reasoning == nil {
		t.Fatalf("reasoning should be set")
	}
	if out.ToolChoice == nil {
		t.Fatalf("tool_choice should be set")
	}
	if len(out.Tools) != 1 {
		t.Fatalf("tools length = %d, want 1", len(out.Tools))
	}
	if out.Tools[0].Function.Name != "list_files" {
		t.Fatalf("tool function name = %q, want list_files", out.Tools[0].Function.Name)
	}

	if len(out.Messages) < 4 {
		t.Fatalf("messages length = %d, want >= 4", len(out.Messages))
	}
	if out.Messages[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", out.Messages[0].Role)
	}
	if out.Messages[0].StringContent() != "follow repo constraints" {
		t.Fatalf("first message content = %q, want instructions", out.Messages[0].StringContent())
	}

	foundUser := false
	foundAssistantToolCall := false
	foundToolOutput := false
	for _, msg := range out.Messages {
		if msg.Role == "user" && msg.StringContent() == "hi" {
			foundUser = true
		}
		if msg.Role == "assistant" {
			for _, tc := range msg.ParseToolCalls() {
				if tc.ID == "call_1" && tc.Function.Name == "list_files" && tc.Function.Arguments == "{\"path\":\".\"}" {
					foundAssistantToolCall = true
				}
			}
		}
		if msg.Role == "tool" && msg.ToolCallId == "call_1" && msg.StringContent() == "{\"ok\":true}" {
			foundToolOutput = true
		}
	}
	if !foundUser {
		t.Fatalf("user message not found")
	}
	if !foundAssistantToolCall {
		t.Fatalf("assistant tool call message not found")
	}
	if !foundToolOutput {
		t.Fatalf("tool output message not found")
	}
}

func TestResponsesRequestToChatCompletionsRequest_RejectsNil(t *testing.T) {
	_, err := ResponsesRequestToChatCompletionsRequest(nil)
	if err == nil {
		t.Fatalf("expected error for nil request")
	}
}

func TestResponsesRequestToChatCompletionsRequest_MessageItemInput(t *testing.T) {
	inputRaw, _ := common.Marshal([]map[string]any{
		{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": "Reply exactly: OK"},
			},
		},
	})

	req := &dto.OpenAIResponsesRequest{
		Model: "ZhipuAI/GLM-5",
		Input: inputRaw,
	}

	out, err := ResponsesRequestToChatCompletionsRequest(req)
	if err != nil {
		t.Fatalf("ResponsesRequestToChatCompletionsRequest returned error: %v", err)
	}
	if len(out.Messages) != 1 {
		t.Fatalf("messages length = %d, want 1", len(out.Messages))
	}
	if out.Messages[0].Role != "user" {
		t.Fatalf("message role = %q, want user", out.Messages[0].Role)
	}
	parts, ok := out.Messages[0].Content.([]map[string]any)
	if !ok {
		t.Fatalf("message content type = %T, want []map[string]any", out.Messages[0].Content)
	}
	if len(parts) != 1 || parts[0]["type"] != "text" || parts[0]["text"] != "Reply exactly: OK" {
		t.Fatalf("unexpected message content: %#v", parts)
	}
}

func TestResponsesRequestToChatCompletionsRequest_DeveloperRoleMapsToSystem(t *testing.T) {
	inputRaw, _ := common.Marshal([]map[string]any{
		{
			"type":    "message",
			"role":    "developer",
			"content": "Follow repo instructions.",
		},
		{
			"type":    "message",
			"role":    "user",
			"content": "Reply exactly: OK",
		},
	})

	req := &dto.OpenAIResponsesRequest{
		Model: "ZhipuAI/GLM-5",
		Input: inputRaw,
	}

	out, err := ResponsesRequestToChatCompletionsRequest(req)
	if err != nil {
		t.Fatalf("ResponsesRequestToChatCompletionsRequest returned error: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("messages length = %d, want 2", len(out.Messages))
	}
	if out.Messages[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", out.Messages[0].Role)
	}
	if out.Messages[0].StringContent() != "Follow repo instructions." {
		t.Fatalf("first message content = %q, want developer content", out.Messages[0].StringContent())
	}
	if out.Messages[1].Role != "user" {
		t.Fatalf("second message role = %q, want user", out.Messages[1].Role)
	}
}

func TestResponsesRequestToChatCompletionsRequest_DeveloperContentPartsBecomeSystemString(t *testing.T) {
	inputRaw, _ := common.Marshal([]map[string]any{
		{
			"type": "message",
			"role": "developer",
			"content": []map[string]any{
				{"type": "input_text", "text": "First instruction."},
				{"type": "input_text", "text": "Second instruction."},
			},
		},
	})

	req := &dto.OpenAIResponsesRequest{
		Model: "ZhipuAI/GLM-5",
		Input: inputRaw,
	}

	out, err := ResponsesRequestToChatCompletionsRequest(req)
	if err != nil {
		t.Fatalf("ResponsesRequestToChatCompletionsRequest returned error: %v", err)
	}
	if len(out.Messages) != 1 {
		t.Fatalf("messages length = %d, want 1", len(out.Messages))
	}
	if out.Messages[0].Role != "system" {
		t.Fatalf("message role = %q, want system", out.Messages[0].Role)
	}
	if out.Messages[0].StringContent() != "First instruction.\nSecond instruction." {
		t.Fatalf("message content = %q, want joined system text", out.Messages[0].StringContent())
	}
}

func TestResponsesRequestToChatCompletionsRequest_NonStreamDefaultsToFalseAndDropsStreamOptions(t *testing.T) {
	req := &dto.OpenAIResponsesRequest{
		Model: "ZhipuAI/GLM-5.1",
		Input: common.StringToByteSlice(`"hi"`),
		StreamOptions: &dto.StreamOptions{
			IncludeUsage: true,
		},
	}

	out, err := ResponsesRequestToChatCompletionsRequest(req)
	if err != nil {
		t.Fatalf("ResponsesRequestToChatCompletionsRequest returned error: %v", err)
	}
	if out.Stream == nil {
		t.Fatalf("stream should be explicitly set to false for non-stream requests")
	}
	if *out.Stream {
		t.Fatalf("stream = true, want false")
	}
	if out.StreamOptions != nil {
		t.Fatalf("stream_options should be omitted for non-stream requests")
	}
}

func TestResponsesRequestToChatCompletionsRequest_StreamFalseDropsStreamOptions(t *testing.T) {
	req := &dto.OpenAIResponsesRequest{
		Model:  "ZhipuAI/GLM-5.1",
		Input:  common.StringToByteSlice(`"hi"`),
		Stream: lo.ToPtr(false),
		StreamOptions: &dto.StreamOptions{
			IncludeUsage: true,
		},
	}

	out, err := ResponsesRequestToChatCompletionsRequest(req)
	if err != nil {
		t.Fatalf("ResponsesRequestToChatCompletionsRequest returned error: %v", err)
	}
	if out.Stream == nil {
		t.Fatalf("stream should be explicitly set to false")
	}
	if *out.Stream {
		t.Fatalf("stream = true, want false")
	}
	if out.StreamOptions != nil {
		t.Fatalf("stream_options should be omitted when stream is false")
	}
}
