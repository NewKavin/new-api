package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/samber/hot"
)

const (
	responsesStateNamespace      = "new-api:responses_state:v1"
	defaultResponsesStateTTL     = 24 * time.Hour
	defaultResponsesStateMaxSize = 100_000
	defaultResponsesStateDepth   = 256
)

type ResponsesStateSnapshot struct {
	ResponseID        string          `json:"response_id"`
	ParentResponseID  string          `json:"parent_response_id,omitempty"`
	PromptCacheKey    string          `json:"prompt_cache_key,omitempty"`
	Model             string          `json:"model,omitempty"`
	CreatedAt         int64           `json:"created_at"`
	Instructions      json.RawMessage `json:"instructions,omitempty"`
	Tools             json.RawMessage `json:"tools,omitempty"`
	ToolChoice        json.RawMessage `json:"tool_choice,omitempty"`
	ParallelToolCalls json.RawMessage `json:"parallel_tool_calls,omitempty"`
	Reasoning         *dto.Reasoning  `json:"reasoning,omitempty"`
	Metadata          json.RawMessage `json:"metadata,omitempty"`
	Messages          []dto.Message   `json:"messages,omitempty"`
	CompactSummary    string          `json:"compact_summary,omitempty"`
	ChainDepth        int             `json:"chain_depth,omitempty"`
}

type ResponsesStateStore struct {
	cache         *cachex.HybridCache[ResponsesStateSnapshot]
	ttl           time.Duration
	maxChainDepth int
}

var (
	responsesStateStoreOnce sync.Once
	responsesStateStore     *ResponsesStateStore
)

func GetResponsesStateStore() *ResponsesStateStore {
	responsesStateStoreOnce.Do(func() {
		responsesStateStore = &ResponsesStateStore{
			cache: cachex.NewHybridCache[ResponsesStateSnapshot](cachex.HybridCacheConfig[ResponsesStateSnapshot]{
				Namespace: cachex.Namespace(responsesStateNamespace),
				Redis:     common.RDB,
				RedisEnabled: func() bool {
					return common.RedisEnabled && common.RDB != nil
				},
				RedisCodec: cachex.JSONCodec[ResponsesStateSnapshot]{},
				Memory: func() *hot.HotCache[string, ResponsesStateSnapshot] {
					return hot.NewHotCache[string, ResponsesStateSnapshot](hot.LRU, defaultResponsesStateMaxSize).
						WithTTL(defaultResponsesStateTTL).
						WithJanitor().
						Build()
				},
			}),
			ttl:           defaultResponsesStateTTL,
			maxChainDepth: defaultResponsesStateDepth,
		}
	})
	return responsesStateStore
}

func newResponsesStateStoreForTest(ttl time.Duration, maxDepth int) *ResponsesStateStore {
	if ttl <= 0 {
		ttl = time.Hour
	}
	if maxDepth <= 0 {
		maxDepth = defaultResponsesStateDepth
	}
	storeNamespace := fmt.Sprintf("%s:test:%d", responsesStateNamespace, time.Now().UnixNano())
	return &ResponsesStateStore{
		cache: cachex.NewHybridCache[ResponsesStateSnapshot](cachex.HybridCacheConfig[ResponsesStateSnapshot]{
			Namespace:  cachex.Namespace(storeNamespace),
			RedisCodec: cachex.JSONCodec[ResponsesStateSnapshot]{},
			Memory: func() *hot.HotCache[string, ResponsesStateSnapshot] {
				return hot.NewHotCache[string, ResponsesStateSnapshot](hot.LRU, 256).
					WithTTL(ttl).
					WithJanitor().
					Build()
			},
		}),
		ttl:           ttl,
		maxChainDepth: maxDepth,
	}
}

func (s *ResponsesStateStore) Save(snapshot *ResponsesStateSnapshot) error {
	if s == nil || s.cache == nil {
		return errors.New("responses state store is nil")
	}
	if snapshot == nil {
		return errors.New("snapshot is nil")
	}
	snapshot.ResponseID = strings.TrimSpace(snapshot.ResponseID)
	if snapshot.ResponseID == "" {
		return errors.New("response_id is required")
	}
	if snapshot.CreatedAt <= 0 {
		snapshot.CreatedAt = time.Now().Unix()
	}
	if snapshot.ChainDepth <= 0 {
		snapshot.ChainDepth = 1
	}

	if err := s.cache.SetWithTTL(stateIDKey(snapshot.ResponseID), *snapshot, s.ttl); err != nil {
		return err
	}
	if key := strings.TrimSpace(snapshot.PromptCacheKey); key != "" {
		if err := s.cache.SetWithTTL(promptKeyIndex(key), *snapshot, s.ttl); err != nil {
			return err
		}
	}
	return nil
}

func (s *ResponsesStateStore) LoadByResponseID(responseID string) (*ResponsesStateSnapshot, bool, error) {
	if s == nil || s.cache == nil {
		return nil, false, errors.New("responses state store is nil")
	}
	v, found, err := s.cache.Get(stateIDKey(responseID))
	if err != nil || !found {
		return nil, found, err
	}
	return &v, true, nil
}

func (s *ResponsesStateStore) LoadByPromptCacheKey(promptCacheKey string) (*ResponsesStateSnapshot, bool, error) {
	if s == nil || s.cache == nil {
		return nil, false, errors.New("responses state store is nil")
	}
	v, found, err := s.cache.Get(promptKeyIndex(promptCacheKey))
	if err != nil || !found {
		return nil, found, err
	}
	return &v, true, nil
}

func (s *ResponsesStateStore) BuildContinuationSnapshot(
	parent *ResponsesStateSnapshot,
	req *dto.OpenAIResponsesRequest,
	newMessages []dto.Message,
	responseID string,
	promptCacheKey string,
	createdAt int64,
) (*ResponsesStateSnapshot, error) {
	if s == nil {
		return nil, errors.New("responses state store is nil")
	}
	if req == nil {
		return nil, errors.New("request is nil")
	}
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return nil, errors.New("response_id is required")
	}
	if createdAt <= 0 {
		createdAt = time.Now().Unix()
	}

	base := &ResponsesStateSnapshot{
		ResponseID:        responseID,
		PromptCacheKey:    strings.TrimSpace(promptCacheKey),
		Model:             strings.TrimSpace(req.Model),
		CreatedAt:         createdAt,
		Instructions:      req.Instructions,
		Tools:             req.Tools,
		ToolChoice:        req.ToolChoice,
		ParallelToolCalls: req.ParallelToolCalls,
		Reasoning:         req.Reasoning,
		Metadata:          req.Metadata,
		Messages:          append([]dto.Message(nil), newMessages...),
		ChainDepth:        1,
	}

	if parent == nil {
		return base, nil
	}

	nextDepth := parent.ChainDepth + 1
	if nextDepth <= 1 {
		nextDepth = 2
	}
	if nextDepth > s.maxChainDepth {
		return nil, fmt.Errorf("responses state depth exceeded: %d > %d", nextDepth, s.maxChainDepth)
	}

	base.ParentResponseID = parent.ResponseID
	base.ChainDepth = nextDepth
	if base.Model == "" {
		base.Model = parent.Model
	}
	if len(base.Instructions) == 0 {
		base.Instructions = parent.Instructions
	}
	if len(base.Tools) == 0 {
		base.Tools = parent.Tools
	}
	if len(base.ToolChoice) == 0 {
		base.ToolChoice = parent.ToolChoice
	}
	if len(base.ParallelToolCalls) == 0 {
		base.ParallelToolCalls = parent.ParallelToolCalls
	}
	if base.Reasoning == nil {
		base.Reasoning = parent.Reasoning
	}
	if len(base.Metadata) == 0 {
		base.Metadata = parent.Metadata
	}
	base.Messages = append(append([]dto.Message(nil), parent.Messages...), newMessages...)
	return base, nil
}

func stateIDKey(responseID string) string {
	return "resp:" + strings.TrimSpace(responseID)
}

func promptKeyIndex(promptCacheKey string) string {
	return "prompt:" + strings.TrimSpace(promptCacheKey)
}
