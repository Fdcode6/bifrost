# 流式请求“首个有效片段前允许 fallback”第二路设计审查

日期：2026-04-16  
范围：`core/bifrost.go`、`core/providers/utils`、`plugins/logging`、`framework/logstore`、`plugins/governance`

## 1. 审查结论

这个能力可以做，而且值得做。对这次线上遇到的“流已经接住，但首个真正内容出来前超时”的失败场景，它能明显提升成功率。

前提是边界必须收紧到下面这条：

> 只有在**首个有效流片段**送给客户端之前，如果当前 attempt 以错误结束，才允许走现有 fallback 链。

这里的“有效流片段”不能简单等同于“第一个收到的 chunk”。如果只是检查第一个 chunk，会把一些仅用于协议占位或状态通知的 chunk 误判为“已经开始输出”，导致能力失效。

## 2. 推荐的最小实现边界

推荐在核心流包装层实现，而不是逐个 provider 改写。

推荐边界：

1. 主请求和每个 fallback attempt 仍然沿用现有 `handleStreamRequest -> tryStreamRequest -> provider stream` 流程。
2. 在 `handleStreamRequest` 拿到 provider 返回的 `chan *BifrostStreamChunk` 之后，增加一个“小型首段观察器”。
3. 观察器只做两件事：
   - 在遇到**首个有效流片段**前，暂存或忽略非有效 chunk。
   - 如果在首个有效流片段前收到错误 chunk，则把这次 attempt 视为“尚未真正开始输出”，回到现有 fallback 循环。
4. 一旦首个有效流片段成立，立刻把之前可对外显示的片段按顺序吐给客户端，后续完全走现有流转发逻辑，不再中途 fallback。

这样做的优点：

- 复用现有 fallback、日志、健康度、route layer 逻辑。
- 不需要逐个 provider 改 streaming goroutine。
- 能覆盖这次线上遇到的 `i/o timeout` 且 `0 output token` 场景。

## 3. 首个有效片段的定义

这是整个方案最关键的设计点。

推荐定义：

### 3.1 视为“有效片段”

- Chat/Text stream：首次出现用户可见文本 delta、tool call delta、audio delta、image delta 等实际输出。
- Responses stream：首次出现真正的 output item / content delta / tool call delta。
- Speech / image / transcription 这类流：首次出现真实业务负载。

### 3.2 不视为“有效片段”

- `responses.created`
- `responses.in_progress`
- 空内容占位 chunk
- 只有元数据、没有任何实际输出的 chunk
- provider 在 HTTP 200 SSE 内回传的错误事件

如果这里偷懒做成“第一个 chunk 不是 error 就算开始”，对于 Responses API 基本等于没修，因为这类流很可能先发 `created/in_progress`，后面才真正出内容或报错。

## 4. 分模块影响与审查意见

## 4.1 `plugins/logging`

### 结论

日志层整体是兼容的，但前提是**必须继续让失败 attempt 走完整个 PostLLMHook**。

### 已有行为

- `PreLLMHook` 为每个 attempt 建立 pending log。
- fallback attempt 已有独立 `fallback_request_id`，并通过 `parent_request_id` 归组。
- `PostLLMHook` 在流式 error 结束时会写出一条失败日志。
- grouped routing 的 `route_layer_index` 会在最终 entry 上按当前上下文刷新。

### 影响

- 如果首个有效片段前 fallback 生效，日志会出现：
  - 第一条：失败 attempt
  - 第二条或更多：后续 fallback attempt
  - 最后一条：真正成功的 final attempt
- 这对当前 request grouping 和 final success 统计是成立的，甚至比现在更符合真实链路。

### 必须规避

1. 不能为了 fallback 而跳过第一次失败 attempt 的 `PostLLMHook`。  
   否则会留下 `processing` 假记录，或者干脆丢失这次失败。

2. 不能绕开现有 `fallback_request_id` / `parent_request_id` 机制。  
   否则列表归组会乱，最终成功率统计会失真。

3. 不能把 attempt-1 的非有效占位 chunk 直接发给客户端后，再让 attempt-2 成功。  
   否则日志显示一组里多条尝试是合理的，但客户端会看到混合序列，和日志对不上。

### 可接受折中

- 首段观察期间增加极小的启动等待，这是可以接受的。
- 日志里保留失败 attempt，是必须保留的，不应“为了看起来干净”把前面的失败藏掉。

## 4.2 `framework/logstore`

### 结论

只要继续复用现有 fallback 日志模型，logstore 基本不需要改 schema。

### 已有行为

- `group_id = COALESCE(parent_request_id, id)`
- `attempt_sequence` 按 `fallback_index ASC, timestamp ASC, id ASC`
- `is_final_attempt` 按组内最后一条 attempt 判定
- 请求级成功率、最终分布都基于 final attempt 计算

### 影响

- 新能力会让一部分原本“只有一条失败 attempt”的请求，变成“前失败 + 后成功”的请求组。
- 这会直接改善：
  - `RequestSuccessRate`
  - `AverageFinalLatency`
  - final success distribution

### 必须规避

1. 不能人为复用第一次 attempt 的 log ID。  
   必须让 fallback 产生新 ID，不然 `CreateIfNotExists` 语义会被破坏。

2. 不能在客户端只隐藏失败 attempt、但统计层仍计入失败 attempt 为 final。  
   必须保证最终落库顺序仍然真实反映“最后一次 attempt 才是 final attempt”。

### 可接受折中

- Attempt 成功率会继续低于请求成功率，这本来就是对的。
- 新能力上线后，请求级成功率上升、attempt 级成功率变化较小，是合理现象，不是统计异常。

## 4.3 `core/providers/utils`

### 结论

这里是最适合承接最小改动的地方，但不能只用现成的 `CheckFirstStreamChunkForError` 直接上线。

### 原因

当前 `CheckFirstStreamChunkForError` 只判断：

- 第一个 chunk 是不是 error

这还不够，因为目标能力是：

- **首个有效片段前**

而不是：

- **第一个 chunk 前**

### 必须规避

1. 不能把 `responses.created` / `in_progress` 当成“已经开始输出”。  
   否则 Responses API 仍然会卡在原问题上。

2. 不能在 provider goroutine 已经通过 `ProcessAndSendBifrostError` 结束整条流语义之后，再在更上层偷偷 fallback。  
   否则 trace 和客户端事件顺序会错位。

3. 不能把“首段观察器”做到 HTTP handler 层。  
   那样只能拦住对客户端的输出，拦不住 core 里 fallback 时机、health tracking 和 trace 生命周期。

### 可接受折中

- 可以新增一个更准确的公共工具，例如“观察到首个有效 chunk 或错误”为止。
- 这个观察器可以短暂缓存少量前置 chunk。
- 缓存上限应非常小，只覆盖首段观察期，不能演变成整流缓冲。

## 4.4 `governance grouped routing` / health tracking

### 结论

健康度逻辑应该把“首个有效片段前失败”仍视为一次真实失败，这一点不能改。

### 已有行为

- `PreLLMHook` 会根据 `fallback_index` 刷新当前 layer、pinned key、`route_layer_index`
- `PostLLMHook` 对 grouped routing 请求记录：
  - `RecordRealAccess`
  - `RecordFailureForRule` 或 `RecordSuccessForRule`

### 影响

- 第一次 attempt 在首个有效片段前失败时：
  - 应记录 real access
  - 应记录 failure
- 后续 fallback 成功时：
  - 新 attempt 再记录 real access
  - final target 记录 success

这和“健康度是按 target 质量判断，不是按最终客户端有没有拿到结果判断”是一致的。

### 必须规避

1. 不能因为客户端最终成功了，就抹掉第一次 target 的失败。  
   否则健康检测会被“后续兜底成功”稀释，坏线路会继续被高估。

2. 不能自己写一套旁路 fallback，不走现有 `fallback_index` 更新。  
   否则 `route_layer_index`、健康 target、日志归组都会错位。

3. 不能把“拿到 stream channel”视为 target 成功。  
   这里真正要看的，是是否在首个有效输出前就失败。

### 可接受折中

- 单次请求层面成功了，但第一层 target 仍记失败，这是合理且应该保留的。
- 这会让“请求成功率上升”和“某层健康度继续偏低”同时出现；这不是冲突，而是两个口径不同。

## 4.5 trace

### 结论

trace 是这次设计里最容易被忽略、但最需要提前规避的问题。

### 风险点

当前一些流内错误走的是 `ProcessAndSendError`，另一些结构化错误走的是 `ProcessAndSendBifrostError`。后者在 `StreamEndIndicator=true` 时会触发 deferred span 完成。

如果第一次 attempt 在首个有效片段前报错，但 deferred `llm.call` span 已经被结束，后面再 fallback 成功，trace 会出现：

- attempt-1 把整次 llm.call 标成 error
- attempt-2 实际成功，但主 llm.call 已经结束
- 最终 trace 的主语义不再对应客户端真实看到的结果

### 必须规避

1. 不能让“可 fallback 的首段失败”提前结束整次 streaming `llm.call` span。

2. 不能让 attempt-1 的 accumulator / deferred span 生命周期直接污染 attempt-2。  
   特别是共用同一个 `traceID` 时，要么确保可重入重建，要么确保第一次失败不会把整体追踪提前封口。

### 可接受折中

- 可以接受 fallback attempt 继续保留独立 child span。
- 可以接受这类请求的 TTFT 统计更接近“最终成功 attempt 的首字时间”，而不是“第一次失败 attempt 的首错误时间”。
- 如果为了最小改动，首版只保证 trace 不错、不重复结束，而不强求每个 attempt 的 TTFT 都极致精确，这是可以接受的。

## 4.6 客户端体验

### 结论

客户端体验的核心目标只有一条：

> 如果 fallback 发生在首个有效片段前，客户端应该感觉这次请求只是“启动稍慢了一点”，而不是“先坏一下又恢复”。

### 必须规避

1. 不能先把失败 attempt 的 error event 发给客户端，再悄悄切到下一条。  
   这会直接破坏 SSE 语义。

2. 不能把 attempt-1 的 `created/in_progress` 发出去，再让 attempt-2 接着输出。  
   这在 Responses API 上会非常混乱。

3. 不能在已经发出任何有效内容后再切换 provider。  
   这会把两家模型的输出拼接到一起。

### 可接受折中

- 首个有效片段前，网关额外持有少量前置事件，这是合理的。
- 客户端看到的效果是“要么直接成功开始输出，要么最终返回一个错误”，不需要知道中间切了几次。

## 4.7 错误语义

### 结论

对外错误语义建议维持“只暴露最终结果”，不要把首段失败 attempt 作为对外可见错误事件抛给客户端。

### 必须规避

1. 不能把第一次失败和最终成功都发给客户端。  
   客户端只应看到最终成功流。

2. 如果所有 attempt 都在首个有效片段前失败，必须明确一个对外错误来源。  
   最小改动下，继续沿用现有整体 fallback 返回口径是可以的，但需要接受“最终返回的错误未必是最后一次失败原因最完整的那条”。

### 可接受折中

- 首版继续复用当前 fallback 总体错误返回语义，不额外引入“聚合错误”结构。
- 更丰富的 attempt 链路说明留给日志页，而不是 API 响应。

## 5. 必须规避的问题清单

以下问题如果处理不好，不建议上线：

1. 把“第一个 chunk”误当成“首个有效片段”。
2. 首段失败 fallback 时，没有继续写出第一次失败 attempt 的日志。
3. 绕开现有 fallback loop，自行实现旁路重试，导致 `fallback_index` / `route_layer_index` / `parent_request_id` 失真。
4. 因第一次可 fallback 错误而提前结束整次 streaming `llm.call` trace。
5. 把第一次 attempt 的占位事件或错误事件发给客户端后，再切到第二次 attempt。
6. 在已经发出任何有效内容后继续 fallback。
7. 因最终成功而不记录第一次 target 的 health failure。

## 6. 可接受的折中

以下折中是可以接受的：

1. 首段观察期带来轻微启动延迟。
2. Attempt 成功率仍然低于请求成功率。
3. 健康度上保留前置失败，即使客户端最终成功。
4. 首版不引入新的 API 错误结构，继续沿用现有整体错误语义。
5. 首版只覆盖“首个有效片段前失败”，不处理中途已经输出后的断流续切。

## 7. 最终建议

建议继续推进，但要按下面的约束推进：

1. 只做“首个有效片段前 fallback”。
2. 只在 core 流包装层加首段观察器，继续复用现有 fallback loop。
3. 明确定义“有效片段”，不要用“第一个 chunk”偷换。
4. 保留失败 attempt 的日志、health failure、route layer。
5. 确保客户端永远只看到最终选中的那条流。
6. 把 trace 视为上线前必须验证项，而不是补充项。

如果以上六条都满足，这个能力是可以以较小改动、较稳方式进入实现阶段的。
