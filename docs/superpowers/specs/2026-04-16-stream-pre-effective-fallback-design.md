# 流式请求首个有效片段前 Fallback 设计稿

日期：2026-04-16

状态：待评审

关联文档：

- `docs/superpowers/specs/2026-04-15-logs-adaptive-routing-request-grouping-design.md`
- `docs/superpowers/specs/2026-04-16-stream-pre-effective-fallback-review.md`

说明：

- 本文档解决“智能路由流式请求已经接住连接，但在真正输出任何有效内容前超时或报错时，不会继续切到后续兜底线路”的问题。
- 本文档只设计“首个有效流片段前允许 fallback”的能力，不设计“已经开始输出后的中途续切”。
- 本文档已经吸收两路子代理复核意见，重点收紧有效片段定义、日志与健康度保真、trace 生命周期与客户端可见行为。

## 1. 结论

本次采用最小改动方案：

- 不改各家 provider 的流式实现
- 不改现有 grouped routing / fallback 主循环
- 不改日志表结构与统计口径
- 只扩展核心层现有的“首段流检查”逻辑

具体做法：

- 保留 [`core/bifrost.go`](/Users/fight/Desktop/2026/bifrost/core/bifrost.go) 中 `executeRequestWithRetries()` 作为流式同步错误与 fallback 的总入口
- 把 [`core/providers/utils/stream.go`](/Users/fight/Desktop/2026/bifrost/core/providers/utils/stream.go) 中现有的 `CheckFirstStreamChunkForError()` 扩展成“首个有效片段前观察器”
- 在真正出现首个有效流片段之前，只缓存少量前导事件，不立即对客户端输出
- 如果在首个有效流片段之前收到终止错误，则把这次 attempt 重新当成同步失败，让现有 fallback 机制继续切下一条线路
- 一旦已经出现首个有效流片段，就立即把缓存内容与后续流一起正常输出，之后保持现有行为，不再中途切换

首版范围固定为：

- `TextCompletionStreamRequest`
- `ChatCompletionStreamRequest`
- `ResponsesStreamRequest`

首版明确不做：

- `PassthroughStreamRequest`
- `WebSocketResponsesRequest`
- `RealtimeRequest`
- `SpeechStreamRequest`
- `TranscriptionStreamRequest`
- `ImageGenerationStreamRequest`
- `ImageEditStreamRequest`
- 已经开始输出后的中途断流续切

## 2. 背景

当前已经具备完整的 fallback 链路与 attempt 级日志能力：

- 路由阶段会生成主线路与后续 fallback 计划
- fallback attempt 会生成新的 request ID，并通过 `parent_request_id` 归组
- grouped routing 会在 fallback 时刷新 `fallback_index`、`route_layer_index` 与 pinned key
- 日志页已经能把多次 attempt 串成一组

但当前流式请求还有一个边界问题：

- 只要 provider 返回了一个看起来不是错误的首个 chunk，核心层就认为“这次流已经开始”
- 后面如果在真正输出正文前才超时、断流或上游在 SSE 中返回错误事件，这个错误只会作为当前流的错误片段抛出
- 现有 fallback 主循环收不到同步错误，因此不会继续切下一条线路

这直接导致一种用户非常难接受的失败：

- 明明配置了多组兜底
- 也明明还没有吐出任何有效内容
- 但请求仍然直接失败

实际线上样本已经证明这一点：

- 请求 `a0123898-938d-47e8-ab40-3f8c6d6384b4`
- 命中 `gemini-auto`
- 实际先走 `云雾API / gemini-3.1-pro-preview`
- `output_message`、`raw_response`、输出 token 全为空
- 错误是读取流时 `i/o timeout`
- 同组没有后续 fallback attempt

说明这次失败发生在“真正开始输出前”，理论上是适合继续切下一条线路的。

## 3. 设计目标

- 让流式请求在“首个有效片段前失败”时可以继续走现有 fallback
- 保证客户端只看到最终选中的那条流，不先看到前一个失败 attempt 的占位事件或错误事件
- 保留失败 attempt 的日志、健康度失败、route layer 与 fallback 链信息
- 不改变现有请求级与 attempt 级统计口径
- 不要求每个 provider 单独改造
- 用最小改动覆盖这次线上真实遇到的问题

## 4. 本次不做

- 不做“已经开始输出后”的中途续切
- 不在客户端拼接不同 provider 的正文输出
- 不为 API 新增聚合错误结构
- 不修改 logstore schema
- 不修改日志页面展示结构
- 不把成功定义从“最终 attempt 成功”改成别的口径
- 不改 passthrough / websocket / realtime 的流式语义
- 不在首版承诺所有结构化流都支持这套能力

## 5. 方案比较

| 方案 | 做法 | 优点 | 缺点 | 结论 |
| --- | --- | --- | --- | --- |
| A | 在核心层扩展现有首段流检查，把“首个 chunk 错误”升级成“首个有效片段前错误” | 改动最小，能复用现有 retry / fallback / logging / health 逻辑 | 需要精确定义有效片段与 attempt 收尾边界 | 选这个 |
| B | 在 `handleStreamRequest()` 上层新增完整流观察包装器 | 语义上可行 | 和现有 `executeRequestWithRetries()` 的同步错误入口重复，容易形成两套判断 | 不选 |
| C | 每个 provider 分别改 streaming goroutine，支持更细粒度续切 | 理论上最强 | 改动面太大，容易把行为做散 | 不选 |
| D | 支持已经开始输出后的中途续切 | 表面成功率最高 | 极易把不同 provider 的输出拼在一起，风险过高 | 不做 |

选择方案 A 的原因：

- [`core/bifrost.go`](/Users/fight/Desktop/2026/bifrost/core/bifrost.go) 中 `executeRequestWithRetries()` 已经有一层“流式返回后马上做同步判断”的入口
- [`core/providers/utils/stream.go`](/Users/fight/Desktop/2026/bifrost/core/providers/utils/stream.go) 已经有现成的 `CheckFirstStreamChunkForError()` 与 `drainDone` 机制
- 只要把判断范围从“第一个 chunk”扩展到“首个有效片段之前”，就能复用现有 fallback 主链

## 6. 当前根因

当前根因不在路由配置，而在核心流式判断过早认定“已经开始输出”。

现状是：

1. `requestWorker` 调 provider，拿到 `chan *BifrostStreamChunk`
2. `executeRequestWithRetries()` 调 `CheckFirstStreamChunkForError()`
3. 这个 helper 只检查“第一个 chunk 是否是 error”
4. 只要第一个 chunk 不是显式错误，就把整条流视为成功开始
5. 之后即使在真正正文输出前才读流失败，错误也只会变成流内错误，而不会回到 fallback 主循环

所以真正的问题不是：

- fallback 没生成
- health rule 没生效
- 日志没记下来

而是：

- “第一个 chunk”
- 和“首个有效流片段”

在当前代码里被错误地等同了。

## 7. 首版范围

首版只覆盖三类结构化流：

- `TextCompletionStreamRequest`
- `ChatCompletionStreamRequest`
- `ResponsesStreamRequest`

原因：

- 这三类流都走统一的 `BifrostStreamChunk` 结构
- 这三类流最直接关联当前智能路由场景
- 这三类流已经有较稳定的日志、trace、UI 语义

首版显式排除：

- `PassthroughStreamRequest`
  原始字节流没有稳定的结构化首段语义，不能在这一版安全判断“有效片段”
- `WebSocketResponsesRequest` / `RealtimeRequest`
  生命周期与 HTTP SSE 不完全一致，当前不与这版一起处理
- `Speech` / `Transcription` / `ImageGeneration` / `ImageEdit`
  首段 payload 语义与客户端期望不同，这版先不扩展

## 8. 详细设计

### 8.1 新的核心观察器

把 [`core/providers/utils/stream.go`](/Users/fight/Desktop/2026/bifrost/core/providers/utils/stream.go) 中现有 helper 扩展为“首个有效片段前观察器”。

建议形态：

- 继续保留“输入源 stream，输出 wrapped stream / drainDone / error”的模式
- 调用点继续放在 [`core/bifrost.go`](/Users/fight/Desktop/2026/bifrost/core/bifrost.go) 的 `executeRequestWithRetries()` 中
- 观察器在同步阶段做完以下判断后再返回：
  - 如果首个有效片段前遇到错误，返回同步错误
  - 如果已经遇到首个有效片段，返回 wrapped stream
  - 如果流在首个有效片段前干净结束且没有错误，保留现有“非错误结束”的语义，不新增 fallback

推荐命名：

- 可以直接扩展现有 `CheckFirstStreamChunkForError()`
- 也可以改名为更准确的 `CheckStreamUntilEffectiveChunkOrError()`

这次设计不强制要求命名变化，但语义必须升级。

### 8.2 首个有效片段定义

这是整个方案最关键的边界。

#### 8.2.1 Text / Chat stream

以下内容视为“有效片段”：

- `content` 有非空增量
- `refusal` 有非空增量
- `reasoning` 有非空增量
- `tool_calls` 出现实际 delta
- `audio` 出现实际业务负载

以下内容不视为“有效片段”：

- 只有 `role`
- 空 delta
- 只有 `finish_reason`
- 只有空 usage 或其他元信息

解释：

- `role-only` 与空 delta 是典型前导片段
- 这类片段如果先发给客户端，再 fallback，就会造成“前一条线路已经对外露出痕迹”的问题
- 因此必须先缓存，不能算真正开始

#### 8.2.2 Responses stream

以下内容视为“有效片段”：

- `response.output_text.delta`
- `response.refusal.delta`
- `response.reasoning_summary_text.delta`
- `response.function_call_arguments.delta`
- `response.mcp_call_arguments.delta`
- `response.custom_tool_call_input.delta`
- `response.code_interpreter_call_code.delta`
- `response.image_generation_call.partial_image`

以下内容不视为“有效片段”：

- `response.ping`
- `response.created`
- `response.queued`
- `response.in_progress`
- `response.completed`
- `response.failed`
- `response.incomplete`
- `response.output_item.added`
- `response.output_item.done`
- `response.content_part.added`
- `response.content_part.done`
- 各类 `*.in_progress`
- 各类 `*.searching`
- 各类 `*.fetching`
- 各类 `*.done`
- 各类 `*.completed`

解释：

- Responses API 有大量生命周期与结构占位事件
- 如果仍按“第一个非错误 chunk”判断，这个功能在 Responses 场景里几乎等于没修

#### 8.2.3 干净结束但没有有效片段

如果流在首个有效片段前干净结束，且没有错误：

- 不新增 fallback
- 不把它直接改判成失败
- 保持现有成功 / 空结果语义

原因：

- 这次设计只解决“首个有效片段前出错”
- 不顺手重写“零内容但正常结束”的成功判定，避免 scope 扩大

### 8.3 前导缓存策略

观察器需要缓存少量前导片段，但不能无限缓存。

固定策略：

- 只在首个有效片段前缓存
- 缓存上限采用“双阈值”：
  - 最多 `8` 个 chunk
  - 最多约 `32 KB` 的序列化体积

达到上限后的处理：

- 立即认定当前 attempt 已经开始
- 立刻把缓存片段原样吐给客户端
- 后续保持现有行为，不再尝试首段 fallback

原因：

- 优先保证协议正确与内存稳定
- 不允许观察器演变成整流缓冲器

补充约束：

- 观察器不额外引入一套新的独立超时
- “等待首个有效片段”的时长，继续沿用现有 request context timeout、provider HTTP timeout 与 stream idle timeout
- 也就是说：
  - 如果 provider 一直没有吐出任何 chunk，观察器不会自己另起计时器
  - 等待会在现有超时机制触发后，以“首个有效片段前错误”回到现有 fallback 逻辑

原因：

- 避免出现两套 timeout 语义相互打架
- 保持这次改动只收敛在“首段判定”，不顺手重写超时体系

### 8.4 Fallback 触发规则

只有同时满足以下条件，才允许继续走下一条 fallback：

1. 当前 request type 在首版支持范围内
2. 当前 attempt 还没有出现首个有效片段
3. 当前 stream 先收到的是终止错误
4. 现有 fallback 链仍然存在后续目标
5. 错误本身没有显式 `AllowFallbacks = false`

一旦已经出现首个有效片段：

- 这次 attempt 即被认定为“已开始输出”
- 后续任何错误都保持现有行为
- 不再继续切到下一条线路

### 8.5 对客户端的可见行为

客户端必须只看到最终选中的那条流。

因此：

- 首个有效片段前缓存的前导事件，不能提前发给客户端
- 首个有效片段前的错误事件，不能先发给客户端再 fallback
- 只有当本次 attempt 被认定为“已开始输出”后，才允许把缓存内容与后续流一起输出

用户最终体验应当是：

- 要么只是“启动慢了一点”
- 要么最终明确失败

不能出现：

- 先报一次错误
- 又自己恢复成功

也不能出现：

- 先看到 attempt-1 的占位事件
- 后面正文却来自 attempt-2

### 8.6 与现有 fallback 主循环的关系

这次不新造 fallback 主循环。

保留现有流程：

- `handleStreamRequest()`
- `tryStreamRequest()`
- `executeRequestWithRetries()`
- 外层 grouped routing / fallback 迭代

新的观察器只负责把“首个有效片段前错误”重新抬回同步错误。

也就是说：

- 当前 attempt 在观察器里失败
- `executeRequestWithRetries()` 收到同步 `bifrostError`
- `handleStreamRequest()` 继续沿用现有 fallback 分支

这样能自然复用：

- `fallback_index`
- `fallback_request_id`
- `clearCtxForFallback()`
- `restoreFallbackAPIKeyIDFromContext()`
- governance grouped routing layer 刷新

补充说明：

- 如果 source stream 在首个 chunk 前就直接关闭，继续保持现有“空流但非错误”的语义
- 这类场景不因为本次改动而新增 fallback
- 如果 source stream 长时间没有任何 chunk，最终由既有 timeout 机制产出错误，再按“首个有效片段前错误”处理

## 9. 日志、健康度与 trace 约束

### 9.1 日志

首个有效片段前失败的 attempt 仍然必须完整记下来。

要求：

- 失败 attempt 继续生成独立日志
- fallback attempt 继续生成新的 request ID
- `parent_request_id` 继续归到同一请求组
- 最终成功 attempt 仍然是该组的 `final attempt`

不能做的事：

- 不能跳过第一次失败的 `PostLLMHook`
- 不能重用第一次失败的 log ID
- 不能为了让客户端“看起来更平滑”而把失败 attempt 从日志里抹掉

### 9.2 健康度与 grouped routing

首个有效片段前失败，仍然是一次真实失败。

要求：

- 第一次 attempt 继续记 `RecordRealAccess`
- 第一次 attempt 继续记 failure
- 后续 fallback success 继续单独记 success

不能做的事：

- 不能因为客户端最终成功，就抹掉第一层 target 的失败
- 不能绕过现有 `fallback_index` 刷新路径

否则会导致：

- 健康度被“后续兜底成功”稀释
- 坏线路继续被高估

### 9.3 trace

trace 是这次上线前必须单独验证的项。

核心要求：

- 第一次 attempt 在首个有效片段前失败时，不能把整次请求 trace 提前封口
- 但第一次 attempt 的 attempt-local 资源也不能悬空

因此需要明确两个层次：

1. request-level trace completion

- 仍然由最终真正交给 transport 的那条流在结束时完成
- 不能因为第一次可 fallback 错误而提前执行整个请求的 `traceCompleter`

2. attempt-level deferred resources

- 如果观察器把首段错误转成同步错误，必须等待当前 stream drain 完成
- 然后补做当前 attempt 的 deferred post-hook / accumulator 清理
- 避免第一次失败 attempt 的 finalizer、stream accumulator 或 pipeline 资源串到第二次 attempt

实现上要求：

- 保留现有 `drainDone` 等待
- 在 `executeRequestWithRetries()` 内，当观察器返回“可 fallback 的同步错误”时，补做 attempt-local finalizer 清理
- 但不在这里结束整个 HTTP request 的 trace

## 10. 状态管理要求

观察器最少需要维护这些状态：

| 状态 | 作用 |
| --- | --- |
| `seenEffectiveChunk` | 是否已经遇到首个有效片段 |
| `bufferedPreludeChunks` | 首个有效片段前缓存的前导 chunk |
| `bufferedPreludeBytes` | 前导缓存体积，用于上限控制 |
| `terminalErrBeforeEffective` | 首个有效片段前是否已收到终止错误 |
| `drainDone` | 当前 attempt 的 source stream 是否已完成 drain / 转发 |

绝对不能跨 fallback attempt 继承的状态：

- `StreamEndIndicator`
- 前一次 attempt 的前导缓存
- 前一次 attempt 的 deferred post-hook finalizer 状态
- 前一次 attempt 的 stream accumulator 完成状态
- 前一次 attempt 的显式 key pin 结果

可以保留的状态：

- 主 request ID
- 虚拟密钥上下文
- 路由规则上下文
- grouped routing 的主链关系

## 11. 测试设计

测试至少覆盖以下场景：

### 11.1 核心 helper 测试

新增或改造 [`core/providers/utils/stream_test.go`](/Users/fight/Desktop/2026/bifrost/core/providers/utils/stream_test.go)：

- `response.created -> response.in_progress -> error`：应返回同步错误，可 fallback
- `role-only -> empty delta -> error`：应返回同步错误，可 fallback
- `role-only -> empty delta -> content delta`：不应 fallback，且前导 chunk 不能丢
- `first effective chunk -> later error`：不应 fallback
- `channel closes before first chunk`：继续保持空流非错误语义，不新增 fallback
- `clean close without effective chunk`：不应误判为错误
- `no chunk until existing timeout fires`：应在首个有效片段前回到同步错误，可 fallback
- `buffer limit reached before effective chunk`：应提交当前流，不再 fallback

### 11.2 核心 fallback 测试

新增 [`core/bifrost_stream_fallback_test.go`](/Users/fight/Desktop/2026/bifrost/core/bifrost_stream_fallback_test.go)：

- 主线路在首个有效片段前报错，第二条 fallback 成功：客户端最终只看到 fallback 流
- 主线路首个有效片段后报错：不切 fallback
- 所有 fallback 都在首个有效片段前失败：最终返回整体错误
- grouped routing 关闭 provider-local retries 时，这套能力仍能正常切换 fallback

### 11.3 日志与治理回归

补充或更新：

- [`plugins/logging/operations_test.go`](/Users/fight/Desktop/2026/bifrost/plugins/logging/operations_test.go)
- [`plugins/governance/routing_test.go`](/Users/fight/Desktop/2026/bifrost/plugins/governance/routing_test.go)
- [`plugins/governance/tracker_test.go`](/Users/fight/Desktop/2026/bifrost/plugins/governance/tracker_test.go)

重点验证：

- 首段失败 attempt 仍然写日志
- fallback attempt 仍然有新的 request ID
- `parent_request_id` / `fallback_index` / `route_layer_index` 正确
- 健康度仍把第一次 target 记失败

## 12. 上线验收标准

满足以下条件才建议上线：

1. `Text / Chat / Responses` 三类结构化流全部通过新增测试
2. 首段失败 fallback 成功后，客户端看不到第一次失败的占位事件或错误事件
3. 日志页中同一请求组能看到“前失败 + 后成功”的多条 attempt
4. 请求成功率提升，但 attempt 成功率保持原口径不变
5. 第一层失败 target 的健康度仍然下降，不因后续成功被抹平
6. trace 不出现“第一次失败 attempt 提前封死整次请求”的情况

## 13. 风险总结

真正需要防的不是“改不动”，而是下面这些误做：

- 把“第一个 chunk”误当成“首个有效片段”
- 先把前一个失败 attempt 的事件发给客户端，再 fallback
- 让第一次失败 attempt 不再记日志或不再记健康失败
- 让第一次失败 attempt 的 deferred 资源污染第二次 attempt
- 在已经发出有效内容后继续切换 provider

只要这五件事都避开，这个功能就能以较小改动、较稳方式进入实现阶段。
