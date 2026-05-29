package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func TestResponsesCompactLocalHelper_NoSummaryPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	c.Set(common.RequestIdKey, "req_compact_local")

	store := service.GetResponsesStateStore()
	parent := &service.ResponsesStateSnapshot{
		ResponseID:     "resp_parent_local_compact",
		PromptCacheKey: "pcache_local_compact",
		Model:          "MiniMax-M2.7",
		CreatedAt:      time.Now().Unix(),
		Instructions:   common.StringToByteSlice(`"follow constraints"`),
		Messages: []dto.Message{
			{Role: "system", Content: "follow constraints"},
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		},
		ChainDepth: 1,
	}
	if err := store.Save(parent); err != nil {
		t.Fatalf("save parent snapshot failed: %v", err)
	}

	info := &relaycommon.RelayInfo{
		OriginModelName: "MiniMax-M2.7",
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	req := &dto.OpenAIResponsesRequest{
		Model:              "MiniMax-M2.7",
		PreviousResponseID: parent.ResponseID,
	}

	newAPIErr := ResponsesCompactLocalHelper(c, info, req)
	if newAPIErr != nil {
		t.Fatalf("ResponsesCompactLocalHelper returned error: %v", newAPIErr)
	}

	var compactionResp dto.OpenAIResponsesCompactionResponse
	if err := common.Unmarshal(common.StringToByteSlice(rec.Body.String()), &compactionResp); err != nil {
		t.Fatalf("failed to parse compaction response: %v", err)
	}
	if compactionResp.ID == "" {
		t.Fatalf("compaction response id should not be empty")
	}
	if compactionResp.Object != "response.compaction" {
		t.Fatalf("object = %q, want response.compaction", compactionResp.Object)
	}
	if len(compactionResp.Output) == 0 {
		t.Fatalf("output should not be empty")
	}

	saved, found, err := store.LoadByResponseID(compactionResp.ID)
	if err != nil {
		t.Fatalf("load compact snapshot failed: %v", err)
	}
	if !found {
		t.Fatalf("compacted snapshot not found in state store")
	}
	if saved.ParentResponseID != parent.ResponseID {
		t.Fatalf("parent_response_id = %q, want %q", saved.ParentResponseID, parent.ResponseID)
	}
	if len(saved.Messages) == 0 {
		t.Fatalf("saved messages should not be empty")
	}
}
