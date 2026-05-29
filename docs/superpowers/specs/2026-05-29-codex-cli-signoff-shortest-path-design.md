# Codex CLI Signoff Shortest-Path Design

Date: 2026-05-29
Status: approved for local handoff drafting
Owner: Codex
Target implementation model: `gpt-5.3-codex`
Delivery mode: local-only docs, no commit required

## 1. Goal

Reach a defensible "Codex CLI can be signed off" state for this gateway with the smallest safe implementation delta.

This signoff is scoped to:

- downstream client: Codex CLI only
- transport: HTTP Responses API
- required endpoints:
  - `POST /v1/responses`
  - `POST /v1/responses/compact`
- upstream targets:
  - `MiniMax-M2.7`
  - `GLM-5.1` via `Zhipu V4 / OpenAI-compatible`
- verification standard:
  - fake-upstream automated validation is required
  - real provider validation is documented as manual follow-up, not an implementation blocker

## 2. External Constraints

The design must match current OpenAI Codex / Responses semantics:

- Codex custom providers use `model_providers.<id>.base_url`, `env_key`, `supports_websockets`, and `wire_api`; `wire_api = "responses"` is the only supported value for custom providers according to the current Codex configuration reference.
- Responses `instructions` are request-scoped. When used with `previous_response_id`, prior-turn `instructions` are not carried forward automatically.
- `/v1/responses/compact` is part of the official long-running conversation state model and is explicitly intended to shrink context for long sessions.

Primary sources checked on 2026-05-29:

- OpenAI Codex configuration reference: `https://developers.openai.com/codex/config-reference`
- OpenAI Responses API reference: `https://platform.openai.com/docs/api-reference/responses/compact`
- OpenAI conversation state guide: `https://platform.openai.com/docs/guides/conversation-state`

## 3. Current Repo State

The repo already contains the core building blocks for a Responses compatibility layer:

- routing:
  - `relay/responses_handler.go`
- DTOs:
  - `dto/openai_request.go`
  - `dto/openai_response.go`
  - `dto/openai_compaction.go`
- conversions:
  - `service/openaicompat/chat_to_responses.go`
  - `service/openaicompat/responses_to_chat.go`
  - `service/openaicompat/responses_to_chat_request.go`
  - `service/openaicompat/chat_response_to_responses.go`
- fallback execution:
  - `relay/responses_via_chat.go`
  - `relay/responses_via_chat_stream.go`
  - `relay/responses_compact_local.go`
- state:
  - `service/responses_state.go`
- capability split:
  - `relay/channel_capability.go`
- Codex CLI affinity/header passthrough:
  - `setting/operation_setting/channel_affinity_setting.go`

The Phase-1 providers still rely on chat fallback rather than native Responses:

- `relay/channel/minimax/adaptor.go`
- `relay/channel/zhipu_4v/adaptor.go`

## 4. Blocking Issues Before Signoff

This repo is not yet at a signoff-quality Codex CLI state because of four concrete gaps.

### 4.1 Compaction boundary is unsafe

Current local compaction splits `parent.Messages` at a fixed message-count boundary. That can cut through:

- `assistant` tool-call messages
- adjacent `tool` result messages
- multi-message logical turns

This violates the earlier design requirement that tool adjacency must be preserved during compaction.

### 4.2 Instructions semantics are still too chat-history-like

Current continuation merge can preserve an old instructions-derived system message and append a new one when the text differs. That does not match official Responses semantics, where `instructions` from the previous response are not automatically carried over when `previous_response_id` is used.

### 4.3 Provider verification is not strong enough

Current tests cover converters and synthetic stream behavior, but not provider-shaped end-to-end fallback execution for:

- MiniMax non-stream
- MiniMax stream with tool calls
- Zhipu V4 non-stream continuation
- compact followed by another `/responses`

### 4.4 Signoff boundary is not written down

The repo currently has a broad compatibility design, but not a narrow "Codex CLI shortest path signoff" design and handoff that explicitly says:

- what is required for signoff
- what is intentionally excluded
- which failing repo tests are pre-existing and out of scope

## 5. Scope

### 5.1 In scope

- harden the existing Responses-via-Chat path for Codex CLI signoff
- fix compaction boundary logic
- fix `instructions` continuation semantics
- add fake-upstream automated integration tests for MiniMax and Zhipu V4
- document manual real-provider validation steps

### 5.2 Out of scope

- Claude CLI
- OpenCode / opencode
- Codex desktop app
- WebSocket transport
- broad adaptor generalization beyond MiniMax and Zhipu V4
- rewriting the state system around database persistence
- cleaning unrelated existing failing tests outside the Codex CLI path

## 6. Recommended Approach

Use the existing fallback architecture and harden only the pieces that block signoff.

This is the smallest design that:

- preserves current routing and billing architecture
- avoids provider-specific native Responses work
- matches OpenAI Responses semantics closely enough for Codex CLI
- can be proven with deterministic local tests

## 7. Architecture Changes

### 7.1 Add a compaction planner layer

Introduce a pure planning layer, preferably in `service/`, for example:

- `service/responses_compaction_planner.go`
- optional tests: `service/responses_compaction_planner_test.go`

The planner must accept the normalized stored transcript and decide:

- which older segment is eligible for summarization
- which recent segment must remain verbatim
- whether compaction is actually needed

The planner must operate on logical conversation units, not raw message count.

Recommended unit model:

- user input message
- assistant text reply
- assistant tool-call message plus immediately following `tool` result messages as one indivisible block

Boundary rules:

- default target window: retain the most recent 8 logical turns
- if the split point lands between a tool-call message and its corresponding tool result, move the split point left until adjacency is preserved
- if no older segment remains after adjacency correction, skip summarization

### 7.2 Make `instructions` state explicit

The stored snapshot already has an `Instructions` field. Signoff work must make that field authoritative.

Required behavior:

- each new fallback chat request should render exactly one active instructions message when `Instructions` is non-empty
- old instructions-derived system messages must not remain duplicated in the merged transcript
- a new request with explicit `instructions` replaces the snapshot's prior instructions
- a continuation request with no `instructions` inherits the snapshot's stored instructions

Implementation implication:

- continuation merge must stop treating instructions as just another message-history artifact
- instructions should be projected into chat messages late, from the snapshot field, not reconstructed from arbitrary historical system messages

### 7.3 Strengthen fake-upstream integration coverage

Add deterministic integration-style tests that:

- start an `httptest.Server`
- return provider-shaped chat payloads or chat SSE chunks
- run through the actual fallback path
- assert state, IDs, response bodies, and continuation behavior

The tests should validate the gateway's internal contract, not the real provider network.

### 7.4 Keep provider capability allowlist narrow

Do not expand beyond:

- `MiniMax`
- `Zhipu V4`

Native Responses support remains unchanged. The signoff path is explicitly:

- native Responses where already supported
- fallback only for MiniMax and Zhipu V4

## 8. File-Level Design

### 8.1 Existing files to modify

- `relay/responses_via_chat.go`
- `relay/responses_compact_local.go`
- `service/responses_state.go`
- `service/openaicompat/responses_to_chat_request.go`
- `relay/responses_via_chat_stream.go`

### 8.2 New files to add

- `service/responses_compaction_planner.go`
- `service/responses_compaction_planner_test.go`
- `relay/responses_via_chat_integration_test.go`

### 8.3 Files expected to remain unchanged

- `relay/channel/minimax/adaptor.go`
- `relay/channel/zhipu_4v/adaptor.go`

Rationale:

- signoff path does not require native Responses adaptor work
- keeping those adaptors unchanged makes the review boundary smaller and lower-risk

## 9. Testing Plan

### 9.1 New unit tests

- compaction planner preserves tool adjacency
- compaction planner retains recent logical turns, not raw messages
- continuation instructions replace rather than duplicate prior instructions
- compact no-op successor path when history is too short

### 9.2 New fake-upstream integration tests

- MiniMax-style chat non-stream:
  - `/v1/responses`
  - response conversion
  - snapshot persistence
- MiniMax-style chat stream with tool calls:
  - `response.created`
  - `response.output_item.added`
  - `response.function_call_arguments.delta`
  - `response.function_call_arguments.done`
  - `response.output_item.done`
  - `response.completed`
- Zhipu V4 OpenAI-compatible non-stream continuation:
  - first turn
  - second turn using `previous_response_id`
- compact then continue:
  - create long transcript
  - run `/v1/responses/compact`
  - continue from compact successor
  - verify adjacency and continuity

### 9.3 Regression tests to run

Targeted only. Do not expand to unrelated red packages as part of this signoff task.

Required:

- `go test ./relay ./service/openaicompat ./service ./controller`

Optional but recommended:

- run any new package-specific tests added for the compaction planner

Not required for this scope:

- fixing unrelated existing failures in `relay/channel/claude`
- fixing unrelated existing failures in `relay/helper`

## 10. Acceptance Criteria

Codex CLI signoff may be claimed only when all of the following are true:

1. `MiniMax-M2.7` fake-upstream non-stream path passes.
2. `MiniMax-M2.7` fake-upstream stream tool-call path passes.
3. `GLM-5.1` through `Zhipu V4` fake-upstream continuation path passes.
4. `/v1/responses/compact` fake-upstream path passes.
5. post-compact continuation passes.
6. compaction boundary never splits tool-call / tool-result adjacency.
7. `instructions` replacement semantics are covered by automated tests.
8. response IDs on fallback remain `resp_*`.
9. the local handoff includes manual real-provider validation steps and production notes on Redis.

## 11. Manual Validation Steps

Real provider validation is a follow-up task, not a blocker for this design's signoff.

Document these steps for operators:

1. Configure Codex CLI with a custom provider:
   - `base_url` pointing at `https://YOUR_GATEWAY/v1`
   - `wire_api = "responses"`
   - `supports_websockets = false`
   - `env_key = "NEWAPI_API_KEY"` or equivalent provider API key env field
2. Validate MiniMax:
   - short turn
   - follow-up turn using the returned `resp_*` chain
   - one tool-call loop
3. Validate Zhipu V4:
   - short turn
   - follow-up turn using the returned `resp_*` chain
4. Force a long session and manually trigger compaction.
5. Restart warning:
   - if Redis is disabled, restart loses continuation state

## 12. Production Note

Redis is not required for the automated signoff target, because `HybridCache` falls back to in-memory storage. However, Redis should be treated as effectively required for production Codex CLI long sessions, because in-memory mode loses all continuation state on process restart.

## 13. Decision Summary

The correct shortest path is not a new architecture. It is a focused hardening pass over the existing fallback implementation:

- fix compaction planning
- fix instructions semantics
- prove MiniMax and Zhipu fallback with fake-upstream integration tests
- document operator validation clearly

That is the smallest change set that can justify a credible Codex CLI signoff.
