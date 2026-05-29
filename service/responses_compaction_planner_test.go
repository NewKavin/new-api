package service

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
)

func TestResponsesCompactionPlanner_PreservesToolAdjacency(t *testing.T) {
	toolCall := dto.ToolCallRequest{
		ID:   "call_1",
		Type: "function",
		Function: dto.FunctionRequest{
			Name:      "list_files",
			Arguments: "{\"path\":\".\"}",
		},
	}
	assistantWithTool := dto.Message{
		Role:    "assistant",
		Content: "",
	}
	assistantWithTool.SetToolCalls([]dto.ToolCallRequest{toolCall})

	messages := []dto.Message{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		assistantWithTool,
		{Role: "tool", ToolCallId: "call_1", Content: "{\"ok\":true}"},
		{Role: "tool", ToolCallId: "call_1", Content: "{\"files\":[\"a.go\"]}"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
	}

	plan, err := BuildResponsesCompactionPlan(messages, 3)
	if err != nil {
		t.Fatalf("BuildResponsesCompactionPlan returned error: %v", err)
	}
	if plan == nil {
		t.Fatalf("plan should not be nil")
	}
	if !plan.NeedsSummary {
		t.Fatalf("plan should require summary")
	}
	if len(plan.OlderMessages) != 6 {
		t.Fatalf("older messages len = %d, want 6", len(plan.OlderMessages))
	}
	if len(plan.RetainedMessages) != 3 {
		t.Fatalf("retained messages len = %d, want 3", len(plan.RetainedMessages))
	}
	if plan.RetainedMessages[0].Role == "tool" {
		t.Fatalf("retained head should not start with tool message")
	}
	if got := len(plan.OlderMessages[3].ParseToolCalls()); got != 1 {
		t.Fatalf("older assistant tool_calls len = %d, want 1", got)
	}
	if plan.OlderMessages[4].Role != "tool" || plan.OlderMessages[5].Role != "tool" {
		t.Fatalf("tool outputs must stay adjacent to assistant tool call")
	}
}

func TestResponsesCompactionPlanner_NoSummaryWhenConversationIsShort(t *testing.T) {
	messages := []dto.Message{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
	}

	plan, err := BuildResponsesCompactionPlan(messages, 8)
	if err != nil {
		t.Fatalf("BuildResponsesCompactionPlan returned error: %v", err)
	}
	if plan == nil {
		t.Fatalf("plan should not be nil")
	}
	if plan.NeedsSummary {
		t.Fatalf("plan should not require summary for short conversation")
	}
	if len(plan.OlderMessages) != 0 {
		t.Fatalf("older messages len = %d, want 0", len(plan.OlderMessages))
	}
	if len(plan.RetainedMessages) != len(messages) {
		t.Fatalf("retained messages len = %d, want %d", len(plan.RetainedMessages), len(messages))
	}
}
