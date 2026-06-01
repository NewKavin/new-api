package codex

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
)

func TestSetupRequestHeaderPlainKeyUsesBearerAuth(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	header := http.Header{}
	info := &relaycommon.RelayInfo{
		IsStream: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:      "sk-test",
			ChannelType: constant.ChannelTypeCodex,
		},
	}

	adaptor := &Adaptor{}
	if err := adaptor.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("SetupRequestHeader returned error: %v", err)
	}

	if got, want := header.Get("Authorization"), "Bearer sk-test"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := header.Get("Accept"), "text/event-stream"; got != want {
		t.Fatalf("Accept = %q, want %q", got, want)
	}
}

func TestSetupRequestHeaderOAuthJSON(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	header := http.Header{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:      `{"access_token":"at","account_id":"acc"}`,
			ChannelType: constant.ChannelTypeCodex,
		},
	}

	adaptor := &Adaptor{}
	if err := adaptor.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("SetupRequestHeader returned error: %v", err)
	}

	if got, want := header.Get("Authorization"), "Bearer at"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := header.Get("chatgpt-account-id"), "acc"; got != want {
		t.Fatalf("chatgpt-account-id = %q, want %q", got, want)
	}
	if got, want := header.Get("OpenAI-Beta"), "responses=experimental"; got != want {
		t.Fatalf("OpenAI-Beta = %q, want %q", got, want)
	}
	if got, want := header.Get("originator"), "codex_cli_rs"; got != want {
		t.Fatalf("originator = %q, want %q", got, want)
	}
}

func TestUsesOpenAICompatModeKeyVariants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		info *relaycommon.RelayInfo
		want bool
	}{
		{
			name: "nil relay info",
			info: nil,
			want: false,
		},
		{
			name: "plain key",
			info: &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test"},
			},
			want: true,
		},
		{
			name: "oauth json",
			info: &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{ApiKey: `{"access_token":"at","account_id":"acc"}`},
			},
			want: false,
		},
		{
			name: "quoted oauth json",
			info: &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{ApiKey: `"{\"access_token\":\"at\",\"account_id\":\"acc\"}"`},
			},
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := usesOpenAICompatMode(tc.info); got != tc.want {
				t.Fatalf("usesOpenAICompatMode() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConvertOpenAIRequestPlainKey(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:      "sk-test",
			ChannelType: constant.ChannelTypeCodex,
		},
	}
	req := &dto.GeneralOpenAIRequest{Model: "minimaxai/minimax-m2.7"}

	got, err := adaptor.ConvertOpenAIRequest(nil, info, req)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	converted, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("ConvertOpenAIRequest returned %T, want *dto.GeneralOpenAIRequest", got)
	}
	if converted != req {
		t.Fatalf("ConvertOpenAIRequest should return the original request pointer")
	}
}

func TestGetRequestURLPlainKeyChatCompletions(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RequestURLPath: "/v1/chat/completions",
		RelayMode:      relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:         "sk-test",
			ChannelType:    constant.ChannelTypeCodex,
			ChannelBaseUrl: "https://example.com",
		},
	}

	got, err := adaptor.GetRequestURL(info)
	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}
	if want := "https://example.com/v1/chat/completions"; got != want {
		t.Fatalf("GetRequestURL = %q, want %q", got, want)
	}
}
