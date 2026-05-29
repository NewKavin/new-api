# Handoff: Codex CLI Responses Compatibility Implementation

Date: 2026-05-29
Target implementation model: `gpt-5.3-codex`
Status: ready for execution

## 1. Mission

Implement the design in:

- `docs/superpowers/specs/2026-05-29-codex-cli-responses-compat-design.md`

The intended outcome is practical Codex CLI support for:

- `MiniMax-M2.7`
- `GLM-5.1` via `Zhipu V4 / OpenAI-compatible`

This implementation is for actual repo-development sessions, not just toy prompt/response demos.

## 2. Constraints

Follow repo rules:

- use `common.Marshal`, `common.Unmarshal`, `common.DecodeJson`, not direct `encoding/json` marshal/unmarshal calls in business code
- keep cross-database compatibility untouched
- do not repurpose the existing `Codex` channel type
- do not add a DB migration
- prefer `pkg/cachex.HybridCache` for state
- keep the scope limited to Codex CLI over HTTP Responses

## 3. First Files to Read

Read these before editing:

- `relay/responses_handler.go`
- `service/openaicompat/chat_to_responses.go`
- `service/openaicompat/responses_to_chat.go`
- `dto/openai_request.go`
- `dto/openai_response.go`
- `dto/openai_compaction.go`
- `relay/channel/minimax/adaptor.go`
- `relay/channel/zhipu_4v/adaptor.go`
- `setting/operation_setting/channel_affinity_setting.go`
- `pkg/cachex/hybrid_cache.go`

## 4. Required Deliverables

### Deliverable A: capability routing

Add explicit routing between:

- native Responses
- Responses-via-Chat
- unsupported

Do not detect by error-string matching.

### Deliverable B: request conversion

Add `Responses request -> GeneralOpenAIRequest` conversion.

Must support:

- plain user text
- assistant text replay
- tool calls
- tool outputs
- instructions
- tools
- tool choice
- reasoning
- max token mapping

### Deliverable C: response-state store

Add a cache-backed store for:

- `response_id -> normalized snapshot`

Recommended response ID format:

- `resp_<request-id>`

Do not use `chatcmpl-*` for the Responses fallback path.

### Deliverable D: non-stream fallback

Implement the local fallback path for chat-based upstreams.

This means:

- load previous state
- append current request
- send chat request upstream
- translate chat response to Responses response
- persist new state

### Deliverable E: stream fallback

Implement Responses SSE emission from upstream chat SSE.

Must support:

- assistant text deltas
- tool call argument deltas
- final usage in completed event

### Deliverable F: local compact

Implement `/v1/responses/compact` for fallback channels with server-side state compaction.

This is required before claiming support for real Codex CLI project development.

## 5. Suggested Edit Order

1. Add `relay/channel_capability.go`
2. Add `service/openaicompat/responses_to_chat_request.go`
3. Add `service/responses_state.go`
4. Add a Responses-specific ID helper
5. Add `service/openaicompat/chat_response_to_responses.go`
6. Add `relay/responses_via_chat.go`
7. Add `relay/responses_via_chat_stream.go`
8. Add `relay/responses_compact_local.go`
9. Wire path selection in `relay/responses_handler.go`
10. Extend tests

## 6. Practical Implementation Notes

### 6.1 Keep the allowlist narrow

Only enable Responses-via-Chat for phase 1 channels:

- `MiniMax`
- `Zhipu V4`

Do not try to generalize to all adaptors in the same change.

### 6.2 Use normalized chat transcript as the stored state

Do not store raw client request bodies as the primary continuation artifact.

Store the normalized message history that the chat fallback actually needs.

### 6.3 Keep `instructions` stable across turns

If a request provides `instructions`, make them part of the stored state. Continuations must preserve that semantic.

### 6.4 Preserve tool adjacency

Never compact or reorder tool use and tool result pairs independently.

### 6.5 Billing

Compact emulation should bill the compaction summary request itself. Normal generation should bill once, after the actual generation response completes.

### 6.6 In-memory fallback

If Redis is unavailable, in-memory state is acceptable for phase 1, but mention in final notes that restart loses continuation state.

## 7. Minimum Test Plan

### Unit tests

- responses input to chat messages
- tool-call conversion
- tool-output conversion
- state save/load
- state continuation merge
- non-stream chat response to Responses response
- stream chat chunks to Responses SSE event sequence

### Integration-style tests with fake upstream

- `MiniMax`-style chat response, non-stream
- `MiniMax`-style chat stream with tool calls
- `Zhipu V4`-style OpenAI-compatible response, non-stream
- local compact request followed by another `/responses`

### Regression checks

- existing native `/v1/responses` still works
- `/v1/chat/completions` unchanged

## 8. Acceptance Criteria

Do not mark complete until all are true:

- Codex CLI can send a short `/responses` session through this gateway
- a follow-up turn using `previous_response_id` works
- a stream response with text deltas works
- a tool-call loop works
- `/responses/compact` succeeds on the fallback path
- post-compact continuation works
- `MiniMax-M2.7` path verified
- `GLM-5.1` through `Zhipu V4` path verified

## 9. Recommended Manual Validation Sequence

1. Use a fake upstream server for deterministic protocol tests.
2. Validate `MiniMax-M2.7` first.
3. Validate `GLM-5.1` via `Zhipu V4` second.
4. Only after both pass, switch Codex CLI to this gateway for live repo work.

## 10. Example Codex CLI Provider Config

This snippet is intentionally marked as a working example to re-check against the current Codex config reference before final use.

```toml
model = "MiniMax-M2.7"
model_provider = "newapi"

[model_providers.newapi]
name = "newapi"
base_url = "https://YOUR_GATEWAY_HOST/v1"
wire_api = "responses"
api_key_env = "NEWAPI_API_KEY"
supports_websockets = false
```

Alternative model:

```toml
model = "glm-5.1"
model_provider = "newapi"
```

If the current Codex CLI config schema differs, update only the provider stanza shape, not the gateway-side design.

## 11. What Not to Do

- do not route Codex CLI through `/v1/chat/completions`
- do not fake `/responses/compact` as a no-op and call it done
- do not broaden support to every adaptor before `MiniMax` and `Zhipu V4` are stable
- do not add a new database table in phase 1
- do not change unrelated billing or routing behavior

## 12. Final Output Expectations

When implementation is done, provide:

- changed file summary
- validated model/channel matrix
- any known residual limitations
- whether Redis is required in production for stable long sessions

## 13. References

- `docs/superpowers/specs/2026-05-29-codex-cli-responses-compat-design.md`
- `https://help.openai.com/en/articles/11096431`
- `https://help.openai.com/en/articles/11381614-api-codex-cli-and-sign-in-with-chatgpt`
- `https://developers.openai.com/codex/config-reference`
- `https://platform.openai.com/docs/api-reference/responses/compact/`
