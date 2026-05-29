package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

func TestResponsesStateStore_SaveLoadByIDAndPromptKey(t *testing.T) {
	store := newResponsesStateStoreForTest(2*time.Hour, 16)
	snapshot := &ResponsesStateSnapshot{
		ResponseID:     "resp_req_1",
		PromptCacheKey: "pcache_1",
		Model:          "MiniMax-M2.7",
		CreatedAt:      1700000000,
		Messages: []dto.Message{
			{Role: "user", Content: "hi"},
		},
	}

	if err := store.Save(snapshot); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	loaded, found, err := store.LoadByResponseID("resp_req_1")
	if err != nil {
		t.Fatalf("LoadByResponseID returned error: %v", err)
	}
	if !found {
		t.Fatalf("LoadByResponseID did not find saved snapshot")
	}
	if loaded.ResponseID != snapshot.ResponseID {
		t.Fatalf("response_id = %q, want %q", loaded.ResponseID, snapshot.ResponseID)
	}

	byPrompt, found, err := store.LoadByPromptCacheKey("pcache_1")
	if err != nil {
		t.Fatalf("LoadByPromptCacheKey returned error: %v", err)
	}
	if !found {
		t.Fatalf("LoadByPromptCacheKey did not find saved snapshot")
	}
	if byPrompt.ResponseID != snapshot.ResponseID {
		t.Fatalf("prompt key points to %q, want %q", byPrompt.ResponseID, snapshot.ResponseID)
	}
}

func TestResponsesStateStore_BuildContinuationSnapshot_MergeAndOverride(t *testing.T) {
	store := newResponsesStateStoreForTest(2*time.Hour, 16)
	prevInstructions, _ := common.Marshal("old instructions")
	newInstructions, _ := common.Marshal("new instructions")
	prevTools, _ := common.Marshal([]map[string]any{{"type": "function", "name": "old_tool"}})
	newTools, _ := common.Marshal([]map[string]any{{"type": "function", "name": "new_tool"}})
	metadataRaw := common.StringToByteSlice(`{"source":"codex-cli"}`)

	parent := &ResponsesStateSnapshot{
		ResponseID:   "resp_parent",
		Model:        "MiniMax-M2.7",
		CreatedAt:    1700000000,
		Instructions: prevInstructions,
		Tools:        prevTools,
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
		},
	}
	req := &dto.OpenAIResponsesRequest{
		Model:        "MiniMax-M2.7",
		Instructions: newInstructions,
		Tools:        newTools,
		Metadata:     metadataRaw,
	}
	newMessages := []dto.Message{
		{Role: "user", Content: "next turn"},
	}

	next, err := store.BuildContinuationSnapshot(parent, req, newMessages, "resp_child", "pcache_child", 1700000010)
	if err != nil {
		t.Fatalf("BuildContinuationSnapshot returned error: %v", err)
	}
	if next.ParentResponseID != "resp_parent" {
		t.Fatalf("parent_response_id = %q, want resp_parent", next.ParentResponseID)
	}
	if next.ResponseID != "resp_child" {
		t.Fatalf("response_id = %q, want resp_child", next.ResponseID)
	}
	if len(next.Messages) != 2 {
		t.Fatalf("messages length = %d, want 2", len(next.Messages))
	}
	if string(next.Instructions) != "\"new instructions\"" {
		t.Fatalf("instructions not overridden, got %s", next.Instructions)
	}
	if string(next.Tools) != string(newTools) {
		t.Fatalf("tools not overridden")
	}
	if string(next.Metadata) != string(metadataRaw) {
		t.Fatalf("metadata not copied")
	}
}

func TestResponsesStateStore_BuildContinuationSnapshot_EnforceDepth(t *testing.T) {
	store := newResponsesStateStoreForTest(2*time.Hour, 2)
	root := &ResponsesStateSnapshot{
		ResponseID: "resp_root",
		Messages:   []dto.Message{{Role: "user", Content: "hi"}},
	}
	req := &dto.OpenAIResponsesRequest{Model: "MiniMax-M2.7"}
	child, err := store.BuildContinuationSnapshot(root, req, []dto.Message{{Role: "assistant", Content: "ok"}}, "resp_child", "", 1)
	if err != nil {
		t.Fatalf("first continuation should succeed: %v", err)
	}
	_, err = store.BuildContinuationSnapshot(child, req, []dto.Message{{Role: "user", Content: "third"}}, "resp_grand", "", 2)
	if err == nil {
		t.Fatalf("expected depth limit error, got nil")
	}
}
