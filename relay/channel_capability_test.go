package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
)

func TestSupportsNativeResponses(t *testing.T) {
	if !SupportsNativeResponses(constant.APITypeOpenAI, constant.ChannelTypeOpenAI) {
		t.Fatalf("openai should support native responses")
	}
	if SupportsNativeResponses(constant.APITypeMiniMax, constant.ChannelTypeMiniMax) {
		t.Fatalf("minimax should not be marked native responses in phase1")
	}
}

func TestSupportsResponsesViaChat(t *testing.T) {
	if !SupportsResponsesViaChat(constant.APITypeMiniMax, constant.ChannelTypeMiniMax) {
		t.Fatalf("minimax should support responses via chat fallback")
	}
	if !SupportsResponsesViaChat(constant.APITypeZhipuV4, constant.ChannelTypeZhipu_v4) {
		t.Fatalf("zhipu_v4 should support responses via chat fallback")
	}
	if SupportsResponsesViaChat(constant.APITypeOpenAI, constant.ChannelTypeOpenAI) {
		t.Fatalf("openai should not use responses via chat fallback")
	}
}
