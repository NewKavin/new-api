# Responses via Chat 配置指南

本文档说明如何为仅支持 Chat 上游的渠道配置和测试 `/v1/responses` 兼容能力，例如 MiniMax、Zhipu 等渠道类型。

## 适用范围

网关当前涉及两个方向的兼容逻辑：

- Responses via Chat：客户端请求 `/v1/responses`，网关对支持的 Chat-only 渠道回退到上游 `/v1/chat/completions`。
- ChatCompletions via Responses：客户端请求 `/v1/chat/completions`，网关在策略启用时改走上游 `/v1/responses`。

本文重点说明 Responses via Chat。ChatCompletions via Responses 只作为相关的可选配置说明。

## 页面配置

### 1. 创建或编辑渠道

打开管理后台：

1. 进入 `渠道` 页面。
2. 点击 `添加渠道`，或编辑已有渠道。
3. 选择支持的渠道类型：
   - `MiniMax`
   - `Zhipu V4`
4. 填写：
   - 渠道名称
   - API Key
   - Base URL，如果使用自定义上游地址
   - 上游账号实际支持的模型列表
   - 分组、优先级、权重等调度配置
5. 保存渠道。

这些渠道类型的 `/v1/responses` 到 Chat 回退由后端渠道能力自动选择，不应依赖写死的模型名。目前页面上还没有单独的 Responses via Chat 开关。

### 2. 模型映射

如果客户端使用的模型名和上游模型名不同：

1. 打开渠道编辑抽屉。
2. 在渠道设置区域配置模型映射。
3. 按需填写客户端模型名到上游模型名的映射：

```json
{
  "<客户端模型名>": "<上游模型名>"
}
```

请使用上游提供商或实际部署支持的准确模型名。不要假设模型名长期稳定。

对于 `/v1/responses/compact`，compact 模型后缀相关逻辑沿用现有模型映射规则。

### 3. 请求字段控制

在渠道编辑抽屉中检查高级透传配置。

Responses via Chat 的推荐默认值：

- 除非明确需要原始请求体透传，否则保持请求体透传关闭。
- 除非希望移除 OpenAI 兼容请求里的 `store` 字段，否则保持 `disable_store` 关闭。
- 除非明确要向上游传递客户端身份字段，否则保持 `allow_safety_identifier` 关闭。
- 除非上游支持且你接受可能的计费影响，否则保持 `allow_service_tier` 关闭。
- 除非明确需要 `stream_options.include_obfuscation`，否则保持 `allow_include_obfuscation` 关闭。

重要的流式行为：

- 非流式 Responses 请求不应向 Chat 上游发送 `stream_options`。
- 流式 Responses 请求可以保留 `stream_options.include_usage`。
- 当前构建会把缺省的 `stream` 明确转换成 `stream:false`，并在 `stream` 为 false 时丢弃 `stream_options`。

### 4. 参数覆盖

仅在渠道需要自定义请求体或请求头改写时使用 `Param Override`。

对于本兼容路径，不要通过 Param Override 给非流式请求重新加回 `stream_options`。Param Override 在正常转换之后执行，可以有意覆盖安全默认行为。

适合使用 Param Override 的场景：

- 重命名上游自定义字段。
- 删除某个厂商不支持的字段。
- 增加厂商专用请求头。
- 为渠道亲和或上游路由透传选定的 CLI 请求头。

### 5. 可选：ChatCompletions 到 Responses 策略

该配置用于相反方向：`/v1/chat/completions` -> 上游 `/v1/responses`。

打开：

1. `系统设置`
2. `模型`
3. `ChatCompletions -> Responses Compatibility`

按指定渠道启用的示例：

```json
{
  "enabled_channel_ids": [123],
  "enabled_channel_types": [],
  "enabled_models": ["<客户端模型名>"]
}
```

如果只需要 MiniMax 或 Zhipu 的 `/v1/responses` 回退到 Chat，保持该配置为 `{}` 即可。

## 端到端测试

### 1. 加载测试镜像

```bash
docker load -i /home/kavin/docker/new_api/calciumion-new-api-codex-cli-project-20260601.tar
```

### 2. 启动网关

使用你的常规环境变量和数据目录挂载。最小本地示例：

```bash
docker run --rm -p 3001:3000 \
  -v "$PWD/data:/data" \
  calciumion/new-api:codex-cli-project-20260601
```

### 3. 在页面配置渠道

1. 使用管理员账号登录。
2. 创建 MiniMax 或 Zhipu V4 渠道。
3. 填写上游 API Key。
4. 添加上游账号实际支持的模型名。
5. 保存并启用渠道。
6. 确认目标用户分组可以使用该模型。

### 4. 测试非流式 Responses

发送一个包含 `stream_options` 的非流式请求。网关应返回正常 Responses 响应，并且发给 Chat 上游的请求中不应包含 `stream_options`。

```bash
curl http://localhost:3001/v1/responses \
  -H "Authorization: Bearer $NEW_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<客户端模型名>",
    "input": "Say hello in one short sentence.",
    "stream_options": {
      "include_usage": true
    }
  }'
```

预期结果：

- HTTP 200。
- 响应对象的 `id` 以 `resp_` 开头。
- 没有上游关于 `stream_options` 的参数校验错误。

### 5. 测试流式 Responses

```bash
curl -N http://localhost:3001/v1/responses \
  -H "Authorization: Bearer $NEW_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<客户端模型名>",
    "input": "Count to three.",
    "stream": true,
    "stream_options": {
      "include_usage": true
    }
  }'
```

预期结果：

- 返回 Server-Sent Events。
- 事件名使用 Responses 事件格式。
- 流式响应正常结束，没有上游 `stream_options` 校验错误。

### 6. 测试续写

使用上一次响应里的 `id`：

```bash
curl http://localhost:3001/v1/responses \
  -H "Authorization: Bearer $NEW_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<客户端模型名>",
    "previous_response_id": "resp_xxx",
    "input": "Continue from the previous answer."
  }'
```

预期结果：

- 网关会从已保存的 Responses 状态重建 Chat 上下文。
- 新的 instructions 会替换旧 instructions，不会不断累积过期 system 消息。

### 7. 测试压缩

长对话后调用：

```bash
curl http://localhost:3001/v1/responses/compact \
  -H "Authorization: Bearer $NEW_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<客户端模型名>",
    "previous_response_id": "resp_xxx"
  }'
```

预期结果：

- 返回 compaction 响应。
- 返回的响应 id 可以作为后续 `/v1/responses` 请求的 `previous_response_id`。

## 本构建已执行的验证

构建镜像前已通过以下自动化检查：

```bash
rtk go test ./service/openaicompat -count=1
rtk go test ./relay -run TestResponsesViaChatIntegration_MiniMaxNonStreamInstructionsReplacement -count=1
rtk go test ./relay ./service/openaicompat -count=1
rtk go test ./... -count=1
```

完整测试结果：

```text
468 passed in 86 packages
```

## 当前页面覆盖情况

页面已支持配置：

- 渠道类型
- API Key
- Base URL
- 模型列表
- 模型映射
- 请求体透传
- Param Override
- `allow_service_tier`
- `disable_store`
- `allow_safety_identifier`
- `allow_include_obfuscation`
- `allow_inference_geo`
- `allow_speed`
- 全局 ChatCompletions -> Responses 策略

当前还没有独立页面开关：

- 按渠道启用或禁用 Responses via Chat。

当前行为是基于渠道能力自动选择：MiniMax 和 Zhipu V4 在不支持原生 Responses 时自动使用 Responses via Chat。该决策应继续基于渠道能力或渠道配置，不应基于模型名。如果需要页面级开关，建议新增类似 `responses_via_chat_enabled` 的渠道配置，并让 relay 的能力判断逻辑读取该配置。
