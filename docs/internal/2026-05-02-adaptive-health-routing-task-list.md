# Adaptive Health Routing Implementation Task List

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将现有 grouped health routing 从“失败才降级”的二态逻辑，升级为“最近窗口内根据慢成功、软失败、硬失败、退避、半开恢复综合判断”的三态健康路由，同时保持低成本组优先但不让慢目标长期占据主路由。

**Architecture:** 原地升级现有 `plugins/governance` grouped routing，不新增第二套路由系统。配置仍挂在 `TableRoutingRule.health_policy` 和 `route_groups` JSON 上；路由决策仍由 `buildGroupedRoutingDecision` 产出 primary + fallbacks；健康状态仍由 in-process `HealthTracker` 管理。

**Tech Stack:** Go multi-module workspace, `plugins/governance`, `framework/configstore`, `transports/bifrost-http`, Next.js UI, Vitest, existing Makefile test targets.

## 当前复核结论（2026-05-02）

本地代码开发已经基本完成：自适应三态健康路由、`fallback_only` 兜底组、half-open、真实请求 latency 记录、provider pricing overrides、同组同健康桶成本排序、active probe liveness-only、stream-only health detection 支持、本地黑盒仿真都已落地。

剩余不是本地开发项，而是上线/配置项：

- [ ] 部署当前代码到线上。
- [ ] 上线前确认 `gemini-auto` 的最终兜底组已显式设置 `fallback_only=true`，且兜底组 `retry_limit` 是预期值。
- [ ] 在 provider 后台给线上自定义模型补 `pricing_overrides`，按 $/1M input/output tokens 填写即可。
- [ ] active probe 默认仍不自动开启；如果线上已经手动开启，它也只做 liveness，不参与主健康评分。
- [ ] 可选清理旧的 health detection target preference 脏数据；清理前先确认 target 不再出现在任何 route group。

---

## 0. 实施边界

- [ ] 不创建新分支、不创建 worktree，默认在当前工作区修改。
- [ ] 不连接或修改远程服务器；本清单只描述本地代码实现与上线操作。
- [ ] 不实现“评分式全局路由器”，不引入质量综合打分；成本只复用现有 provider pricing，在同组、同健康桶内排序。
- [ ] 不实现 per-attempt 硬 deadline；`request_deadline_ms` 本轮仅作为预留字段，默认值必须是 `0`，表示禁用。
- [ ] 不自动开启 active probe；全局默认仍是关闭，用户需要时手动打开。开启后也只做 liveness，不参与主健康状态机。
- [ ] 不破坏旧 schema；只追加 JSON 字段。老规则上线后会统一升级到新行为，如需旧行为由 policy 显式覆盖。

## 0.1 复核后必须先消歧的点

- [ ] `slow_recovery_seconds=0` 必须表示“显式关闭 last-slow recency 判定”，不能被默认值逻辑改回 `60`。
  - [ ] 推荐落地：`HealthPolicy.SlowRecoverySeconds` 使用 `*int`，nil 默认 `60`，显式 `0` 表示关闭。
  - [ ] `HealthPolicyRequest.SlowRecoverySeconds` 也使用 `*int`，从 HTTP 请求到 DB JSON 全链路保留显式 `0`。
  - [ ] 如果最终不采用指针字段，必须改设计文档和“等价旧行为”模板，不能让 `0` 同时表示默认和关闭。
- [ ] `slow_ratio_threshold=999` 是设计文档指定的关闭 slow ratio 配法，因此后端校验和 UI 输入不能强制 `<= 1`。
  - [ ] 推荐校验：`slow_ratio_threshold > 0`；`>1` 明确视为禁用 ratio degraded。
  - [ ] 文案说明默认推荐 `0.5`，高级用户可填 `999` 关闭。
- [ ] `consecutive_failures=0` 是现有语义：使用 `failure_threshold`。后端、schema、UI 都必须允许 `0`。
- [ ] API 兼容性优先：保留 legacy `status: "available" | "cooldown"`，新增 `health_level: "healthy" | "degraded" | "cooldown"` 给新 UI 使用。不要直接把 `status` 改成三态，避免旧 UI/调用方解析失败。
- [ ] Outcome 分类以设计文档 §3.2 为准：429、408、502、503、504、quota/rate-limit/saturated/overloaded 属于 `SoftFail`；未知 5xx 默认 `HardFail`；除 429/408 外的普通 4xx 默认不污染健康，等价 `Success`。

## 1. 基线确认

- [ ] 阅读并确认设计文档：[2026-05-02-adaptive-health-routing-redesign.md](./2026-05-02-adaptive-health-routing-redesign.md)。
- [ ] 确认当前核心文件位置：
  - [framework/configstore/tables/routing_rules.go](../../framework/configstore/tables/routing_rules.go)
  - [plugins/governance/health_tracker.go](../../plugins/governance/health_tracker.go)
  - [plugins/governance/grouped_router.go](../../plugins/governance/grouped_router.go)
  - [plugins/governance/main.go](../../plugins/governance/main.go)
  - [plugins/governance/active_probe.go](../../plugins/governance/active_probe.go)
  - [transports/bifrost-http/handlers/adaptive_routing.go](../../transports/bifrost-http/handlers/adaptive_routing.go)
  - [transports/bifrost-http/handlers/governance.go](../../transports/bifrost-http/handlers/governance.go)
  - [transports/config.schema.json](../../transports/config.schema.json)
  - [ui/lib/types/routingRules.ts](../../ui/lib/types/routingRules.ts)
  - [ui/app/workspace/routing-rules/views/routingRuleSheet.tsx](../../ui/app/workspace/routing-rules/views/routingRuleSheet.tsx)
  - [ui/app/workspace/adaptive-routing/healthStatusView.tsx](../../ui/app/workspace/adaptive-routing/healthStatusView.tsx)
- [ ] 运行一次当前相关测试，记录改动前状态：
  - [ ] `make test-governance`
  - [ ] `(cd transports && go test ./schema_test ./bifrost-http/handlers -run 'Test.*(Adaptive|Routing|Schema)')`
  - [ ] `(cd ui && npx vitest run app/workspace/adaptive-routing/healthDetectionConfig.test.ts app/workspace/adaptive-routing/healthDetectionTargets.test.ts app/workspace/routing-rules/views/routeGroupState.test.ts)`
- [ ] 如果基线测试已有失败，先记录失败用例和原因；不要把既有失败混入本次改造结论。

## 2. 配置结构与默认值

- [ ] 在 [framework/configstore/tables/routing_rules.go](../../framework/configstore/tables/routing_rules.go) 扩展 `HealthPolicy`：
  - [ ] `SlowThresholdMs int`，JSON 字段 `slow_threshold_ms`，默认 `45000`。
  - [ ] `SlowWindowSize int`，JSON 字段 `slow_window_size`，默认 `10`。
  - [ ] `SlowRatioThreshold float64`，JSON 字段 `slow_ratio_threshold`，默认 `0.5`。
  - [ ] `SlowRecoverySeconds *int`，JSON 字段 `slow_recovery_seconds`，nil 默认 `60`，显式 `0` 表示关闭 last-slow recency 判定。
  - [ ] `RequestDeadlineMs int`，JSON 字段 `request_deadline_ms`，默认 `0`，本轮不生效。
  - [ ] `SoftCooldownMultiplier float64`，JSON 字段 `soft_cooldown_multiplier`，默认 `2.0`。
  - [ ] `CooldownBackoffFactor float64`，JSON 字段 `cooldown_backoff_factor`，默认 `2.0`。
  - [ ] `CooldownMaxSeconds int`，JSON 字段 `cooldown_max_seconds`，默认 `600`。
  - [ ] `HalfOpenProbe *bool`，JSON 字段 `half_open_probe`，nil 时默认 `true`，显式 `false` 必须保留。
- [ ] 在 `RouteGroup` 上追加 `FallbackOnly bool`，JSON 字段 `fallback_only`，默认 `false`。
- [ ] 新增或迁移 `ApplyHealthPolicyDefaults(policy *HealthPolicy) *HealthPolicy`：
  - [ ] nil policy 返回完整默认 policy。
  - [ ] 零值字段填默认值。
  - [ ] `ConsecutiveFailures == 0` 保留为“使用 failure_threshold”，不要填成 2。
  - [ ] `SlowRecoverySeconds == nil` 时写入 `60` 指针。
  - [ ] `SlowRecoverySeconds != nil && *SlowRecoverySeconds == 0` 时保留显式关闭语义。
  - [ ] `HalfOpenProbe == nil` 时写入 `true` 指针。
  - [ ] `RequestDeadlineMs == 0` 保持禁用，不填成 `90000`。
- [ ] 将 `plugins/governance/grouped_router.go` 当前的 `defaultHealthPolicy()` 改为调用统一默认值函数，避免后端、HTTP handler、UI 看到不同默认。
- [ ] 检查 `BeforeSave` / `AfterFind`，确认新增字段通过 `sonic` JSON 自动序列化，不需要新增 DB 列。
- [ ] 检查 [framework/configstore/clientconfig.go](../../framework/configstore/clientconfig.go) 的 routing rule hash 逻辑，确认新增 JSON 字段参与 hash，配置变更能触发 reload。

**验收标准**

- [ ] 老 JSON 缺少新字段时能被正常读取并补默认。
- [ ] `half_open_probe:false` 不会被默认值覆盖成 true。
- [ ] `slow_recovery_seconds:0` 不会被默认值覆盖成 60。
- [ ] `consecutive_failures:0` 不会被校验或默认值改掉。
- [ ] `fallback_only` 缺失时等价 false。

## 3. Schema 与后端入参校验

- [ ] 更新 [transports/config.schema.json](../../transports/config.schema.json) 的 `$defs.health_policy`：
  - [ ] 补齐 §2 的所有新字段、类型、最小值和默认值说明。
  - [ ] `request_deadline_ms` 描述必须明确“当前版本预留，0 表示禁用”。
  - [ ] `half_open_probe` 类型为 boolean，允许缺省。
- [ ] 更新 `$defs.route_group`：
  - [ ] 增加 `fallback_only` boolean。
  - [ ] 描述为“不抢普通组 primary；追加到当前请求 fallback 链末尾；普通组全不可用时可作为 primary”。
- [ ] 更新 [transports/schema_test/config_schema_test.go](../../transports/schema_test/config_schema_test.go)：
  - [ ] 新增配置示例覆盖所有新 health policy 字段。
  - [ ] 新增配置示例覆盖 `route_groups[].fallback_only`。
  - [ ] 新增配置示例覆盖 `slow_ratio_threshold=999`、`slow_recovery_seconds=0`、`consecutive_failures=0`，防止 schema 把灰度/旧行为配法挡掉。
  - [ ] 保留现有 grouped routing without legacy targets 测试。
- [ ] 更新 [transports/bifrost-http/handlers/governance.go](../../transports/bifrost-http/handlers/governance.go) 的 routing rule 创建/更新校验：
  - [ ] `HealthPolicyRequest` 补齐所有新字段，使用指针字段保留“未填”和“显式 0”的差异。
  - [ ] `RouteGroupRequest` 增加 `FallbackOnly *bool` 或 `FallbackOnly bool`，build 时写入 `RouteGroup.FallbackOnly`。
  - [ ] `buildHealthPolicy` 调用统一默认值函数，且不丢弃 `slow_recovery_seconds:0`、`half_open_probe:false`。
  - [ ] 新字段允许保存。
  - [ ] ratio 字段限制为 `slow_ratio_threshold > 0`；允许 `999` 这类大于 1 的关闭值。
  - [ ] `cooldown_backoff_factor >= 1`。
  - [ ] `soft_cooldown_multiplier >= 1`。
  - [ ] `cooldown_max_seconds >= cooldown_seconds`，或在 defaults 中自动抬升。
  - [ ] `request_deadline_ms` 允许 `0`。
  - [ ] `consecutive_failures` 允许 `0`。
- [ ] 确认 redaction、list、get、reload 路径不会丢弃 `health_policy` 和 `route_groups` 新字段。

**验收标准**

- [ ] config schema 能通过新示例。
- [ ] API 创建/更新 routing rule 后，新字段能从 get/list 返回。
- [ ] 非 grouped routing 的老规则不要求填写 `health_policy` 或 `route_groups`。

## 4. Outcome 分类器

- [ ] 新建 [plugins/governance/outcome_classifier.go](../../plugins/governance/outcome_classifier.go)。
- [ ] 定义 `OutcomeKind`：
  - [ ] `OutcomeSuccess`
  - [ ] `OutcomeSlow`
  - [ ] `OutcomeSoftFail`
  - [ ] `OutcomeHardFail`
- [ ] 实现 `ClassifyOutcome(err *schemas.BifrostError, latency time.Duration, slowThreshold time.Duration) OutcomeKind`，函数签名与设计文档 §3.2 对齐：
  - [ ] `err == nil && latency < SlowThresholdMs` => `Success`。
  - [ ] `err == nil && latency >= SlowThresholdMs` => `Slow`。
  - [ ] deadline abort、provider timeout、408、429、502、503、504 => `SoftFail`。
  - [ ] saturated、quota、rate limit、too many、overloaded、upstream busy、context_length_exceeded 等关键词 => `SoftFail`。
  - [ ] 未识别 5xx 默认按 `HardFail`。
  - [ ] 除 429/408 外的普通 4xx 默认不污染健康，等价 `Success`。
  - [ ] 401、403、bad auth、permission denied、model not found 如需特殊处理，必须先在测试中明确期望；默认不要把普通 4xx 计为 target 健康失败。
  - [ ] 分类器不得修改 response、error 或 context。
- [ ] 新增 [plugins/governance/outcome_classifier_test.go](../../plugins/governance/outcome_classifier_test.go)：
  - [ ] `TestClassifyOutcome_FastSuccess`
  - [ ] `TestClassifyOutcome_SlowSuccess`
  - [ ] `TestClassifyOutcome_429IsSoftFail`
  - [ ] `TestClassifyOutcome_TimeoutIsSoftFail`
  - [ ] `TestClassifyOutcome_QuotaIsSoftFail`
  - [ ] `TestClassifyOutcome_ContextLengthExceededIsSoftFail`
  - [ ] `TestClassifyOutcome_Unknown5xxIsHardFail`
  - [ ] `TestClassifyOutcome_Normal4xxDoesNotPolluteHealth`

**验收标准**

- [ ] 分类逻辑有单元测试锁住，不依赖字符串裸猜测散落在 `PostLLMHook` 中。
- [ ] 慢成功不会被当成失败，但会被记录为可导致 Degraded 的信号。

## 5. HealthTracker 三态状态机

- [ ] 在 [plugins/governance/health_tracker.go](../../plugins/governance/health_tracker.go) 扩展 `TargetHealthState`：
  - [ ] 最近窗口样本，例如 `recentSamples []OutcomeSample`，长度上限为 `max(20, policy.SlowWindowSize)`。
  - [ ] `slowCount int`，维护最近窗口内 slow 样本数。
  - [ ] `softFailCount int`，维护最近窗口内 soft fail 样本数。
  - [ ] `hardFailCount int`，维护最近窗口内 hard fail 样本数。
  - [ ] `lastSlowAt time.Time`。
  - [ ] `lastSlowLatency time.Duration`。
  - [ ] `cooldownStreak int`。
  - [ ] `halfOpenInFlight bool`。
  - [ ] `halfOpenStartedAt time.Time`。
  - [ ] `lastOutcomeKind OutcomeKind`。
- [ ] 定义 `HealthLevel` 常量，API JSON 值统一为：
  - [ ] `"healthy"`
  - [ ] `"degraded"`
  - [ ] `"cooldown"`
- [ ] 新增 `RecordOutcome(ruleID, targetKey string, kind OutcomeKind, latency time.Duration, failureMsg string, policy *HealthPolicy, now time.Time)`，函数签名与设计文档 §3.5 对齐：
  - [ ] `Success`：记录样本，清理连续失败；如果当前不是 cooldown，则可清理退化状态。
  - [ ] `Slow`：记录慢样本，不清零已有 hard/soft failure 造成的 cooldown；达到 slow ratio 后返回 Degraded。
  - [ ] `SoftFail`：记录失败，cooldown 时长使用 `CooldownSeconds * SoftCooldownMultiplier`，再乘以指数退避 streak。
  - [ ] `HardFail`：记录失败，cooldown 时长使用 `CooldownSeconds`，再乘以指数退避 streak。
  - [ ] cooldown 时长必须被 `CooldownMaxSeconds` 封顶。
  - [ ] 成功的 half-open 探针释放 cooldown；失败则重新进入退避。
- [ ] 保留 `RecordFailureForRule` / `RecordSuccessForRule` 作为兼容 wrapper 时，wrapper 内部必须调用 `RecordOutcome`；或一次性更新所有调用点。不能留下旧路径绕过新状态机。
- [ ] 新增 `GetTargetHealthForRule(ruleID, targetKey, policy, now)`：
  - [ ] cooldown 未到期 => `cooldown`。
  - [ ] cooldown 到期且 `HalfOpenProbe == true` => 只允许一个 half-open in-flight；其他请求仍视为 cooldown。
  - [ ] 最近 `SlowWindowSize` 样本内 slow ratio 达阈值，且未超过 recovery 条件 => `degraded`。
  - [ ] 其他情况 => `healthy`。
- [ ] 扩展 `TargetHealthSnapshot`：
  - [ ] 保留 `status: "available" | "cooldown"` 作为 legacy 字段。
  - [ ] 新增 `health_level: "healthy" | "degraded" | "cooldown"` 作为新三态字段。
  - [ ] `slow_count`。
  - [ ] `sample_count`。
  - [ ] `slow_ratio`。
  - [ ] `p95_latency_ms`，与设计文档/界面列名保持一致。
  - [ ] `cooldown_streak`。
  - [ ] `last_slow_at`。
  - [ ] `last_slow_latency_ms`。
  - [ ] `last_outcome_kind: "success" | "slow" | "soft_fail" | "hard_fail"`。
  - [ ] `half_open_in_flight`。
  - [ ] 保留 `failure_count`、`consecutive_failures`、`cooldown_until`、`last_failure_msg` 等现有字段。
- [ ] 更新 [plugins/governance/health_tracker_test.go](../../plugins/governance/health_tracker_test.go)：
  - [ ] `TestRecordOutcome_SlowSuccess_DoesNotResetFailures`
  - [ ] `TestGetTargetHealth_DegradedBySlowRatio`
  - [ ] `TestGetTargetHealth_RecoversToHealthy`
  - [ ] `TestHalfOpenProbe_OnlyOneInFlight`
  - [ ] `TestExponentialBackoff_StreakIncrement`
  - [ ] `TestRecordOutcome_SoftFailUsesMultiplier`
  - [ ] `TestRecordOutcome_HardFailUsesBaseCooldown`
  - [ ] `TestApplyHealthPolicyDefaults_PreservesExplicitHalfOpenFalse`
  - [ ] `TestApplyHealthPolicyDefaults_PreservesExplicitSlowRecoveryZero`
  - [ ] `TestGetTargetHealth_SlowRatioThresholdAboveOneDisablesDegraded`
  - [ ] `TestConsecutiveFailuresZeroFallsBackToFailureThreshold`

**验收标准**

- [ ] 三态判定只在 `HealthTracker` 内集中实现。
- [ ] 慢成功影响后续路由，但不会触发当前请求的立即 fallback。
- [ ] cooldown、退避、half-open 在并发下不会产生多个探针同时放行。

## 6. Grouped Router 选择规则

- [ ] 在 [plugins/governance/grouped_router.go](../../plugins/governance/grouped_router.go) 重写 `buildGroupedRoutingDecision` 的目标分桶：
  - [ ] 普通组 `healthy` 目标，按 group 顺序参与主链。
  - [ ] 普通组 `degraded` 目标，在所有普通 healthy 后参与。
  - [ ] `fallback_only=true` 组不参与 healthy/degraded 竞争，但会追加到当前请求 fallback 链末尾；所有普通组都没有可用目标时可作为 primary。
  - [ ] cooldown 但允许 half-open 的目标只能进入 fallback 链尾部，不抢 primary。
  - [ ] 完全 cooldown 且不允许 half-open 的目标跳过。
- [ ] 保留现有能力：
  - [ ] group 内按 weight 随机。
  - [ ] group 内按 `1 + retry_limit` 选择 slot。
  - [ ] 普通组按 `TargetKey` 去重，不重复同一 target；`fallback_only=true` 组允许重复已选 target 来消耗 retry budget。
  - [ ] `RoutingDecision.PrimaryLayer` 和 `FallbackLayerPlan` 继续准确记录 layer 信息。
  - [ ] key pin 和 fallback key id 继续对齐。
- [ ] 更新路由日志：
  - [ ] 输出每组 healthy/degraded/cooldown/fallback-only 数量。
  - [ ] 输出目标被过滤、降级、兜底、half-open tail 的原因。
- [ ] 更新 [plugins/governance/grouped_router_test.go](../../plugins/governance/grouped_router_test.go)：
  - [ ] `TestRouter_HealthOverCost_DegradedYieldsToNextGroup`
  - [ ] `TestRouter_LastLayerOnlyAsLastResort`
  - [ ] `TestRouter_LastLayerNeverPromotedWhenAnyEarlierAvailable`
  - [ ] `TestRouter_HalfOpenProbeSinksToFallbackTail`
  - [ ] `TestRouter_FallbackOnlyAppendedAfterRegularHealthyExists`
  - [ ] `TestRouter_FallbackOnlyAppendedAfterRegularDegradedExists`
  - [ ] `TestRouter_FallbackOnlyRetryLimitRepeatsSingleTarget`
  - [ ] `TestRouter_DedupAcrossHealthBuckets`
  - [ ] `TestRouter_RetryLimitStillAppliesPerGroup`

**验收标准**

- [ ] 低成本组 healthy 时优先用低成本组。
- [ ] 低成本组 degraded 且后面普通组 healthy 时，切到后面普通组。
- [ ] 兜底组不会因为 healthy 就越过 degraded 的普通组。
- [ ] 普通链路在同一请求里都失败时，会继续尝试兜底组。
- [ ] 单 target 兜底组设置 `retry_limit=2` 时，当前请求最多尝试 3 次该兜底 target。
- [ ] 没有可用目标时返回 nil，让现有逻辑处理无路由结果。

## 7. PreLLMHook / PostLLMHook 集成

- [ ] 在 [plugins/governance/main.go](../../plugins/governance/main.go) 增加 grouped routing attempt 起始时间 context key：
  - [ ] 在每次 `PreLLMHook` 尝试开始时写入 `time.Now()`。
  - [ ] fallback attempt 进入 `PreLLMHook` 时必须刷新起始时间。
  - [ ] 不设置 context deadline，不调用不存在的 `SetDeadline`。
- [ ] 在 `PostLLMHook` 里改造健康记录：
  - [ ] 从 context 读取 rule id、当前 layer、pinned key id。
  - [ ] 计算 attempt latency。
  - [ ] 读取 rule policy 并 apply defaults。
  - [ ] 调用 `ClassifyOutcome`。
  - [ ] 调用 `RecordOutcome`。
  - [ ] 继续记录 `RecordRealAccess`，供 active probe idle 判断使用。
- [ ] 确认大 payload 路径 `governLargePayload` 和普通路径都能设置 grouped routing context。
- [ ] 确认 skip key selection、governance rejected、非 grouped routing 请求不会污染 health tracker。
- [ ] 增加或扩展 governance plugin 测试：
  - [ ] success + latency below threshold 记录 healthy。
  - [ ] success + latency above threshold 记录 slow。
  - [ ] fallback attempt 使用 fallback layer 的 provider/model/key_id 记录，不误记 primary。
  - [ ] non grouped routing request 不记录 health。

**验收标准**

- [ ] 健康记录与真实 attempt 对齐，不再只按最终 response 粗略判断。
- [ ] 当前请求不会因为慢成功被中断；慢成功只影响后续路由。

## 8. Active Probe 调整

- [ ] 在 [plugins/governance/active_probe.go](../../plugins/governance/active_probe.go) 调整默认/推荐配置：
  - [ ] `Enabled` 默认继续 `false`。
  - [ ] `Interval` 默认或推荐改为 `60s`。
  - [ ] `IdlePause` 默认或推荐改为 `5m` / `300s`，兼容现有 `ActiveHealthProbeIdlePauseMinutes` 与 `ActiveHealthProbePassiveFreshnessSeconds`。
  - [ ] `Timeout` 保持现有合理值，除非设计文档另有指定。
- [ ] `buildActiveProbePlans` 跳过 `RouteGroup.FallbackOnly == true` 的组。
- [ ] 手动开启 active probe 后：
  - [ ] 只探测用户在统一目标列表中启用的 target。
  - [ ] idle 超过阈值的目标暂停探测。
  - [ ] stream-only 目标按设计文档改用 ChatCompletion 探针请求，`max_tokens=1`、`temperature=0`，不因真实业务请求是 stream 就直接 unsupported。
- [ ] `applyActiveProbeResult` 只调用 `RecordProbeResult`：
  - [ ] 成功探针只更新 last probe success，不清空 cooldown。
  - [ ] 失败探针只更新 last probe failure，不触发 cooldown。
  - [ ] probe 结果不进入 slow ratio / p95 / routing health。
- [ ] 更新 [plugins/governance/active_probe_test.go](../../plugins/governance/active_probe_test.go)：
  - [ ] 默认配置 disabled。
  - [ ] 推荐 interval/idle pause 生效。
  - [ ] fallback-only target 不生成 probe plan。
  - [ ] stream-only history 生成 ChatCompletion probe plan。
  - [ ] active probe result 不改变主健康状态机。

**验收标准**

- [ ] 上线后不会突然增加后台探测流量。
- [ ] 兜底层不会被主动探测拉热。
- [ ] probe 结果与真实流量结果分离；真实业务请求和 half-open 请求才影响 routing health。

## 9. HTTP API 与健康状态输出

- [ ] 更新 [transports/bifrost-http/handlers/adaptive_routing.go](../../transports/bifrost-http/handlers/adaptive_routing.go)：
  - [ ] `healthPolicyOrDefault` 改为统一默认值函数。
  - [ ] `getHealthStatus` 返回三态 status 和新增指标。
  - [ ] target snapshot 包含 p95 latency、slow ratio、sample count、cooldown streak。
  - [ ] 兼容没有 health state 的 target，默认返回 healthy + 空指标。
- [ ] 更新 health detection targets 汇总：
  - [ ] `cooldown_rule_count` 保持语义为 cooldown，不把 degraded 算作 cooldown。
  - [ ] 如需要，新增 `degraded_rule_count`，UI 可用来展示黄色状态。
  - [ ] stream-only 目标如果能用 ChatCompletion 探测，应返回 supported，不再因为最近真实请求是 stream 就变灰。
- [ ] 更新 [transports/bifrost-http/handlers/adaptive_routing_test.go](../../transports/bifrost-http/handlers/adaptive_routing_test.go)：
  - [ ] health status 包含 healthy/degraded/cooldown 三态。
  - [ ] fallback-only target 在 health status 中可见，但 active probe 列表不自动启用。
  - [ ] 新字段 JSON 名称与 UI 类型一致。

**验收标准**

- [ ] `/api/adaptive-routing/health-status` 能完整表达新状态。
- [ ] UI 不需要靠解析 `last_failure_msg` 推断状态。
- [ ] 老 target 无历史状态时页面不会显示错误或空白。

## 10. UI 类型与 Routing Rule 表单

- [ ] 更新 [ui/lib/types/routingRules.ts](../../ui/lib/types/routingRules.ts)：
  - [ ] `HealthPolicy` 补齐所有新字段。
  - [ ] `slow_recovery_seconds` 类型允许显式 `0`；`DEFAULT_HEALTH_POLICY` 中使用 `60`。
  - [ ] `consecutive_failures` 默认值改为 `0`，与后端“0 = use failure_threshold”一致。
  - [ ] `RouteGroup` / `RouteGroupFormData` 增加 `fallback_only`。
  - [ ] `HealthSnapshot.status` 保留 `"available" | "cooldown"`。
  - [ ] `HealthSnapshot.health_level` 增加 `"healthy" | "degraded" | "cooldown"`。
  - [ ] `HealthSnapshot` 增加 slow ratio、sample count、latency、streak 字段。
  - [ ] `DEFAULT_HEALTH_POLICY` 与后端默认一致，`request_deadline_ms: 0`。
- [ ] 更新 [ui/app/workspace/routing-rules/views/routeGroupState.ts](../../ui/app/workspace/routing-rules/views/routeGroupState.ts)：
  - [ ] 处理 `fallback_only` 的复制、更新、默认值。
  - [ ] 保持 target provider/model/key_id 更新逻辑不变。
- [ ] 更新 [ui/app/workspace/routing-rules/views/routingRuleSheet.tsx](../../ui/app/workspace/routing-rules/views/routingRuleSheet.tsx)：
  - [ ] Health Policy 基础区保留现有字段。
  - [ ] 增加默认折叠的 Advanced 区，放 latency、slow ratio、退避、half-open、deadline 预留字段。
  - [ ] `Request deadline (ms)` 默认显示 `0`，help text 说明当前版本禁用。
  - [ ] `Slow ratio threshold` 允许输入 `999`，用于关闭 slow ratio degraded。
  - [ ] `Slow recovery (s)` 允许输入 `0`，用于关闭 last-slow recency degraded。
  - [ ] `Consecutive failures` 允许输入 `0`，用于回退到 `failure_threshold`。
  - [ ] `half_open_probe` 使用 switch/checkbox，显式 false 可保存。
  - [ ] Route group 编辑区增加 `Fallback only` checkbox。
  - [ ] 表单 submit 时完整带上新字段；非 grouped routing 时仍不发送 health policy 和 route groups。
- [ ] 更新 UI 测试：
  - [ ] [ui/app/workspace/routing-rules/views/routeGroupState.test.ts](../../ui/app/workspace/routing-rules/views/routeGroupState.test.ts) 覆盖 `fallback_only` 保留。
  - [ ] 新增或扩展 routing rule sheet 测试，覆盖高级字段默认值、显式 half-open false、`slow_ratio_threshold=999`、`slow_recovery_seconds=0`、`consecutive_failures=0`。

**验收标准**

- [ ] 新字段能在创建、编辑、保存、再次打开时保留。
- [ ] 老规则打开后不会出现 NaN、空输入或错误默认。
- [ ] 用户可以只通过 UI 配出“完全等价旧行为”。

## 11. UI Adaptive Routing 页面

- [ ] 更新 [ui/app/workspace/adaptive-routing/healthStatusView.tsx](../../ui/app/workspace/adaptive-routing/healthStatusView.tsx)：
  - [ ] Status badge 使用 `health_level` 从 Available/Cooldown 展示为 Healthy/Degraded/Cooldown。
  - [ ] Healthy 使用绿色，Degraded 使用黄色，Cooldown 使用红色。
  - [ ] 新增列：p95 latency、slow ratio、sample count、cooldown streak。
  - [ ] 原有 last observed、source、last failure、cooldown until 保留。
  - [ ] summary card 中区分 degraded target 和 cooldown target。
- [ ] 更新 [ui/app/workspace/adaptive-routing/healthDetectionTargets.ts](../../ui/app/workspace/adaptive-routing/healthDetectionTargets.ts)：
  - [ ] 增加三态状态标签/描述 helper，避免页面里散落判断。
  - [ ] 空指标格式化为 `-` 或现有 UI 约定。
- [ ] 更新 [ui/app/workspace/adaptive-routing/healthDetectionTargetsTable.tsx](../../ui/app/workspace/adaptive-routing/healthDetectionTargetsTable.tsx)：
  - [ ] 如果 API 新增 `degraded_rule_count`，表格显示 degraded/cooldown 汇总。
  - [ ] 不改变现有 target enable/disable 操作语义。
- [ ] 更新 UI 测试：
  - [ ] [ui/app/workspace/adaptive-routing/healthDetectionConfig.test.ts](../../ui/app/workspace/adaptive-routing/healthDetectionConfig.test.ts)
  - [ ] [ui/app/workspace/adaptive-routing/healthDetectionTargets.test.ts](../../ui/app/workspace/adaptive-routing/healthDetectionTargets.test.ts)
  - [ ] 新增 health status formatting 测试，覆盖 healthy/degraded/cooldown 三态。

**验收标准**

- [ ] 页面能解释“黄色 degraded 不是不可用，只是优先级降低”。
- [ ] 文案不暗示 active probe 已自动开启。
- [ ] 文案明确 active probe 是 liveness-only，不暗示它会参与路由评分或自动恢复 target。
- [ ] 所有状态和指标在窄屏下不溢出、不重叠。

## 11.5 Provider 定价覆盖与组内成本排序

- [ ] 复用现有 ModelCatalog 价格系统，不新增 routing 专用价格表。
- [ ] provider 配置增加 `pricing_overrides` 往返保存：
  - [ ] `exact` / `wildcard` / `regex` 三种匹配。
  - [ ] 可选 request type 过滤。
  - [ ] 支持覆盖 `input_cost_per_token` 和 `output_cost_per_token`。
  - [ ] 未填写字段回退 datasheet 价格。
- [ ] provider UI 增加 Pricing Overrides 页签：
  - [ ] 支持直接编辑 JSON。
  - [ ] 提供 $/1M input tokens 和 $/1M output tokens 转 per-token JSON 的辅助按钮。
  - [ ] 默认 request types 覆盖 `chat_completion` 和 `chat_completion_stream`。
- [ ] grouped router 成本排序：
  - [ ] 成本只在同一组、同一健康桶内生效，不跨过 Healthy / Degraded / HalfOpen / FallbackOnly 顺序。
  - [ ] 价格分数使用 `(input_cost_per_token + output_cost_per_token) / weight`。
  - [ ] 只有部分 target 有价格时，已知价格 target 排在未知价格 target 前面。
  - [ ] 全组都无价格时回退 weighted random，不让 target 因缺价格不可用。
- [ ] 测试：
  - [ ] [framework/modelcatalog/overrides_test.go](../../framework/modelcatalog/overrides_test.go) 覆盖 overrides 匹配优先级、request type、无 datasheet 价格时创建有效价格。
  - [ ] [plugins/governance/routing_test.go](../../plugins/governance/routing_test.go) 覆盖同组 Healthy target 按成本排序，以及 Degraded 不能因便宜越过下一组 Healthy。
  - [ ] [ui/app/workspace/providers/fragments/pricingOverrides.test.ts](../../ui/app/workspace/providers/fragments/pricingOverrides.test.ts) 覆盖 $/1M token 转 per-token。

**验收标准**

- [ ] 用户只需要在 provider 原有价格体系里维护一次价格，cost tracking 与 routing 排序使用同一套数据。
- [ ] 价格不会改变组优先级、健康优先级或 fallback-only 的最后兜底语义。

## 12. 迁移与运维配置

- [ ] 为线上现有 `gemini-auto` 规则准备一次性迁移操作：
  - [ ] 找到预期的兜底 route group。
  - [ ] 将该 group 设置 `fallback_only=true`。
  - [ ] 迁移前备份原 routing rule JSON。
  - [ ] 迁移后通过 API 或 DB 查询确认保存成功。
  - [ ] 按 [2026-05-02-adaptive-health-routing-rollout-runbook.md](./2026-05-02-adaptive-health-routing-rollout-runbook.md) 执行上线前 PostgreSQL 迁移。
  - [ ] 只修改 `name='gemini-auto'` 且 `grouped_routing_enabled=true` 的目标规则，不批量修改其他 routing rule。
  - [ ] 验证只有最终兜底 group 出现 `fallback_only=true`，普通低成本 group 保持缺省或 `false`。
- [ ] 准备“等价旧行为”配置模板：
  - [ ] `slow_ratio_threshold = 999`
  - [ ] `slow_recovery_seconds = 0`
  - [ ] `cooldown_backoff_factor = 1.0`
  - [ ] `half_open_probe = false`
  - [ ] `request_deadline_ms = 0`
- [ ] 准备“默认推荐行为”配置模板：
  - [ ] low-cost 普通组按原顺序保留。
  - [ ] 高质量兜底组显式 `fallback_only=true`。
  - [ ] 不自动开启 active probe；如已手动开启，也只做 liveness。
  - [ ] `SlowThresholdMs=45000`，上线后按真实日志调整。
- [ ] 准备回滚说明：
  - [ ] 代码 revert 即可。
  - [ ] DB 不需要 down migration。
  - [ ] 老版本忽略 JSON 里的未知字段。

**验收标准**

- [ ] 上线前能明确哪些组是普通组，哪些组是兜底组。
- [ ] 用户可以按 rule 灰度启用或关闭新行为。
- [ ] 回滚路径不依赖手动删除 JSON 字段。

## 13. 测试矩阵

- [ ] Governance plugin 单测：
  - [ ] `(cd plugins/governance && go test ./...)`
  - [ ] `make test-governance`
- [ ] Transport schema/API 测试：
  - [ ] `(cd transports && go test ./schema_test ./bifrost-http/handlers -run 'Test.*(Adaptive|Routing|Schema)')`
  - [ ] 如 handler 依赖较多，先跑相关测试，再在最终验证跑 `(cd transports && go test ./...)`。
- [ ] UI 单测：
  - [ ] `(cd ui && npx vitest run app/workspace/adaptive-routing/healthDetectionConfig.test.ts app/workspace/adaptive-routing/healthDetectionTargets.test.ts app/workspace/routing-rules/views/routeGroupState.test.ts)`
  - [ ] `(cd ui && npx vitest run app/workspace/providers/dialogs/providerConfigTabs.test.ts app/workspace/providers/fragments/pricingOverrides.test.ts)`
  - [ ] 如果新增 sheet 测试，同步加入命令。
- [ ] Model catalog 单测：
  - [ ] `(cd framework && go test ./modelcatalog -count=1 -timeout 180s)`
- [ ] 格式与静态检查：
  - [ ] `make fmt`
  - [ ] `make lint`
  - [ ] `(cd ui && npm run lint)`
- [ ] 行为级手工验证：
  - [ ] 构造 fast success，确认 target healthy。
  - [ ] 构造 6/10 slow success，确认 target degraded，后续普通 healthy 组被优先选中。
  - [ ] 构造 429 saturated，确认 soft fail cooldown 时长带 multiplier。
  - [ ] 构造 auth/quota hard fail，确认 hard fail cooldown。
  - [ ] cooldown 到期后并发请求只放行一个 half-open。
  - [ ] fallback-only 组只在普通组不可用时被选为 primary。
  - [ ] active probe disabled 时没有后台探测请求。
  - [ ] active probe enabled 时，probe 成功/失败只更新 liveness 字段，不改变主健康状态。
  - [ ] pricing overrides 填入不同价格后，同组同健康桶内按价格顺序选。
  - [ ] 运行本地黑盒仿真：`MOCK_PORT=19111 BIFROST_PORT=18081 tests/manual/grouped-routing-lab/run_lab.sh`。

**验收标准**

- [ ] 新增单测覆盖设计文档 §6 的核心用例。
- [ ] 至少一条手工链路证明“低成本快则优先，低成本慢则切走，普通链路都失败时当前请求走兜底”。
- [ ] 没有测试只验证字段存在而不验证路由行为。

## 14. 上线灰度步骤

- [ ] Day 0，上线前：
  - [ ] 备份线上 routing rule 配置。
  - [ ] 给现有兜底组补 `fallback_only=true`。
  - [ ] 确认 active probe 仍是 disabled。
- [ ] Day 1，只观测 latency：
  - [ ] 代码上线。
  - [ ] 对需要保守的 rule 设置 `slow_ratio_threshold=999`，暂时不触发 degraded。
  - [ ] 观察 health snapshot 是否正确记录 latency、slow sample、failure type。
  - [ ] 观察日志中 selected primary/fallback 是否仍与旧行为一致。
- [ ] Day 2，启用三态：
  - [ ] 恢复默认 `slow_ratio_threshold=0.5`。
  - [ ] 确认慢目标进入 degraded 后，后续普通 healthy 组被优先选中。
  - [ ] 确认 fallback-only 组没有被提前使用。
- [ ] Day 3，可选 active probe：
  - [ ] 仅在需要时手动开启 active probe。
  - [ ] 先只启用少量 target。
  - [ ] 观察探测流量和 last probe 状态；不要期待它改变 routing health / cooldown。

**验收标准**

- [ ] 灰度可以按单条 routing rule 控制。
- [ ] active probe 不作为本次上线的默认行为。
- [ ] 出现异常时能快速切回等价旧行为配置。

## 15. 文档更新

- [ ] 更新内部设计文档中若实现细节变化的部分，特别是：
  - [ ] 实际 API JSON status 值。
  - [ ] 最终 latency 指标使用 p95。
  - [ ] active probe 默认/推荐值。
  - [ ] fallback-only 迁移说明。
- [ ] 如仓库已有用户文档或 Mintlify 页面介绍 adaptive routing，补充：
  - [ ] Healthy / Degraded / Cooldown 含义。
  - [ ] Fallback only route group 的用途。
  - [ ] 如何关闭新行为回到旧行为。
  - [ ] active probe 默认关闭。
- [ ] 新增或更新一份测试计划文档：
  - [ ] 参考 [2026-04-14-hybrid-health-probing-test-plan.md](./2026-04-14-hybrid-health-probing-test-plan.md)。
  - [ ] 覆盖三态路由、退避、half-open、fallback-only、UI 显示、灰度回滚。

**验收标准**

- [ ] 用户能从文档看懂为什么慢成功会降低后续优先级。
- [ ] 文档不再暗示当前版本能中断 100 秒慢请求。
- [ ] 文档和 UI 字段默认值一致。

---

## 自审对齐表

| 设计要求 | 覆盖任务 | 自审结论 |
|---|---:|---|
| 原地升级 grouped routing，不另起一套路由系统 | §0, §6, §7 | 已覆盖，所有任务围绕现有 `plugins/governance` 路径展开 |
| schema 兼容但行为统一升级 | §2, §3, §12 | 已覆盖，新增字段为 JSON 追加，旧行为靠 policy 覆盖 |
| `HalfOpenProbe` 必须区分未填和显式 false | §2, §5, §10 | 已覆盖，要求 `*bool` 和测试显式 false |
| `SlowRecoverySeconds` 必须区分未填和显式 0 | §0.1, §2, §10, §12 | 已覆盖，推荐 `*int`，nil 默认 60，显式 0 关闭 |
| `slow_ratio_threshold=999` 不能被校验挡住 | §0.1, §3, §10, §12, §14 | 已覆盖，校验改为 `>0`，大于 1 表示关闭 ratio degraded |
| `consecutive_failures=0` 兼容旧语义 | §0.1, §2, §3, §10 | 已覆盖，0 保留为 fallback 到 failure_threshold |
| HealthSnapshot API 向后兼容 | §0.1, §5, §9, §10, §11 | 已覆盖，保留 legacy `status`，新增 `health_level` |
| 不实现当前请求硬 deadline | §0, §7, §15 | 已覆盖，明确 `request_deadline_ms=0` 且不设置 context deadline |
| 慢成功要影响后续路由 | §4, §5, §7, §13 | 已覆盖，通过 `OutcomeSlow` 和 slow ratio 测试锁定 |
| 软失败/硬失败分类 | §4, §5, §13 | 已覆盖，分类器和 cooldown multiplier 测试都有任务 |
| 三态健康 Healthy/Degraded/Cooldown | §5, §6, §9, §11 | 已覆盖，后端状态机、API、UI 都有任务 |
| 指数退避和最大 cooldown | §2, §5, §13 | 已覆盖，含 `cooldown_backoff_factor` 与封顶测试 |
| half-open 恢复且只允许一个 in-flight | §5, §6, §13 | 已覆盖，含并发测试 |
| `FallbackOnly` 不再靠最后一组硬编码 | §2, §3, §6, §8, §10, §12 | 已覆盖，配置、schema、router、UI、迁移都有任务 |
| active probe 默认不打开 | §0, §8, §14 | 已覆盖，默认关闭和灰度手动开启均明确 |
| active probe 跳过兜底层 | §8, §13 | 已覆盖，含测试 |
| active probe 只做 liveness，不参与路由健康评分 | §0, §8, §9, §11, §13, §14 | 已覆盖，probe result 与 `RecordOutcome` 分离 |
| 成本排序复用 provider pricing overrides | §0, §11.5, §13 | 已覆盖，不新建第二套价格系统 |
| UI 可配置新字段并展示新状态 | §10, §11 | 已覆盖，表单和状态页拆分处理 |
| 测试计划覆盖核心风险 | §13, §15 | 已覆盖，单测、schema/API、UI、手工验证均列出 |
| 上线可灰度、可回滚 | §12, §14 | 已覆盖，含等价旧行为模板和 rollback 路径 |

## 最小可评审切片

如果实现时需要拆 PR 或拆 review，建议按下面顺序切，但代码仍可一次性落地：

- [ ] Slice 1：配置结构、schema、默认值、UI 类型，不改变运行时路由行为。
- [ ] Slice 2：Outcome 分类器和 HealthTracker 三态状态机，单测先行。
- [ ] Slice 3：Grouped router 选择规则和 `FallbackOnly`，路由器单测先行。
- [ ] Slice 4：PreLLMHook/PostLLMHook 接入真实 latency 与 outcome。
- [ ] Slice 5：Active probe 保持默认关闭，并与主健康状态机解耦为 liveness-only。
- [ ] Slice 6：HTTP health status 和 UI 展示。
- [ ] Slice 7：Provider pricing overrides 与同桶成本排序。
- [ ] Slice 8：上线灰度配置、测试计划、用户文档。

## 完成定义

- [ ] 所有新增字段可从 config、API、UI 往返保存。
- [ ] §0.1 的 5 个消歧点都有对应实现和测试覆盖。
- [ ] 健康状态机能稳定输出 healthy/degraded/cooldown。
- [ ] 路由器满足“普通 healthy 优先、普通 degraded 次之、fallback-only 最后”的顺序。
- [ ] 慢成功不会立即中断当前请求，但会影响后续请求优先级。
- [ ] active probe 默认关闭，开启后不探测 fallback-only 组，且不改变主健康评分。
- [ ] provider pricing overrides 能被 cost tracking 和 grouped routing 共同使用。
- [ ] 关键 Go 测试、schema/API 测试、UI 测试通过。
- [ ] 文档明确当前版本不解决单次 100 秒慢请求的硬中断问题。
