# Codex CLI Responses Compatibility Design

Date: 2026-05-29
Status: approved for implementation planning
Owner: Codex

## 1. Goal

Enable Codex CLI to use this gateway for real repository development through the OpenAI Responses API surface exposed at `/v1`, without adding a new external sidecar service and without changing the existing channel selection, billing, auth, or retry architecture.

This design is specifically scoped for:

- downstream client: Codex CLI only
- transport: HTTP Responses API
- required endpoints:
  - `POST /v1/responses`
  - `POST /v1/responses/compact`
- primary upstream models:
  - `MiniMax-M2.7`
  - `GLM-5.1` through `Zhipu V4 / OpenAI-compatible`

## 2. Expected Outcome

After this work is complete, Codex CLI should be able to:

- connect to this gateway as a custom Responses provider
- perform multi-turn coding-agent loops
- read repo context, propose edits, consume tool outputs, and continue
- survive long sessions by using `previous_response_id` and `/responses/compact`

This does not mean the behavior will equal OpenAI's hosted `gpt-5-codex`. It means the gateway will provide protocol and session semantics good enough for practical CLI-driven project development.

## 3. Non-Goals

This phase does not include:

- Codex desktop app support
- WebSocket transport
- realtime API support
- 100% parity with every OpenAI Responses event variant
- support for every existing channel type in the repo
- database migrations
- a new public channel type for "Codex compatibility"

## 4. Current Project State

The repo already contains important building blocks:

- downstream Responses routes:
  - `router/relay-router.go`
- Responses request DTOs and stream DTOs:
  - `dto/openai_request.go`
  - `dto/openai_response.go`
  - `dto/openai_compaction.go`
- chat-to-responses conversion:
  - `service/openaicompat/chat_to_responses.go`
- responses-to-chat response conversion:
  - `service/openaicompat/responses_to_chat.go`
- native Responses relay path:
  - `relay/responses_handler.go`
- Codex CLI affinity and header passthrough templates:
  - `setting/operation_setting/channel_affinity_setting.go`
- a hybrid Redis/in-memory cache utility suitable for response-state storage:
  - `pkg/cachex/hybrid_cache.go`

The repo also has a separate `Codex` channel type, but that adaptor is for forwarding to OpenAI's own Codex upstream (`/backend-api/codex/...`). It must not be repurposed as the compatibility layer for external Codex CLI clients.

## 5. Main Gap Analysis

The current repo is not yet sufficient for Codex CLI development usage because of four missing pieces:

1. Many channel adaptors do not implement `ConvertOpenAIResponsesRequest`.
2. There is no general `responses -> chat upstream` fallback path.
3. There is no server-side response-state layer for `previous_response_id`.
4. There is no local emulation path for `/v1/responses/compact` when the upstream is chat-based instead of native Responses.

The third and fourth gaps are critical. Without them, Codex CLI may work for short conversations but will degrade or break in real project-development sessions.

## 6. Release Scope

This design intentionally uses a narrow support matrix for the first implementation.

### 6.1 Supported downstream mode

- Codex CLI custom provider
- `wire_api = "responses"`
- `supports_websockets = false`

### 6.2 Supported upstream execution paths

Two execution paths will exist:

- Path A: native Responses relay
  - used when the selected channel already supports `/v1/responses`
- Path B: Responses-via-Chat fallback
  - used for selected first-phase channels that are chat-capable but not Responses-native

### 6.3 First-phase upstream allowlist for Path B

The first implementation should explicitly support the fallback path for:

- `MiniMax`
- `Zhipu V4`

The fallback path should stay closed for other channel types until separately validated.

## 7. Architecture

### 7.1 High-level flow

`Codex CLI -> /v1/responses or /v1/responses/compact -> gateway -> channel selection -> native responses OR responses-via-chat -> response-state persistence -> Responses JSON/SSE back to CLI`

### 7.2 New internal capability split

The gateway must distinguish between:

- native Responses capability
- Responses-via-Chat capability
- unsupported

Do not detect this by matching `"not implemented"` error strings. Use an explicit capability helper or allowlist.

Recommended helper:

- new file: `relay/channel_capability.go`

Recommended functions:

- `SupportsNativeResponses(apiType int, channelType int) bool`
- `SupportsResponsesViaChat(apiType int, channelType int) bool`

## 8. Required Components

### 8.1 Responses request to chat request converter

Add a new converter that turns a `dto.OpenAIResponsesRequest` into a `dto.GeneralOpenAIRequest`.

Recommended files:

- new: `service/openaicompat/responses_to_chat_request.go`
- existing wrapper update: `service/openai_chat_responses_compat.go`

Required behavior:

- map `input` items into ordered chat messages
- map `instructions` into a system/developer message
- map `tools` into chat tool definitions
- map `tool_choice`
- map `max_output_tokens` to chat token limits
- map `reasoning`
- preserve explicit zero values using pointer fields where needed
- convert:
  - `function_call` into assistant tool calls
  - `function_call_output` into tool messages

The output of this converter must be suitable for OpenAI-compatible chat upstreams used by `MiniMax` and `Zhipu V4`.

### 8.2 Response-state store

Add a server-side response-state layer keyed by generated Responses-style IDs.

Recommended files:

- new: `service/responses_state.go`
- optional new tests: `service/responses_state_test.go`

Recommended storage backend:

- `pkg/cachex.HybridCache`

Do not use the database in phase 1.

Recommended cache value shape:

```go
type ResponsesStateSnapshot struct {
    ResponseID         string
    ParentResponseID   string
    PromptCacheKey     string
    Model              string
    CreatedAt          int64
    Instructions       json.RawMessage
    Tools              json.RawMessage
    ToolChoice         json.RawMessage
    ParallelToolCalls  json.RawMessage
    Reasoning          *dto.Reasoning
    Metadata           json.RawMessage
    Messages           []dto.Message
    CompactSummary     string
}
```

Recommended defaults:

- TTL: 24h
- hard max chain depth: 256

The store must support:

- save snapshot after a successful response
- load snapshot by `previous_response_id`
- load snapshot by `prompt_cache_key` when useful for diagnostics or future extension
- create a compacted successor snapshot

### 8.3 Responses-style ID generation

The fallback path must not reuse `chatcmpl-*` IDs.

Add a new helper for Responses IDs:

- new helper in `relay/helper/common.go` or new file nearby

Recommended format:

- `resp_<request-id>`

Use this ID for:

- response JSON `id`
- `previous_response_id` chains
- compact successor IDs

Keep existing `GetResponseID()` unchanged for chat-completions flows.

### 8.4 Responses-via-Chat non-stream handler

Add a helper that:

1. loads state when `previous_response_id` is present
2. merges prior state with current request
3. converts merged state to chat request
4. sends the chat request upstream through the selected adaptor
5. converts the chat response into a Responses response
6. persists the new state snapshot

Recommended file:

- new: `relay/responses_via_chat.go`

Recommended main entrypoint:

- `ResponsesViaChatHelper(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError`

This helper should be invoked from `relay/responses_handler.go` when Path B is selected.

### 8.5 Responses-via-Chat stream handler

This is the most important protocol layer for Codex CLI.

The gateway must convert upstream chat SSE chunks into Responses SSE events. The first implementation only needs the event set required for coding-agent loops:

- `response.created`
- `response.output_item.added`
- `response.output_text.delta`
- `response.function_call_arguments.delta`
- `response.function_call_arguments.done`
- `response.output_item.done`
- `response.completed`

Recommended file:

- new: `relay/responses_via_chat_stream.go`

Recommended approach:

- consume existing chat stream chunks from upstream
- maintain per-output-item state in memory during the request
- emit Responses SSE in stable order

Important mapping rules:

- assistant text:
  - create a message output item
  - stream text as `response.output_text.delta`
- tool call:
  - emit `response.output_item.added` once for the function-call item
  - map tool argument deltas to `response.function_call_arguments.delta`
  - finalize with `response.function_call_arguments.done`
  - emit `response.output_item.done`
- final usage:
  - include in `response.completed`

Do not attempt every optional OpenAI event type in phase 1.

### 8.6 Chat response to Responses response converter

Add a direct converter for the fallback path instead of forcing the client to parse chat payloads.

Recommended file:

- new: `service/openaicompat/chat_response_to_responses.go`

Required output:

- `dto.OpenAIResponsesResponse`

For text outputs, build:

- `output: [{type:"message", role:"assistant", content:[{type:"output_text", text:"..."}]}]`

For tool calls, build:

- `output: [{type:"function_call", call_id:"...", name:"...", arguments:...}]`

### 8.7 `/v1/responses/compact` emulation for chat-based upstreams

This endpoint is required for development-grade Codex CLI use.

For channels using Path B, `/v1/responses/compact` must be emulated server-side instead of returning "unsupported".

Recommended file:

- new: `relay/responses_compact_local.go`

Recommended behavior:

1. load the parent snapshot from `previous_response_id`
2. reconstruct the full normalized transcript
3. split transcript into:
   - retained recent window
   - older history to summarize
4. generate a compact summary using the selected upstream model via an internal chat request
5. save a new compacted snapshot with a new `resp_*` ID
6. return `dto.OpenAIResponsesCompactionResponse`

Recommended compaction policy:

- keep the last 8 message turns verbatim
- keep all unresolved tool-result adjacency intact
- summarize everything older into one synthetic system message

Recommended summary prompt requirements:

- preserve repo facts
- preserve user requirements and constraints
- preserve completed decisions
- preserve unresolved TODOs
- preserve tool-created facts that future code changes depend on
- never invent files, commands, or test results

Recommended first implementation detail:

- perform compaction only when older history exists
- if not enough history exists, create a successor snapshot without summarization

Billing rule:

- compact requests should be billed by the actual compaction summary call usage

### 8.8 Affinity and Codex CLI headers

Keep the existing `prompt_cache_key`-based affinity rule and extend it slightly.

Existing file:

- `setting/operation_setting/channel_affinity_setting.go`

Current header passthrough already includes:

- `Originator`
- `Session_id`
- `User-Agent`
- `X-Codex-Beta-Features`
- `X-Codex-Turn-Metadata`

Recommended additions for phase 1 if observed in real traffic:

- `X-Codex-Installation-Id`
- `X-Codex-Parent-Thread-Id`
- `X-Codex-Window-Id`
- `X-Codex-Turn-State`

Do not block the implementation on guessing every optional Codex header. Preserve known useful headers and keep the mechanism extensible.

## 9. Routing Changes

Update `relay/responses_handler.go` so the handler chooses between:

- native Responses path
- local compact path
- Responses-via-Chat fallback

Recommended order:

1. if relay mode is `ResponsesCompact` and channel is Path B:
   - use local compact helper
2. else if native Responses is supported:
   - use current logic
3. else if Responses-via-Chat is supported:
   - use fallback helper
4. else:
   - return unsupported

## 10. Logging and Observability

Add targeted debug logs for:

- selected execution path
- generated `resp_*` IDs
- state load/save hits and misses
- compact summary call start/finish
- fallback stream event translation failures

Do not log full prompts or tool outputs at info level.

## 11. Exact Files Expected to Change

### 11.1 Existing files

- `relay/responses_handler.go`
- `service/openai_chat_responses_compat.go`
- `relay/helper/common.go`
- `setting/operation_setting/channel_affinity_setting.go`
- `controller/channel-test.go`

### 11.2 New files

- `service/openaicompat/responses_to_chat_request.go`
- `service/openaicompat/chat_response_to_responses.go`
- `service/responses_state.go`
- `relay/channel_capability.go`
- `relay/responses_via_chat.go`
- `relay/responses_via_chat_stream.go`
- `relay/responses_compact_local.go`

### 11.3 Tests to add

- `service/openaicompat/responses_to_chat_request_test.go`
- `service/openaicompat/chat_response_to_responses_test.go`
- `service/responses_state_test.go`
- `relay/responses_via_chat_test.go`
- `relay/responses_via_chat_stream_test.go`
- `relay/responses_compact_local_test.go`

## 12. Recommended Implementation Order

1. Add capability helper and path selection scaffolding.
2. Add Responses request to chat request conversion.
3. Add response-state store and `resp_*` ID helper.
4. Add non-stream Responses-via-Chat fallback.
5. Add stream event translation.
6. Add local compact emulation.
7. Extend tests and channel test support.
8. Verify on `MiniMax-M2.7`.
9. Verify on `GLM-5.1` through `Zhipu V4`.

## 13. Validation Matrix

The implementation is not done until all of the following work:

### 13.1 MiniMax-M2.7

- single-turn `/v1/responses`
- multi-turn `previous_response_id`
- stream text output
- stream tool call
- compact followed by another generation

### 13.2 GLM-5.1 through Zhipu V4

- single-turn `/v1/responses`
- multi-turn `previous_response_id`
- stream text output
- tool call loop if the model supports it in the configured upstream mode
- compact followed by another generation

### 13.3 Regression checks

- existing native Responses channels still work
- chat-completions routes still work
- billing/quota still posts once per completed request
- affinity still binds on `prompt_cache_key`

## 14. Key Risks and Mitigations

### Risk: compact summary drifts from original conversation

Mitigation:

- summarize only older history
- keep recent turns verbatim
- use a strict compaction prompt
- store summary text in snapshot for debugging

### Risk: tool-call stream translation is incomplete

Mitigation:

- target only the minimal required Responses event set
- test both text-only and tool-call streams
- fail loudly on unsupported chunk patterns during development

### Risk: fallback path becomes too broad

Mitigation:

- keep explicit allowlist for phase 1
- validate `MiniMax` first, `Zhipu V4` second

### Risk: no Redis in some deployments

Mitigation:

- use `HybridCache`; Redis if enabled, in-memory otherwise
- document that in-memory fallback loses state across process restarts

## 15. Decision Summary

The gateway should not implement "Codex support" as a separate upstream channel. It should implement Codex CLI compatibility as a downstream Responses protocol layer with:

- native Responses passthrough when available
- Responses-via-Chat fallback for selected channels
- server-side response-state persistence
- server-side compact emulation

This is the smallest design that is still credible for actual CLI-driven project development.

## 16. External References

Official references checked on 2026-05-29:

- Codex CLI getting started:
  - `https://help.openai.com/en/articles/11096431`
- Codex CLI sign-in and API usage:
  - `https://help.openai.com/en/articles/11381614-api-codex-cli-and-sign-in-with-chatgpt`
- Codex agent loop engineering writeup:
  - `https://openai.com/index/unrolling-the-codex-agent-loop/`
- Codex config reference:
  - `https://developers.openai.com/codex/config-reference`
- Responses compact API reference:
  - `https://platform.openai.com/docs/api-reference/responses/compact/`
