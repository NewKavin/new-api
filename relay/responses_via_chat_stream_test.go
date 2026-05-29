package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func TestResponsesViaChatStreamHandler_TextFlow(t *testing.T) {
	constant.StreamingTimeout = 30

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "req_stream_text")

	streamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_1","model":"MiniMax-M2.7","created":1700000001,"choices":[{"index":0,"delta":{"role":"assistant","content":"hel"}}]}`,
		`data: {"id":"chatcmpl_1","model":"MiniMax-M2.7","created":1700000001,"choices":[{"index":0,"delta":{"content":"lo"}}]}`,
		`data: {"id":"chatcmpl_1","model":"MiniMax-M2.7","created":1700000001,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`data: [DONE]`,
	}, "\n")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "MiniMax-M2.7",
		},
	}
	info.SetEstimatePromptTokens(12)
	req := &dto.OpenAIResponsesRequest{
		Model:  "MiniMax-M2.7",
		Stream: lo.ToPtr(true),
	}

	usage, msg, createdAt, err := ResponsesViaChatStreamHandler(c, info, resp, req, "resp_req_stream_text", 1700000000)
	if err != nil {
		t.Fatalf("ResponsesViaChatStreamHandler returned error: %v", err)
	}
	if createdAt == 0 {
		t.Fatalf("createdAt should not be zero")
	}
	if usage == nil || usage.TotalTokens == 0 {
		t.Fatalf("usage should be parsed from stream")
	}
	if msg.StringContent() != "hello" {
		t.Fatalf("assistant message content = %q, want hello", msg.StringContent())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: response.created") {
		t.Fatalf("missing response.created event: %s", body)
	}
	if !strings.Contains(body, "event: response.output_text.delta") {
		t.Fatalf("missing response.output_text.delta event: %s", body)
	}
	if !strings.Contains(body, "event: response.output_item.done") {
		t.Fatalf("missing response.output_item.done event: %s", body)
	}
	if !strings.Contains(body, "event: response.completed") {
		t.Fatalf("missing response.completed event: %s", body)
	}
}

func TestResponsesViaChatStreamHandler_ToolCallFlow(t *testing.T) {
	constant.StreamingTimeout = 30

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "req_stream_tool")

	streamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_2","model":"MiniMax-M2.7","created":1700000002,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"list_files","arguments":"{"}}]}}]}`,
		`data: {"id":"chatcmpl_2","model":"MiniMax-M2.7","created":1700000002,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "MiniMax-M2.7",
		},
	}
	info.SetEstimatePromptTokens(12)
	req := &dto.OpenAIResponsesRequest{
		Model:  "MiniMax-M2.7",
		Stream: lo.ToPtr(true),
	}

	_, msg, _, err := ResponsesViaChatStreamHandler(c, info, resp, req, "resp_req_stream_tool", 1700000000)
	if err != nil {
		t.Fatalf("ResponsesViaChatStreamHandler returned error: %v", err)
	}

	toolCalls := msg.ParseToolCalls()
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls length = %d, want 1", len(toolCalls))
	}
	if toolCalls[0].ID != "call_1" || toolCalls[0].Function.Name != "list_files" {
		t.Fatalf("unexpected tool call payload: %+v", toolCalls[0])
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: response.function_call_arguments.delta") {
		t.Fatalf("missing response.function_call_arguments.delta event: %s", body)
	}
	if !strings.Contains(body, "event: response.function_call_arguments.done") {
		t.Fatalf("missing response.function_call_arguments.done event: %s", body)
	}
}
