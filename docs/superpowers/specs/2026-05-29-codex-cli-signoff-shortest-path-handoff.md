# Handoff: Codex CLI Signoff Shortest Path

Date: 2026-05-29
Target implementation model: `gpt-5.3-codex`
Status: ready for local execution
Delivery mode: do not commit; keep all artifacts local

## 1. Mission

Take the current partial Codex CLI compatibility implementation and raise it to a signoff-quality state for:

- downstream: Codex CLI only
- protocol: HTTP Responses API
- endpoints:
  - `POST /v1/responses`
  - `POST /v1/responses/compact`
- upstreams:
  - `MiniMax-M2.7`
  - `GLM-5.1` via `Zhipu V4`

The implementation is complete only when fake-upstream automated validation proves:

- normal generation
- `previous_response_id` continuation
- tool-call streaming
- compaction followed by continuation

Real provider validation is documented, not required for code completion in this handoff.

## 2. Hard Constraints

- use `common.Marshal`, `common.Unmarshal`, `common.DecodeJson`
- do not add DB persistence
- do not repurpose the existing `Codex` channel type
- keep fallback allowlist limited to MiniMax and Zhipu V4
- do not widen scope to Claude CLI or OpenCode
- do not fix unrelated repo-red tests unless they directly block this work
- do not commit anything

## 3. Repo Facts You Must Preserve

- custom Codex providers use Responses only; `wire_api = "responses"` is the only supported provider protocol in the current Codex config reference
- Responses `instructions` do not automatically carry over across `previous_response_id`
- local compact exists to support long-running conversation state

Primary references:

- `https://developers.openai.com/codex/config-reference`
- `https://platform.openai.com/docs/api-reference/responses/compact`
- `https://platform.openai.com/docs/guides/conversation-state`

## 4. Current Known Issues

### 4.1 Unsafe compaction split

`relay/responses_compact_local.go` currently slices history by raw message count and can split:

- assistant tool-call message
- adjacent tool result

This is the top signoff blocker.

### 4.2 Wrong instructions continuation semantics

`relay/responses_via_chat.go` currently deduplicates only identical leading system messages. That can leave the old instructions effective while appending new instructions, which does not match Responses semantics.

### 4.3 Provider verification gap

Current tests are mostly converter / synthetic-path tests. They do not yet prove:

- MiniMax fake-upstream non-stream
- MiniMax fake-upstream stream tool-call
- Zhipu V4 fake-upstream continuation
- compact then continue

### 4.4 Existing unrelated red tests

Do not spend this task fixing these unless they directly block your target package runs:

- `relay/channel/claude`
- `relay/helper`

They are not part of Codex CLI signoff scope.

## 5. Files to Read First

- `relay/responses_handler.go`
- `relay/responses_via_chat.go`
- `relay/responses_via_chat_stream.go`
- `relay/responses_compact_local.go`
- `service/responses_state.go`
- `service/openaicompat/responses_to_chat_request.go`
- `service/openaicompat/chat_response_to_responses.go`
- `relay/channel/minimax/adaptor.go`
- `relay/channel/zhipu_4v/adaptor.go`
- `docs/superpowers/specs/2026-05-29-codex-cli-signoff-shortest-path-design.md`

## 6. Required Deliverables

### Deliverable A: compaction planner

Add a planner layer that splits transcript into:

- older segment to summarize
- retained recent segment
- no-op compaction case

Rules:

- retain recent logical turns, not recent raw messages
- preserve assistant tool-call and matching tool-result adjacency
- if boundary lands inside adjacency, move it left

### Deliverable B: instructions state fix

Make `Instructions` in `ResponsesStateSnapshot` authoritative.

Rules:

- new explicit instructions replace old instructions
- inherited instructions come from snapshot field, not residual system messages
- merged transcript must not accumulate contradictory system messages across turns

### Deliverable C: fake-upstream integration tests

Add deterministic integration-style tests with `httptest.Server` for:

- MiniMax non-stream
- MiniMax stream tool-call
- Zhipu V4 continuation
- compact then continue

### Deliverable D: local docs refresh

Update this handoff and the paired design doc if implementation forces any decisions to become more specific.

## 7. Recommended Edit Order

1. Add `service/responses_compaction_planner.go`
2. Add planner tests
3. Fix instructions merge behavior in `relay/responses_via_chat.go`
4. Refactor `relay/responses_compact_local.go` to use the planner
5. Add fake-upstream fixtures and integration tests
6. Validate MiniMax path
7. Validate Zhipu V4 path
8. Validate compact then continue
9. Refresh local handoff notes if implementation details changed

## 8. Detailed Implementation Notes

### 8.1 Planner model

Do not model compaction over naked messages only.

Recommended representation:

- `ConversationUnit`
  - one user turn
  - one assistant text turn
  - one assistant tool-call message plus adjacent tool result messages

Recommended planner API:

```go
type CompactionPlan struct {
    OlderMessages    []dto.Message
    RetainedMessages []dto.Message
    NeedsSummary     bool
}

func BuildResponsesCompactionPlan(messages []dto.Message, retainTurns int) (*CompactionPlan, error)
```

If another shape fits the codebase better, keep the same invariants.

### 8.2 Instructions handling

Recommended approach:

- store normalized transcript without embedding prior instructions as permanent history
- during continuation:
  - determine active `Instructions`
  - render a single instructions message at request-build time
  - merge only non-instructions transcript history

If you keep instructions inside `Messages`, explicitly scrub the old rendered instructions message before appending the new one.

### 8.3 Fake-upstream strategy

Use `httptest.Server` and provider-specific response bodies.

MiniMax non-stream fixture:

- return chat-completions JSON
- include assistant text
- include usage

MiniMax stream fixture:

- return SSE chat chunks
- include tool call `id`, `name`, argument deltas
- include final usage chunk
- terminate with `[DONE]`

Zhipu V4 fixture:

- return OpenAI-compatible chat-completions JSON
- second request should be asserted to contain the merged continuation transcript

### 8.4 Assertions that matter

Every integration test should assert:

- fallback response ID starts with `resp_`
- saved snapshot exists in `ResponsesStateStore`
- persisted transcript is usable for next turn
- tool-call sequence remains intact
- compact successor snapshot can be continued from

## 9. Tests to Add

- `service/responses_compaction_planner_test.go`
- `relay/responses_via_chat_integration_test.go`

You may add more files if it keeps coverage easier to read, but do not fragment the suite unnecessarily.

## 10. Commands to Run

Use targeted test commands.

Required:

```bash
go test ./relay ./service/openaicompat ./service ./controller -run 'TestResponses|TestSupports|TestChatCompletionsResponseToResponsesResponse'
```

If you add a dedicated planner test name, also run:

```bash
go test ./service -run 'TestResponsesCompactionPlanner'
```

Do not claim completion based only on package compile success.

## 11. Completion Checklist

- compaction planner added
- tool adjacency preserved by automated tests
- instructions replacement semantics covered by automated tests
- MiniMax fake-upstream non-stream test passes
- MiniMax fake-upstream stream tool-call test passes
- Zhipu V4 fake-upstream continuation test passes
- compact then continue test passes
- no new scope added for Claude CLI or OpenCode
- local docs still match implementation reality

## 12. Manual Validation Steps To Keep In Docs

Document but do not automate:

1. Codex CLI provider config using:
   - `base_url`
   - `env_key`
   - `supports_websockets = false`
   - `wire_api = "responses"`
2. MiniMax real test:
   - short coding turn
   - follow-up via `previous_response_id`
   - one tool-call loop
3. Zhipu V4 real test:
   - short coding turn
   - follow-up via `previous_response_id`
4. one long session requiring compaction
5. restart behavior note when Redis is disabled

## 13. What Not To Do

- do not rewrite the whole state system
- do not implement native Responses adaptors for MiniMax or Zhipu V4 in this task
- do not generalize fallback beyond MiniMax and Zhipu V4
- do not claim signoff without fake-upstream compact-and-continue proof
- do not fix unrelated `claude` or `stream_scanner` repo failures as part of scope unless they directly block your tests
- do not commit changes

## 14. Final Output Expectations

When implementation is done, report:

- whether Codex CLI signoff criteria are met
- exact test commands run
- fake-upstream matrix results
- any residual manual-only validation items
- whether Redis is merely optional or operationally recommended

Completion is not "code compiles". Completion is "fake-upstream signoff matrix passes."
