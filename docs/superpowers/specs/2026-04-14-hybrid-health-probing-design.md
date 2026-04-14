# 混合健康探测设计稿

日期：2026-04-14

## 1. 结论

推荐方案是“被动优先，静默后主动补探测”。

具体做法：

- 继续保留现有 grouped routing 的被动健康记录和 cooldown 过滤。
- 不新建第二套健康状态，继续复用 `HealthTracker`。
- 在目标一段时间内没有任何真实请求结果时，才发起主动探测。
- 主动探测的结果仍然写回同一份健康状态。

这个方案最适合当前代码结构，原因有三点：

- 现有路由过滤逻辑已经可用，不需要重做选路。
- 现有健康状态页面已经可用，不需要先改 UI 才能看到结果。
- 主动探测只是在“没有新鲜被动观测”时补一笔状态，额外请求最少，也不会轻易盖过真实流量结果。

## 2. 目标

本次设计只解决一个问题：

当 grouped routing 某个目标长时间没有真实流量时，系统没有新的健康判断，只能等下一次真实请求失败后才知道它已经不可用。

本次设计要达到的效果：

- 有真实请求结果时，优先相信真实请求结果。
- 没有真实请求结果时，系统可以自己补一笔小请求，刷新目标健康状态。
- 目标一旦恢复，不需要等很久才重新参与选路。
- 改动尽量收敛在 `governance` 这一层，不影响现有主流程。

## 3. 不做的事

第一版明确不做下面这些内容：

- 不把主动探测扩展到所有路由模式，只做 grouped routing。
- 不改系统 `/health` 的语义。它仍然只看基础组件连通性，不看模型健康。
- 不做多实例共享健康状态。第一版仍然是单实例、进程内状态。
- 不做 embedding、images、audio、batch、file 这类操作的主动探测。
- 不给所有没有具体 `provider/model` 的目标做主动探测。
- 不引入新的持久化表，不把健康状态写数据库。

## 4. 现状

当前 grouped routing 的健康逻辑已经具备两个基础能力：

1. 请求结束后记录健康结果  
   grouped routing 请求成功时写 success，失败时写 failure。

2. 下次选路时过滤 cooldown 目标  
   目标达到失败阈值后进入 cooldown，选路时被排除。

当前结构的特点：

- `HealthTracker` 已经是 grouped routing 的统一健康状态来源。
- `buildGroupedRoutingDecision()` 已经在选路时调用 cooldown 判断。
- `/api/governance/health-status` 已经读取这份状态。
- UI 已经能展示 grouped routing 的健康状态。

现状的缺口只有一个：

- 如果某个目标很久没有真实流量，系统不会主动刷新它的状态。

## 5. 方案比较

| 方案 | 做法 | 优点 | 缺点 | 结论 |
| --- | --- | --- | --- | --- |
| A 纯主动 | 所有目标按周期探测 | 没流量也一直有状态 | 额外请求多，容易覆盖真实流量结果，改动更大 | 不选 |
| B 混合模式 | 有被动结果就用被动；静默一段时间才主动探测 | 最贴合现有结构，改动最小，状态也最接近真实使用 | 需要补后台探测器和少量状态字段 | 选这个 |
| C 轻量 ping | 只探服务活着没有 | 最省 | 无法判断 model/key 是否真的可用 | 不能满足需求 |

## 6. 推荐设计

### 6.1 总体原则

- 健康状态只保留一份，统一由 `HealthTracker` 管。
- 被动结果优先级高于主动结果。
- 主动探测不是常驻替代品，只是“没有新鲜被动观测时的补位机制”。
- 路由过滤逻辑不改，仍然沿用当前 cooldown 规则。

### 6.2 状态模型

在现有 `TargetHealthState` 上补充下面几个字段：

- `lastObservedAt`
  - 最近一次拿到健康结果的时间。
  - 成功和失败都会更新。

- `lastObservedRequestType`
  - 最近一次用于判断该目标健康的请求类型。
  - 第一版只支持 `chat`、`responses`、`text` 三类。

- `lastObservationSource`
  - `passive` 或 `active`。
  - 路由本身不依赖这个字段，但它有助于排查和后续展示。

同时调整成功写入逻辑：

- 现在 success 只会清空连续失败计数。
- 第一版建议改成：success 同时清空 `cooldownUntil`。

原因：

- 主动探测成功时，如果不清掉 cooldown，目标即使已经恢复也要继续等 cooldown 自然到期，主动探测的价值会明显下降。

### 6.3 候选目标范围

第一版只对“可确定的具体目标”做主动探测。

满足以下条件才会进入主动探测候选集：

- 来自 grouped routing 规则。
- target 有明确的 `provider` 和 `model`。
- 最近一次观测到的请求类型属于 `chat`、`responses`、`text`。

以下目标先保持被动模式：

- target 省略了 `provider` 或 `model`，只能在真实请求上下文里解析。
- 从未观测到可用的请求类型。
- 最近一次观测类型不是 `chat`、`responses`、`text`。

这样做的原因很直接：

- 没有具体 `provider/model` 的 target，不存在一个单一、稳定的后台探测对象。
- 没有已知请求类型的 target，第一版不应该猜测该用哪种 API 去试。

### 6.4 探测触发规则

新增一个后台循环任务，运行在 `governance` 插件内部。

每轮执行流程：

1. 获取当前内存中的 routing rules。
2. 找出启用了 grouped routing 的规则。
3. 收集其中可主动探测的具体目标。
4. 如果目标在“被动新鲜窗口”内已有观测，跳过。
5. 如果目标超过该窗口没有任何观测，执行主动探测。
6. 探测结果回写 `HealthTracker`。

这里的关键判断只有一个：

- `now - lastObservedAt < passive_freshness_window`
  - 是：认为最近已有被动或主动结果，不额外探测。
  - 否：触发主动探测。

### 6.5 主动探测请求

主动探测不走普通外部请求入口，而是直接复用内部 `Bifrost` client 发最小请求。

设计要求：

- 每次探测只打一条最小请求。
- 不走治理计费和预算统计。
- 不写业务日志。
- 不走 grouped routing 再次选路，避免递归。
- 禁用 provider 侧重试，防止一次探测放大成多次外部请求。

建议上下文设置：

- 指定 `APIKeyID`，确保探测落到目标配置的那把 key。
- 设置 `DisableProviderRetries = true`。
- 设置 `SkipPluginPipeline = true`。
- 使用独立短超时。

请求内容按最近一次已知请求类型构造：

- `chat`
  - 发送极小 messages，请求一个极短回复。

- `responses`
  - 发送极小 input，请求一个极短回复。

- `text`
  - 发送极小 prompt，请求一个极短回复。

主动探测的成功条件：

- 能拿到正常响应，且没有返回 `BifrostError`。

主动探测的失败条件：

- 返回错误、超时、鉴权失败、模型不可用、key 不可用，都按 failure 处理。

### 6.6 状态写回规则

主动探测和被动结果都写同一份 `HealthTracker`，只是 source 不同。

写回行为建议统一成两类：

- success
  - 更新 `lastObservedAt`
  - 更新 `lastObservedRequestType`
  - 更新 `lastObservationSource`
  - 清空连续失败计数
  - 清空 cooldown

- failure
  - 更新 `lastObservedAt`
  - 更新 `lastObservedRequestType`
  - 更新 `lastObservationSource`
  - 写入 `lastFailureTime`
  - 写入 `lastFailureMsg`
  - 增加失败窗口计数
  - 增加连续失败计数

这样可以保证：

- cooldown 依旧由当前规则触发。
- 主动和被动不会各算一套相互打架的状态。

### 6.7 选路行为

当前 `IsInCooldownForRule()` 和 grouped routing 的过滤逻辑保持不变。

也就是说：

- 第一版不改 routing decision 的结构。
- 第一版不改 fallback 链构建方式。
- 第一版不改“全部目标都不可用时”的兜底行为。

现有兜底继续保留：

- 如果某条 grouped rule 下所有目标都不可用，这条规则会被跳过。
- 后续仍然可以继续匹配别的规则，或者回到默认路由。

这点很重要，因为主动探测不应该把系统变成“全部挡死”的模式。

### 6.8 多规则复用同一目标

同一个具体目标可能出现在多条 grouped routing 规则里。

第一版建议这样处理：

- 后台收集候选目标时，按具体目标去重：`provider:model:key_id`
- 一次探测结果扇出写回到所有命中的 rule-scoped health bucket

这样做的好处：

- 外部探测请求最少
- 仍然保留 rule-scoped 的健康状态和 cooldown 语义

如果实现阶段为了缩短交付时间，也可以先接受“同一目标在不同规则里各探一次”，但这不作为推荐实现。

## 7. 配置建议

第一版建议只加少量全局配置，放在 `governance` 插件配置里，不碰 routing rule schema。

建议字段：

| 字段 | 说明 | 建议默认值 |
| --- | --- | --- |
| `active_health_probe_enabled` | 是否启用主动探测 | `false` |
| `active_health_probe_interval_seconds` | 后台扫描周期 | `15` |
| `active_health_probe_passive_freshness_seconds` | 多久没有新鲜观测才触发探测 | `30` |
| `active_health_probe_timeout_seconds` | 单次探测超时 | `5` |
| `active_health_probe_max_concurrency` | 单轮最大并发探测数 | `4` |

设计取舍：

- 第一版不做 per-rule 配置，避免再改 schema、API、UI。
- 先用全局配置把机制跑通，后续如果确有需要，再放大到 rule 级别。

## 8. 依赖注入方式

`governance` 插件初始化时，还拿不到最终的 `Bifrost` client。

这是因为：

- 插件先加载
- `Bifrost` client 后初始化

因此第一版建议采用“初始化后注入”：

1. 给 `GovernancePlugin` 增加 `SetBifrostClient(*bifrost.Bifrost)`。
2. 在 HTTP server 完成 `s.Client` 初始化后，把 client 注入 governance plugin。
3. governance plugin 在拿到 client 后，再启动主动探测循环。

这样改动比重做初始化顺序小很多，也不需要让插件自己反向去找 transport 层对象。

## 9. 预期改动范围

建议改动集中在下面这些文件：

- `plugins/governance/health_tracker.go`
  - 增加最近观测信息
  - 调整 success 的恢复逻辑
  - 补充主动探测可用的读写方法

- `plugins/governance/main.go`
  - 在被动 success/failure 时补写最近观测信息
  - 增加 `SetBifrostClient()`
  - 管理后台探测任务的生命周期

- `plugins/governance/active_probe.go`
  - 新增后台探测器实现

- `transports/bifrost-http/server/server.go`
  - 在 `Bifrost` client 初始化完成后注入 governance plugin

可选改动：

- `transports/bifrost-http/handlers/governance.go`
  - 如果要把 `last_observed_at` 和 `last_observation_source` 暴露给前端，这里会自然带出来

- `ui/app/workspace/adaptive-routing/healthStatusView.tsx`
  - 第一版可以不改
  - 如果后续要让页面直接标注“这个状态来自主动还是被动”，再补展示

## 10. 风险与边界

### 10.1 没有明确 provider/model 的目标

这是第一版最重要的边界。

如果 route group target 省略了 `provider` 或 `model`，它只能在真实请求上下文里解析。后台任务没有这个上下文，因此不能安全地主动探测。

处理方式：

- 明确跳过
- 保持被动模式

### 10.2 没有已知请求类型的目标

第一版不猜 API 类型。

如果目标从未被真实请求打到，或者最近一次请求类型不在支持范围内，就不主动探测。

处理方式：

- 明确跳过
- 保持被动模式

### 10.3 多实例不共享状态

第一版仍然沿用当前 grouped health routing 的单实例内存状态。

这意味着：

- 每个实例只根据自己看到的被动流量和主动探测更新状态
- 实例之间不共享结果

这和当前 grouped health routing 的行为是一致的，因此不是新的回退。

### 10.4 主动探测带来的外部请求成本

即使是最小请求，它仍然是实际外部请求。

处理方式：

- 只在静默后才探
- 设置最大并发
- 禁用 provider 重试
- 只对可确定目标探测

## 11. 验收标准

### 11.1 功能验收

- 有真实请求持续进入某个 grouped target 时，系统不应额外主动探测该 target。
- 某个 grouped target 超过静默窗口没有任何观测后，系统会主动发起探测。
- 主动探测失败后，该 target 会进入与现有规则一致的 cooldown 判断流程。
- 处于 cooldown 的 target 如果被主动探测成功，应恢复为可选状态，不再继续等原 cooldown 自然到期。
- 所有 target 都不可用时，系统仍保持现有兜底，不把请求直接挡死。
- 没有明确 `provider/model` 的 target，不会被主动探测。
- 没有已知支持请求类型的 target，不会被主动探测。

### 11.2 运行验收

- 主动探测不应触发预算扣减。
- 主动探测不应触发治理拒绝逻辑。
- 主动探测不应因为 provider 内部重试而放大外部请求次数。
- 主动探测任务在 server 关闭时能正常停止。

## 12. 后续阶段

如果第一版效果符合预期，后续可以再考虑下面三件事：

1. 把 `last_observed_at`、`last_observation_source` 展示到健康页。
2. 为没有已知请求类型的目标提供显式 probe template。
3. 在多实例场景下引入共享健康状态。

## 13. 最终建议

按下面这个最小版本进入实现最合适：

- 仅支持 grouped routing
- 仅探测有明确 `provider/model` 的目标
- 仅支持最近一次观测类型为 `chat`、`responses`、`text` 的目标
- 继续复用 `HealthTracker`
- 保持当前 routing 过滤逻辑不变
- 在 `governance` 内部新增后台探测器
- 在 `Bifrost` client 初始化后注入给 governance plugin

这是最像 LiteLLM 主动探测效果、同时又最贴合当前 Bifrost 结构的版本。
