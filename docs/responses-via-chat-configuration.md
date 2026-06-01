# Responses via Chat Configuration Guide

This guide describes how to configure and test `/v1/responses` compatibility for chat-only upstream channels such as MiniMax and Zhipu.

## Scope

The gateway supports two related compatibility directions:

- Responses via Chat: client calls `/v1/responses`, gateway falls back to upstream `/v1/chat/completions` for supported chat-only channels.
- ChatCompletions via Responses: client calls `/v1/chat/completions`, gateway sends upstream `/v1/responses` when enabled by policy.

This document focuses on Responses via Chat. The ChatCompletions via Responses policy is included only as an optional related setting.

## Page Configuration

### 1. Create or Edit a Channel

Open the admin dashboard:

1. Go to `Channels`.
2. Click `Add Channel`, or edit an existing channel.
3. Select a supported channel type:
   - `MiniMax`
   - `Zhipu V4`
4. Fill in:
   - Channel name
   - API key
   - Base URL, if using a custom endpoint
   - Models supported by your upstream account
   - Groups and priority/weight as needed
5. Save the channel.

For these channel types, `/v1/responses` fallback to chat is selected automatically by backend channel capability. It must not depend on hard-coded model names. There is currently no separate page toggle for Responses via Chat.

### 2. Model Mapping

If the client model name is different from the upstream model name:

1. Open the channel edit drawer.
2. Configure model mapping in the channel settings area.
3. Map client model to upstream model if needed:

```json
{
  "<client-model-name>": "<upstream-model-name>"
}
```

Use the exact model names from the upstream provider or your deployment. Do not assume model names are stable over time.

For `/v1/responses/compact`, compact model suffix mapping follows the existing model mapping rules.

### 3. Request Field Controls

In the channel edit drawer, review the advanced passthrough controls.

Recommended defaults for Responses via Chat:

- Keep request body passthrough disabled unless you explicitly need raw passthrough behavior.
- Keep `disable_store` off unless you want to remove `store` from OpenAI-compatible requests.
- Keep `allow_safety_identifier` off unless you explicitly want to pass client identity fields upstream.
- Keep `allow_service_tier` off unless the upstream supports it and you accept possible billing impact.
- Keep `allow_include_obfuscation` off unless you explicitly need `stream_options.include_obfuscation`.

Important stream behavior:

- Non-streaming Responses requests should not send `stream_options` to chat upstreams.
- Streaming Responses requests may preserve `stream_options.include_usage`.
- The current build explicitly converts missing `stream` to `stream:false` and drops `stream_options` when `stream` is false.

### 4. Param Override

Use `Param Override` only when a channel requires custom body/header rewriting.

For this compatibility path, do not add `stream_options` back into non-streaming requests via Param Override. Param Override runs after normal conversion and can intentionally override the safe default.

Useful cases:

- Rename custom upstream fields.
- Remove fields unsupported by one vendor.
- Add vendor-specific headers.
- Pass through selected CLI headers for channel affinity or upstream routing.

### 5. Optional: ChatCompletions to Responses Policy

This setting is for the opposite direction: `/v1/chat/completions` -> upstream `/v1/responses`.

Open:

1. `System Settings`
2. `Models`
3. `ChatCompletions -> Responses Compatibility`

Example for specific channels:

```json
{
  "enabled_channel_ids": [123],
  "enabled_channel_types": [],
  "enabled_models": ["<client-model-name>"]
}
```

Leave this as `{}` if you only need `/v1/responses` fallback to chat for MiniMax or Zhipu.

## End-to-End Test

### 1. Load the Test Image

```bash
docker load -i /home/kavin/docker/new_api/calciumion-new-api-codex-cli-project-20260601.tar
```

### 2. Start the Gateway

Use your normal environment variables and mounted data directory. Minimal local example:

```bash
docker run --rm -p 3001:3000 \
  -v "$PWD/data:/data" \
  calciumion/new-api:codex-cli-project-20260601
```

### 3. Configure the Channel in the Page

1. Log in as admin.
2. Create a MiniMax or Zhipu V4 channel.
3. Add the upstream API key.
4. Add the model name supported by your upstream account.
5. Save and enable the channel.
6. Confirm the model is available to the target user group.

### 4. Test Non-Streaming Responses

Send a non-streaming request that includes `stream_options`. The gateway should return a normal Responses payload, and the upstream chat request should not include `stream_options`.

```bash
curl http://localhost:3001/v1/responses \
  -H "Authorization: Bearer $NEW_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<client-model-name>",
    "input": "Say hello in one short sentence.",
    "stream_options": {
      "include_usage": true
    }
  }'
```

Expected result:

- HTTP 200
- Response object has an `id` starting with `resp_`
- No upstream error about `stream_options`

### 5. Test Streaming Responses

```bash
curl -N http://localhost:3001/v1/responses \
  -H "Authorization: Bearer $NEW_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<client-model-name>",
    "input": "Count to three.",
    "stream": true,
    "stream_options": {
      "include_usage": true
    }
  }'
```

Expected result:

- Server-sent events are returned.
- Events use Responses event names.
- Final stream completes without upstream `stream_options` validation errors.

### 6. Test Continuation

Use the `id` from a previous response:

```bash
curl http://localhost:3001/v1/responses \
  -H "Authorization: Bearer $NEW_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<client-model-name>",
    "previous_response_id": "resp_xxx",
    "input": "Continue from the previous answer."
  }'
```

Expected result:

- The gateway rebuilds chat context from stored Responses state.
- New instructions replace previous instructions instead of accumulating stale system messages.

### 7. Test Compaction

After a long conversation, call:

```bash
curl http://localhost:3001/v1/responses/compact \
  -H "Authorization: Bearer $NEW_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<client-model-name>",
    "previous_response_id": "resp_xxx"
  }'
```

Expected result:

- A compaction response is returned.
- The returned response id can be used as `previous_response_id` in a later `/v1/responses` call.

## Verification Performed for This Build

The following automated checks passed before building the image:

```bash
rtk go test ./service/openaicompat -count=1
rtk go test ./relay -run TestResponsesViaChatIntegration_MiniMaxNonStreamInstructionsReplacement -count=1
rtk go test ./relay ./service/openaicompat -count=1
rtk go test ./... -count=1
```

Full test result:

```text
468 passed in 86 packages
```

## Current UI Coverage

Already configurable in the page:

- Channel type
- API key
- Base URL
- Models
- Model mapping
- Request body passthrough
- Param Override
- `allow_service_tier`
- `disable_store`
- `allow_safety_identifier`
- `allow_include_obfuscation`
- `allow_inference_geo`
- `allow_speed`
- Global ChatCompletions -> Responses policy

Not currently exposed as a dedicated page toggle:

- Enable or disable Responses via Chat per channel.

Current behavior is capability based: MiniMax and Zhipu V4 use Responses via Chat automatically when they do not support native Responses. The decision should remain channel capability/configuration based, not model-name based. If a page-level switch is required, add a channel setting such as `responses_via_chat_enabled` and have the relay capability check honor it.
