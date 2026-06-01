package relay

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func TestResponsesViaChatIntegration_MiniMaxNonStreamInstructionsReplacement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	disableConsumeLogForTest(t)

	var (
		reqMu       sync.Mutex
		chatReqs    []dto.GeneralOpenAIRequest
		requestTurn int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/text/chatcompletion_v2" {
			http.NotFound(w, r)
			return
		}
		var req dto.GeneralOpenAIRequest
		if err := common.DecodeJson(r.Body, &req); err != nil {
			t.Fatalf("decode minimax request failed: %v", err)
		}
		reqMu.Lock()
		chatReqs = append(chatReqs, req)
		requestTurn = len(chatReqs)
		reqMu.Unlock()

		resp := dto.OpenAITextResponse{
			Id:      fmt.Sprintf("chatcmpl_mm_%d", requestTurn),
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "MiniMax-M2.7",
			Choices: []dto.OpenAITextResponseChoice{
				{
					Index: 0,
					Message: dto.Message{
						Role:    "assistant",
						Content: fmt.Sprintf("mm-turn-%d", requestTurn),
					},
					FinishReason: "stop",
				},
			},
			Usage: dto.Usage{},
		}
		raw, _ := common.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}))
	defer server.Close()

	firstResp := runResponsesViaChatTurn(
		t,
		"it_mm_replace_1",
		newMiniMaxRelayInfo(server.URL, "MiniMax-M2.7"),
		&dto.OpenAIResponsesRequest{
			Model:        "MiniMax-M2.7",
			Input:        mustMarshal(t, "hello"),
			Instructions: mustMarshal(t, "old instructions"),
			StreamOptions: &dto.StreamOptions{
				IncludeUsage: true,
			},
		},
	)

	secondResp := runResponsesViaChatTurn(
		t,
		"it_mm_replace_2",
		newMiniMaxRelayInfo(server.URL, "MiniMax-M2.7"),
		&dto.OpenAIResponsesRequest{
			Model:              "MiniMax-M2.7",
			Input:              mustMarshal(t, "next"),
			Instructions:       mustMarshal(t, "new instructions"),
			PreviousResponseID: firstResp.ID,
		},
	)

	reqMu.Lock()
	if len(chatReqs) != 2 {
		t.Fatalf("captured request count = %d, want 2", len(chatReqs))
	}
	secondReq := chatReqs[1]
	firstReq := chatReqs[0]
	reqMu.Unlock()

	if firstReq.Stream == nil {
		t.Fatalf("first chat request stream should be explicitly false")
	}
	if *firstReq.Stream {
		t.Fatalf("first chat request stream = true, want false")
	}
	if firstReq.StreamOptions != nil {
		t.Fatalf("first chat request should not include stream_options for non-stream responses fallback")
	}

	systemMessages := 0
	for _, msg := range secondReq.Messages {
		if msg.Role != "system" {
			continue
		}
		systemMessages++
		if msg.StringContent() == "old instructions" {
			t.Fatalf("old instructions must not remain in continuation request")
		}
	}
	if systemMessages != 1 {
		t.Fatalf("system messages in continuation = %d, want 1", systemMessages)
	}
	if secondReq.Messages[0].Role != "system" || secondReq.Messages[0].StringContent() != "new instructions" {
		t.Fatalf("leading system instruction mismatch: %+v", secondReq.Messages[0])
	}

	store := service.GetResponsesStateStore()
	saved, found, err := store.LoadByResponseID(secondResp.ID)
	if err != nil {
		t.Fatalf("load continuation snapshot failed: %v", err)
	}
	if !found {
		t.Fatalf("continuation snapshot not found")
	}
	if string(saved.Instructions) != "\"new instructions\"" {
		t.Fatalf("snapshot instructions = %s, want \"new instructions\"", saved.Instructions)
	}
	if len(saved.Messages) == 0 {
		t.Fatalf("snapshot messages should not be empty")
	}
	if saved.Messages[0].Role == "system" && saved.Messages[0].StringContent() == "new instructions" {
		t.Fatalf("instruction-rendered system message should not be persisted in transcript")
	}
}

func TestResponsesViaChatIntegration_MiniMaxStreamToolCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	constant.StreamingTimeout = 30

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/text/chatcompletion_v2" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_stream_1\",\"model\":\"MiniMax-M2.7\",\"created\":1700000001,\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"list_files\",\"arguments\":\"{\"}}]}}]}\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_stream_1\",\"model\":\"MiniMax-M2.7\",\"created\":1700000001,\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"path\\\":\\\".\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n")
		_, _ = io.WriteString(w, "data: [DONE]\n")
	}))
	defer server.Close()

	c, rec := newResponsesTestContext("it_mm_stream_1", "/v1/responses")
	info := newMiniMaxRelayInfo(server.URL, "MiniMax-M2.7")
	req := &dto.OpenAIResponsesRequest{
		Model:  "MiniMax-M2.7",
		Input:  mustMarshal(t, "run a tool"),
		Stream: lo.ToPtr(true),
	}
	chatReq, err := service.ResponsesRequestToChatCompletionsRequest(req)
	if err != nil {
		t.Fatalf("convert request failed: %v", err)
	}

	usage, msg, _, newAPIErr := doResponsesViaChatStream(c, info, req, chatReq, "resp_it_mm_stream_1", time.Now().Unix())
	if newAPIErr != nil {
		t.Fatalf("doResponsesViaChatStream returned error: %v", newAPIErr)
	}
	if usage == nil {
		t.Fatalf("usage should not be nil")
	}
	if len(msg.ParseToolCalls()) != 1 {
		t.Fatalf("assistant tool_calls len = %d, want 1", len(msg.ParseToolCalls()))
	}
	toolCall := msg.ParseToolCalls()[0]
	if toolCall.ID != "call_1" || toolCall.Function.Name != "list_files" {
		t.Fatalf("unexpected tool call payload: %+v", toolCall)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: response.function_call_arguments.delta") {
		t.Fatalf("stream output missing response.function_call_arguments.delta")
	}
	if !strings.Contains(body, "event: response.function_call_arguments.done") {
		t.Fatalf("stream output missing response.function_call_arguments.done")
	}
}

func TestResponsesViaChatIntegration_ZhipuContinuation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	disableConsumeLogForTest(t)

	var (
		reqMu    sync.Mutex
		chatReqs []dto.GeneralOpenAIRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/paas/v4/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var req dto.GeneralOpenAIRequest
		if err := common.DecodeJson(r.Body, &req); err != nil {
			t.Fatalf("decode zhipu request failed: %v", err)
		}
		reqMu.Lock()
		chatReqs = append(chatReqs, req)
		turn := len(chatReqs)
		reqMu.Unlock()

		resp := dto.OpenAITextResponse{
			Id:      fmt.Sprintf("chatcmpl_zp_%d", turn),
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "glm-5.1",
			Choices: []dto.OpenAITextResponseChoice{
				{
					Index: 0,
					Message: dto.Message{
						Role:    "assistant",
						Content: fmt.Sprintf("zp-turn-%d", turn),
					},
					FinishReason: "stop",
				},
			},
			Usage: dto.Usage{},
		}
		raw, _ := common.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}))
	defer server.Close()

	firstResp := runResponsesViaChatTurn(
		t,
		"it_zp_continue_1",
		newZhipuRelayInfo(server.URL, "glm-5.1"),
		&dto.OpenAIResponsesRequest{
			Model: "glm-5.1",
			Input: mustMarshal(t, "first turn"),
		},
	)

	_ = runResponsesViaChatTurn(
		t,
		"it_zp_continue_2",
		newZhipuRelayInfo(server.URL, "glm-5.1"),
		&dto.OpenAIResponsesRequest{
			Model:              "glm-5.1",
			Input:              mustMarshal(t, "second turn"),
			PreviousResponseID: firstResp.ID,
		},
	)

	reqMu.Lock()
	if len(chatReqs) != 2 {
		t.Fatalf("captured request count = %d, want 2", len(chatReqs))
	}
	secondReq := chatReqs[1]
	reqMu.Unlock()

	contains := func(role string, content string) bool {
		for _, msg := range secondReq.Messages {
			if msg.Role == role && msg.StringContent() == content {
				return true
			}
		}
		return false
	}
	if !contains("user", "first turn") {
		t.Fatalf("continuation request should contain first user turn")
	}
	if !contains("assistant", "zp-turn-1") {
		t.Fatalf("continuation request should contain first assistant turn")
	}
	if !contains("user", "second turn") {
		t.Fatalf("continuation request should contain second user input")
	}
}

func TestResponsesViaChatIntegration_CompactThenContinue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	disableConsumeLogForTest(t)

	var (
		reqMu    sync.Mutex
		chatReqs []dto.GeneralOpenAIRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/text/chatcompletion_v2" {
			http.NotFound(w, r)
			return
		}
		var req dto.GeneralOpenAIRequest
		if err := common.DecodeJson(r.Body, &req); err != nil {
			t.Fatalf("decode minimax request failed: %v", err)
		}
		reqMu.Lock()
		chatReqs = append(chatReqs, req)
		turn := len(chatReqs)
		reqMu.Unlock()

		resp := dto.OpenAITextResponse{
			Id:      fmt.Sprintf("chatcmpl_compact_%d", turn),
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "MiniMax-M2.7",
			Choices: []dto.OpenAITextResponseChoice{
				{
					Index: 0,
					Message: dto.Message{
						Role: "assistant",
						Content: func() string {
							if turn == 1 {
								return "summary for compacted context"
							}
							return "continue after compact"
						}(),
					},
					FinishReason: "stop",
				},
			},
			Usage: dto.Usage{},
		}
		raw, _ := common.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}))
	defer server.Close()

	store := service.GetResponsesStateStore()
	parentID := "resp_parent_compact_integration"
	parentMessages := make([]dto.Message, 0, 24)
	for i := 0; i < 12; i++ {
		parentMessages = append(parentMessages, dto.Message{Role: "user", Content: fmt.Sprintf("u-%d", i)})
		parentMessages = append(parentMessages, dto.Message{Role: "assistant", Content: fmt.Sprintf("a-%d", i)})
	}
	if err := store.Save(&service.ResponsesStateSnapshot{
		ResponseID:     parentID,
		PromptCacheKey: "pcache_compact_integration",
		Model:          "MiniMax-M2.7",
		CreatedAt:      time.Now().Unix(),
		Instructions:   mustMarshal(t, "keep coding constraints"),
		Messages:       parentMessages,
		ChainDepth:     1,
	}); err != nil {
		t.Fatalf("save parent snapshot failed: %v", err)
	}

	compactCtx, compactRec := newResponsesTestContext("it_compact_1", "/v1/responses/compact")
	compactReq := &dto.OpenAIResponsesRequest{
		Model:              "MiniMax-M2.7",
		PreviousResponseID: parentID,
	}
	if newAPIErr := ResponsesCompactLocalHelper(compactCtx, newMiniMaxRelayInfo(server.URL, "MiniMax-M2.7"), compactReq); newAPIErr != nil {
		t.Fatalf("ResponsesCompactLocalHelper returned error: %v", newAPIErr)
	}

	var compactResp dto.OpenAIResponsesCompactionResponse
	if err := common.Unmarshal(common.StringToByteSlice(compactRec.Body.String()), &compactResp); err != nil {
		t.Fatalf("parse compaction response failed: %v", err)
	}
	if compactResp.ID == "" {
		t.Fatalf("compaction response id should not be empty")
	}

	_ = runResponsesViaChatTurn(
		t,
		"it_compact_continue_1",
		newMiniMaxRelayInfo(server.URL, "MiniMax-M2.7"),
		&dto.OpenAIResponsesRequest{
			Model:              "MiniMax-M2.7",
			Input:              mustMarshal(t, "what should we do next"),
			PreviousResponseID: compactResp.ID,
		},
	)

	reqMu.Lock()
	if len(chatReqs) < 2 {
		t.Fatalf("captured request count = %d, want >= 2", len(chatReqs))
	}
	continueReq := chatReqs[len(chatReqs)-1]
	reqMu.Unlock()

	hasSummary := false
	for _, msg := range continueReq.Messages {
		if msg.Role == "system" && strings.Contains(msg.StringContent(), "[COMPACT SUMMARY]") {
			hasSummary = true
			break
		}
	}
	if !hasSummary {
		t.Fatalf("continuation request should include compact summary system message")
	}
}

func runResponsesViaChatTurn(t *testing.T, requestID string, info *relaycommon.RelayInfo, req *dto.OpenAIResponsesRequest) dto.OpenAIResponsesResponse {
	t.Helper()
	c, rec := newResponsesTestContext(requestID, "/v1/responses")
	if newAPIErr := ResponsesViaChatHelper(c, info, req); newAPIErr != nil {
		t.Fatalf("ResponsesViaChatHelper returned error: %v", newAPIErr)
	}
	var resp dto.OpenAIResponsesResponse
	if err := common.Unmarshal(common.StringToByteSlice(rec.Body.String()), &resp); err != nil {
		t.Fatalf("parse responses payload failed: %v; body=%s", err, rec.Body.String())
	}
	if !strings.HasPrefix(resp.ID, "resp_") {
		t.Fatalf("response id = %q, want prefix resp_", resp.ID)
	}
	return resp
}

func newResponsesTestContext(requestID string, path string) (*gin.Context, *httptest.ResponseRecorder) {
	service.InitHttpClient()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(common.RequestIdKey, requestID)
	return c, rec
}

func newMiniMaxRelayInfo(baseURL string, model string) *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{
		OriginModelName: model,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeMiniMax,
			ChannelType:       constant.ChannelTypeMiniMax,
			ChannelBaseUrl:    baseURL,
			ApiKey:            "sk-test",
			UpstreamModelName: model,
		},
	}
	info.SetEstimatePromptTokens(16)
	return info
}

func newZhipuRelayInfo(baseURL string, model string) *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{
		OriginModelName: model,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeZhipuV4,
			ChannelType:       constant.ChannelTypeZhipu_v4,
			ChannelBaseUrl:    baseURL,
			ApiKey:            "sk-test",
			UpstreamModelName: model,
		},
	}
	info.SetEstimatePromptTokens(16)
	return info
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := common.Marshal(v)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return raw
}

func disableConsumeLogForTest(t *testing.T) {
	t.Helper()
	old := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() {
		common.LogConsumeEnabled = old
	})
}
