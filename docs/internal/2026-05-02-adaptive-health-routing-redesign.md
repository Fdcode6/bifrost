# 自适应健康路由改造设计（Adaptive Health Routing Redesign）

- 文档日期：2026-05-02
- 涉及模块：`plugins/governance`
- 关联前作：
  - [2026-04-13-grouped-health-routing-requirements.md](./2026-04-13-grouped-health-routing-requirements.md)
  - [2026-04-14-hybrid-health-probing-test-plan.md](./2026-04-14-hybrid-health-probing-test-plan.md)

---

## 0. TL;DR

把 grouped routing 的健康判定从「**只看是否硬失败**」升级为「**端到端 latency + 错误分类 + 三态健康 + 指数退避 + 半开恢复**」，并把"健康"的优先级**置于成本之上**（同等健康档位内仍便宜优先）。

成本排序不新建第二套价格系统，直接复用 provider 的 `pricing_overrides` / ModelCatalog 生效价格；active probe 不参与主健康评分，只作为手动开启后的 liveness（死活）检测。

> 当前版本**不承诺单 attempt 到点后立即 fallback**。在不重构 core/provider 请求生命周期的前提下，先实现"慢成功会被记录并影响后续路由"：本次慢请求仍可能等到 provider 返回，但后续请求会避开已 Degraded 的 target。真正的 per-attempt deadline + 快速 fallback 列入后续 core 改造（见 §3.4）。

**兼容性**：schema 完全向后兼容（旧规则不需要修改即可写入/读取）；行为上则统一升级——所有 grouped routing 规则启用新判定。如需保持旧行为，需在 policy 字段层显式回退（见 §10.2）。

**影响范围**：核心改动集中在 `plugins/governance/`，但配套必然涉及 `framework/configstore/tables`、`transports/bifrost-http/handlers`、`transports/config.schema.json` 与 `ui/`。完整清单见 §8。

不引入综合评分系统，不增加新外部依赖。

---

## 1. 背景与现状

### 1.1 当前规则示例（用户线上 `gemini-auto`）

```
1. poloapi      / gemini-3.1-pro-preview          ← 最便宜，主力
2. 4sapi        / gemini-3.1-pro-preview-medium   ← 中等
3. openrouter   / gemma-4-31b-it                  ← 兜底（效果较差）
```

按组顺序固定优先级 fallback，每组单 target，weight 形同虚设。

### 1.2 关键代码位置

| 关注点 | 文件 |
|---|---|
| 健康状态结构 + cooldown 评估 | [plugins/governance/health_tracker.go](../../plugins/governance/health_tracker.go) |
| grouped routing primary/fallback 选择 | [plugins/governance/grouped_router.go](../../plugins/governance/grouped_router.go) |
| 失败/成功记录入口 | [plugins/governance/main.go#L1142](../../plugins/governance/main.go) (`PostLLMHook`) |
| Active probe 执行循环 | [plugins/governance/active_probe.go](../../plugins/governance/active_probe.go) |
| 请求执行 + ctx 取消语义 | [core/providers/utils/utils.go#L113-L160](../../core/providers/utils/utils.go) (`MakeRequestWithContext`) |
| BifrostContext 取消机制 | [core/schemas/context.go](../../core/schemas/context.go) |
| `HealthPolicy` 持久化定义 | [framework/configstore/tables/routing_rules.go#L13](../../framework/configstore/tables/routing_rules.go) |

### 1.3 现状的根因问题

1. **慢成功 = 成功，且会清空失败状态**
   - `RecordSuccess` → `resetCooldownLocked` 把 `failures`、`consecutiveFailures` 全清零（`health_tracker.go` L172-L184，L431-L437）。
   - `PostLLMHook` 仅按 `err != nil` 分支，不计算 latency（`main.go` L1170-L1181）。
2. **provider 侧 `default_request_timeout_in_seconds` 是 read/write 超时**
   - 只能拦"一个字都不回"的请求；流式响应只要每帧 < timeout 就不会触发，因此用户感知 100s+ 的请求会被记成成功。
3. **错误未分类**
   - 5xx / 网络错误 / 429 quota / "upstream saturated" 一视同仁，cooldown 时长固定。
4. **健康只有二态**（available / cooldown）
   - 没有"在用但已劣化"的中间档，要么完全打它要么完全跳过。
5. **fallback target 长期没流量，无健康信号**
   - 备用模型一旦默默坏掉，主线挂的瞬间才暴露。
6. **历史版本 active probe 默认关闭，且 stream-only 目标会被判定为 unsupported**
   - 本方案仍默认关闭 active probe；手动开启后，stream-only 目标改用普通 ChatCompletion 做低成本 liveness 探测。
   - 探测结果只更新 probe 状态，不参与 slow ratio / cooldown / 路由健康评分。

---

## 2. 设计目标

| 编号 | 目标 | 衡量 |
|---|---|---|
| G1 | 慢请求可观测、可降级 | 当前版本不做强制中断；成功但 latency ≥ 45s 计入 `slow_success`，使 target 进入 Degraded，后续请求自动让位给更健康 target。真正"单 attempt 到点后立即 fallback"需要后续 per-attempt context 改造 |
| G2 | "慢成功"被识别并影响路由 | 成功但 latency ≥ 45s 计入 `slow_success`；最近 N 次中 slow 比例 ≥ 阈值 → 进入 Degraded 状态 |
| G3 | 软失败（429 / quota / saturated）不被错误地短 cooldown 反复试 | 错误分类后差异化 cooldown，连续触发指数退避 |
| G4 | 健康优先于成本（A 方案） | 同健康档位内按组序与成本排序；前一组所有候选 Degraded 时，让位给后一组 Healthy 候选 |
| G5 | 兜底层（openrouter）不抢主路由，但必须作为当前请求最后兜底 | 永不主动探测，永不影子流量；普通链路都失败时同一请求立刻尝试兜底，普通目标全 Cooldown 时兜底可晋升 primary |
| G6 | 恢复期不立即满血 | half-open：cooldown 结束先放 1 个探针请求 |
| G7 | UI 可观测 | 显示 p95 latency、slow ratio、cooldown 次数、原因 |
| G8 | Schema 向后兼容 | `HealthPolicy` 老字段语义不变，新字段全部可选；旧规则不修改即可继续读写。**注意**：行为上统一升级，如需保留旧二态行为需显式覆盖 policy（详见 §10.2） |
| G9 | 价格配置不双轨 | 复用 provider `pricing_overrides`，UI 仅提供 $/1M token 转 per-token JSON 的辅助输入 |

---

## 3. 改造范围（一次性全做）

### 3.1 数据结构变更

#### 3.1.1 `HealthPolicy` 扩展（[framework/configstore/tables/routing_rules.go](../../framework/configstore/tables/routing_rules.go)）

```go
type HealthPolicy struct {
    // ─────── 现有字段（保持不变）───────
    FailureThreshold     int `json:"failure_threshold"`      // 默认 2
    FailureWindowSeconds int `json:"failure_window_seconds"` // 默认 30
    CooldownSeconds      int `json:"cooldown_seconds"`       // 默认 30
    // 默认 0；运行时语义：0 = 使用 FailureThreshold，不要在默认值填充时改成 FailureThreshold
    ConsecutiveFailures  int `json:"consecutive_failures"`

    // ─────── 新增字段（全部可选）───────

    // Latency / Slow-success 相关
    SlowThresholdMs    int     `json:"slow_threshold_ms,omitempty"`    // 默认 45000；超过即记 slow_success
    SlowWindowSize     int     `json:"slow_window_size,omitempty"`     // 默认 10；最近 N 次的滑动窗口
    SlowRatioThreshold float64 `json:"slow_ratio_threshold,omitempty"` // 默认 0.5；slow/window ≥ 该值进入 Degraded
    // nil = 默认 60；显式 0 = 关闭 last-slow recency 判定
    SlowRecoverySeconds *int   `json:"slow_recovery_seconds,omitempty"`// 最近一次 slow 距今 ≤ 该值仍视作 Degraded

    // 端到端 deadline（预留字段，当前版本默认禁用）
    // 0 = 禁用；真正按 attempt 快速 fallback 需要后续 core/provider 请求生命周期改造（见 §3.4）
    RequestDeadlineMs int `json:"request_deadline_ms,omitempty"`

    // 软失败差异化
    SoftCooldownMultiplier float64 `json:"soft_cooldown_multiplier,omitempty"` // 默认 2.0；soft 失败 cooldown = base × 该值

    // 指数退避
    CooldownBackoffFactor float64 `json:"cooldown_backoff_factor,omitempty"` // 默认 2.0
    CooldownMaxSeconds    int     `json:"cooldown_max_seconds,omitempty"`    // 默认 600

    // 半开恢复（指针类型，区分"未设置"和"显式关闭"；nil → 默认 true）
    HalfOpenProbe *bool `json:"half_open_probe,omitempty"`
}
```

#### 3.1.1.1 `RouteGroup` 新增 `FallbackOnly` 字段（兜底层显式标记）

原方案打算用「最后一组 = 兜底层」的硬编码识别，Codex 评审指出该假设脆弱（未来若增加更多 group 或顺序变化会失效）。改为给 `RouteGroup` 加一个**显式开关**：

```go
// framework/configstore/tables/routing_rules.go
type RouteGroup struct {
    Name         string             `json:"name"`
    RetryLimit   int                `json:"retry_limit"` // 普通组：补选其它 target；FallbackOnly 组：允许重复 target 消耗重试预算
    Targets      []RouteGroupTarget `json:"targets"`
    FallbackOnly bool               `json:"fallback_only,omitempty"` // 新增：true = 不抢普通组 primary；追加到当前请求 fallback 链末尾；普通组全不可用时可作为 primary；不参与 active probe
}
```

#### 3.1.2 `TargetHealthState` 扩展（[plugins/governance/health_tracker.go](../../plugins/governance/health_tracker.go)）

```go
type TargetHealthState struct {
    mu sync.Mutex

    // 现有
    failures            []time.Time
    consecutiveFailures int
    cooldownUntil       time.Time
    lastFailureTime     time.Time
    lastFailureMsg      string

    // 新增 ─────────────────────────────

    // 最近 N 次请求的 latency 环形缓冲，用于 p95 / slow ratio 计算
    recentSamples []latencySample // 长度上限 = SlowWindowSize（取 max(20, policy.SlowWindowSize)）

    // 慢成功专用
    slowCount  int       // recentSamples 中的 slow 计数（实时维护，O(1)）
    lastSlowAt time.Time

    // 错误分类计数（仅近窗口）
    softFailCount int
    hardFailCount int

    // 退避
    cooldownStreak int       // 连续触发 cooldown 次数；成功探针后清零

    // 半开
    halfOpenInFlight bool      // 已派出探针但未回执；防止并发多个探针
    halfOpenStartedAt time.Time
}

type latencySample struct {
    at      time.Time
    latency time.Duration
    kind    OutcomeKind // success / slow / softFail / hardFail
}

type OutcomeKind int
const (
    OutcomeSuccess OutcomeKind = iota
    OutcomeSlow
    OutcomeSoftFail
    OutcomeHardFail
)
```

#### 3.1.3 三态健康枚举（新增 [plugins/governance/health_tracker.go](../../plugins/governance/health_tracker.go)）

```go
type HealthLevel int
const (
    HealthHealthy  HealthLevel = iota
    HealthDegraded
    HealthCooldown
)

func (ht *HealthTracker) GetTargetHealth(stateKey string, policy *configstoreTables.HealthPolicy, now time.Time) HealthLevel
```

`IsInCooldown` / `IsInCooldownForRule` 保留为 `GetTargetHealth(...) == HealthCooldown` 的便捷方法，向后兼容。

### 3.2 `OutcomeKind` 分类规则（新文件 `plugins/governance/outcome_classifier.go`）

```go
func ClassifyOutcome(err *schemas.BifrostError, latency time.Duration, slowThreshold time.Duration) OutcomeKind {
    if err == nil {
        if latency >= slowThreshold {
            return OutcomeSlow
        }
        return OutcomeSuccess
    }

    // 1. 现有 provider timeout / 未来 per-attempt deadline 触发 abort → SoftFail（不计入连续硬失败）
    if isDeadlineAbort(err) {
        return OutcomeSoftFail
    }

    // 2. HTTP 状态码
    if err.StatusCode != nil {
        switch *err.StatusCode {
        case 429:
            return OutcomeSoftFail
        case 408, 502, 503, 504:
            return OutcomeSoftFail
        }
        if *err.StatusCode >= 500 {
            return OutcomeHardFail
        }
        if *err.StatusCode >= 400 {
            // 4xx（除 429/408）通常是用户输入问题，不计入健康
            return OutcomeSuccess  // 等同"不污染健康"
        }
    }

    // 3. 错误消息关键字（小写匹配）
    msg := strings.ToLower(errorMessage(err))
    softKeywords := []string{
        "saturated", "no capacity", "quota", "rate limit", "too many",
        "overloaded", "upstream busy", "model is currently overloaded",
        "context_length_exceeded",  // 该走 fallback，但不算坏
    }
    for _, k := range softKeywords {
        if strings.Contains(msg, k) {
            return OutcomeSoftFail
        }
    }

    // 4. 默认按硬失败
    return OutcomeHardFail
}
```

### 3.3 路由器：三态选择逻辑（重写 [plugins/governance/grouped_router.go](../../plugins/governance/grouped_router.go) 的 `buildGroupedRoutingDecision`）

#### 3.3.1 选择算法

```
输入：rule.ParsedRouteGroups（按优先级）
输出：primary + fallback chain

伪代码：

healthy   = []   # (groupIdx, target)
degraded  = []
half_open = []
fallback_only = []
seen      = set()

# 第一步：按组顺序扫描，分桶
for gi, group in enumerate(groups):
    is_fallback_only = group.FallbackOnly   # 显式字段，不再依赖位置
    for t in group.targets:
        rt = resolve(t)
        if rt.key in seen: continue
        level = tracker.GetTargetHealth(...)
        if level == Cooldown:
            if not tracker.shouldHalfOpenProbe(rt):  # 半开探针窗口
                continue
            # 半开：允许 1 个探针请求
            mark_as_half_open(rt)
        if is_fallback_only:
            # 兜底层：不参与 healthy/degraded 分桶，最后追加到同一个请求的 fallback 链
            fallback_only.append((gi, rt))
            continue
        if level == Healthy:
            healthy.append((gi, rt))
        elif level == Degraded:
            degraded.append((gi, rt))
        else:  # Cooldown 但被半开放出
            half_open.append((gi, rt))

# 第二步：按"健康优先于成本，但同档位仍按组顺序"组装；
# fallback_only 永远在当前请求 fallback 链末尾。若普通链为空，它自然成为 primary。
# fallback_only 可以按 1 + retry_limit 重复同一个 target；普通组仍不重复同一 target。
ordered = sort(healthy, key=groupIdx) + sort(degraded, key=groupIdx) + sort(half_open, key=groupIdx) + expand_fallback_only_with_retries(fallback_only)
chain = dedup(ordered)

primary = chain[0]
fallbacks = chain[1:]
```

#### 3.3.2 关键决策表

约定：组 1、组 2 是 `FallbackOnly=false` 的常规组（按成本排序）；兜底是 `FallbackOnly=true` 的组。

| 第 1 组 | 第 2 组 | 兜底（FallbackOnly=true） | primary 选择 |
|---|---|---|---|
| Healthy | Healthy | 任意 | 第 1 组 Healthy（便宜优先，同档位按组序） |
| **Degraded** | **Healthy** | 任意 | **第 2 组 Healthy**（健康优先于成本，目标 G4） |
| Degraded | Degraded | 任意 | 第 1 组 Degraded（同档位仍按组序） |
| Cooldown | Healthy | 任意 | 第 2 组 Healthy |
| Cooldown | Cooldown | Healthy | 兜底组（普通链为空，此时晋升 primary） |
| Cooldown(半开) | Healthy | 任意 | 第 2 组 Healthy（探针沉到 fallback 末尾） |

> **目标 G5**：`FallbackOnly=true` 的组永远不参与 healthy/degraded 让位逻辑，也永远不被 active probe 主动探测。它会追加到当前请求 fallback 链末尾，所以前面的普通目标在同一请求内都失败时会立刻尝试兜底；只有所有非 FallbackOnly 组都不可路由时才会充当 primary。

> **兜底重试语义**：普通组的 `retry_limit` 仍然是不重复同一 target、只补选同组其它 target；`FallbackOnly=true` 组允许重复已选 target 来消耗 `1 + retry_limit` 次机会。这样线上单个 `openrouter/gemma-4-31b-it` 兜底组设置 `retry_limit=2` 时，会在普通链路失败后最多尝试 3 次兜底。

#### 3.3.3 半开探针策略

`shouldHalfOpenProbe` 的实现：

```go
func (ht *HealthTracker) shouldHalfOpenProbe(stateKey string, policy *HealthPolicy, now time.Time) bool {
    if policy.HalfOpenProbe != nil && !*policy.HalfOpenProbe { return false } // 显式关闭
    // nil 视为默认开启，由 ApplyHealthPolicyDefaults 兜底；这里再做一次保护
    s := ht.targets[stateKey]
    s.mu.Lock(); defer s.mu.Unlock()
    if s.cooldownUntil.IsZero() { return false }              // 不在 cooldown
    if now.Before(s.cooldownUntil) { return false }           // cooldown 还没结束
    if s.halfOpenInFlight && now.Sub(s.halfOpenStartedAt) < 30*time.Second {
        return false                                          // 已有探针在路上
    }
    s.halfOpenInFlight = true
    s.halfOpenStartedAt = now
    return true
}
```

`PostLLMHook` 处理 half-open 结果：
- 探针 outcome == Success 且非 Slow → `cooldownStreak = 0`，完全恢复（清空 failures、cooldownUntil、halfOpenInFlight）。
- 探针 outcome == Slow / SoftFail / HardFail → `cooldownStreak++`，cooldown = `min(base × backoff^streak × softMult, max)`，重新进入 Cooldown。

#### 3.3.4 成本排序与 provider pricing overrides 复用

成本只在**同一组、同一健康桶**内参与排序，不会跨过健康优先级：

1. 路由器先按组顺序和健康状态分桶：`Healthy → Degraded → HalfOpen → FallbackOnly`。
2. 每个组内选 target 时，如果能解析到有效价格，按 `score = (input_cost_per_token + output_cost_per_token) / weight` 升序选；`weight <= 0` 的已知价格目标在已知价格目标里排到较低优先级，但仍早于完全未知价格的目标。
3. 如果只有部分 target 有价格，已知价格目标优先；没有价格的目标仍保留可路由资格，按原始顺序排在同桶后面。
4. 如果整个组都没有价格，回退到原来的 weighted random 选择，不因为缺价格导致不可用。

价格来源统一走现有 ModelCatalog：

```go
pricing := modelCatalog.GetEffectivePricingEntry(model, provider, requestType)
cost := pricing.InputCostPerToken + pricing.OutputCostPerToken
```

provider 的 `pricing_overrides` 会覆盖 datasheet 价格，也能给线上自定义模型名补价格。后台 UI 允许用户按 **$/1M input tokens** 和 **$/1M output tokens** 填写，保存时转换为 JSON 里的 per-token 字段；不新增 routing 专用价格表，避免双轨配置。

### 3.4 端到端 Deadline（后续改造，不纳入当前版本）

#### 3.4.1 关键约束（来自 Codex 复评）

经核查代码后确认，**不要在当前版本用 watchdog 调 `ctx.Cancel()` 来实现 deadline**，原因有三点：

1. `BifrostContext` 没有 `SetDeadline`，deadline 是 `NewBifrostContext` 创建时一次性设置（[core/schemas/context.go#L46-L66](../../core/schemas/context.go)），`PreLLMHook` 阶段只能写 value，无法变更 deadline。
2. `BifrostContext.Cancel()` 固定设置 `context.Canceled`，不是 `context.DeadlineExceeded`（[core/schemas/context.go#L113-L116](../../core/schemas/context.go)）。这会被 core 识别成 `RequestCancelled`，而 `shouldTryFallbacks` 遇到 `RequestCancelled` 会直接停止 fallback（[core/bifrost.go#L4092-L4104](../../core/bifrost.go)）。
3. fallback loop 复用同一个 `ctx`。一旦 watchdog 把共享 ctx cancel，后续 fallback attempt 会立即命中 `ctx.Done()`，无法正常排队或等待 provider 响应（[core/bifrost.go#L4292-L4318](../../core/bifrost.go)、[core/bifrost.go#L4564-L4671](../../core/bifrost.go)）。

此外，`MakeRequestWithContext` 的注释明确说：「**does NOT truly cancel the underlying fasthttp network request**」（[core/providers/utils/utils.go#L113-L121](../../core/providers/utils/utils.go)）。provider 侧调用普遍 `defer wait()`，因此即便内部提前得到 ctx done，非流式 provider 函数也可能仍等到底层 `client.Do` 完成后才真正返回（例如 [core/providers/openai/openai.go#L158-L160](../../core/providers/openai/openai.go)）。

#### 3.4.2 当前版本的取舍

当前版本不实现 `RequestDeadlineMs` 的强制中断语义：

- `RequestDeadlineMs` 作为预留字段保留在 `HealthPolicy` 中，默认值为 `0`（禁用）。
- `PreLLMHook` 只记录 `groupedRoutingAttemptStartContextKey = time.Now()`，用于 `PostLLMHook` 计算真实 latency。
- 成功但耗时超过 `SlowThresholdMs` 的请求记录为 `OutcomeSlow`，影响后续路由排序。
- 如果请求最终由现有 provider read/write timeout 产生错误，`ClassifyOutcome` 再按 timeout / soft fail / hard fail 记录健康状态。

这意味着：**当前慢请求本身不一定被提前切走，但后续请求会避开已 Degraded 的 target。** 这是在不改 core/provider 生命周期前提下最小、最稳的落地点。

#### 3.4.3 如果后续要做"单 attempt 到点立即 fallback"

需要单独做 core/provider 级改造，不能只靠 governance plugin：

1. 在 core fallback loop 中为每个 attempt 派生独立 context，例如 `attemptCtx := schemas.NewBifrostContext(parentCtx, time.Now().Add(deadline))`，不能 cancel 共享请求 ctx。
2. `tryRequest` / `tryStreamRequest` 使用 attemptCtx 等待当前 provider，但 fallback loop 保留未取消的 parent ctx 继续下一跳。
3. provider 请求生命周期要重构 `MakeRequestWithContext` 的 `wait()` / req / resp 释放策略，避免函数返回仍被底层 fasthttp goroutine 卡住。
4. 对流式请求要明确区分 TTFT deadline、整体 stream duration deadline、client disconnect 三种语义。

该改造风险和范围都超出当前 `plugins/governance` 健康路由增强，建议另开设计文档处理。

### 3.5 失败/成功记录入口统一（重构 `PostLLMHook`）

新增统一入口 `RecordOutcome`：

```go
// plugins/governance/health_tracker.go
func (ht *HealthTracker) RecordOutcome(
    ruleID, targetKey string,
    kind OutcomeKind,
    latency time.Duration,
    failureMsg string,
    policy *configstoreTables.HealthPolicy,
    now time.Time,
)
```

`main.go PostLLMHook` 改造：

```go
// 替换原 RecordFailureForRule / RecordSuccessForRule 调用
attemptStart, _ := ctx.Value(groupedRoutingAttemptStartContextKey).(time.Time)
latency := time.Duration(0)
if !attemptStart.IsZero() {
    latency = time.Since(attemptStart)
}
slowThreshold := time.Duration(policy.SlowThresholdMs) * time.Millisecond
kind := ClassifyOutcome(err, latency, slowThreshold)

failureMsg := ""
if err != nil && err.Error != nil {
    failureMsg = err.Error.Message
}

p.healthTracker.RecordOutcome(ruleID, targetKey, kind, latency, failureMsg, policy, time.Now())
```

`groupedRoutingAttemptStartContextKey` 必须在普通请求路径和 `governLargePayload` 路径的每次 `PreLLMHook` attempt 中写入；fallback attempt 进入 `PreLLMHook` 时也要刷新，保证 latency 归属到真实 attempt，而不是整条请求链。

`RecordOutcome` 内部按 kind 分发：

| Kind | failures 计数 | consecutiveFailures | recentSamples | cooldown 触发 |
|---|---|---|---|---|
| Success | 不计 | reset | append（kind=success） | 探针成功时清零 streak |
| Slow | 不计 | reset | append（kind=slow） | 不直接 cooldown，仅参与 Degraded 判定 |
| SoftFail | 计入 | +1 | append | 触发后 cooldown = base × softMult × backoff^streak |
| HardFail | 计入 | +1 | append | 触发后 cooldown = base × backoff^streak |

### 3.6 三态判定算法（`GetTargetHealth`）

```go
func (ht *HealthTracker) GetTargetHealth(stateKey, policy, now) HealthLevel {
    s := ht.targets[stateKey]
    if s == nil { return HealthHealthy }

    // 1. cooldown 优先
    if evaluateCooldownLocked(s, policy, now) {
        return HealthCooldown
    }

    // 2. slow ratio 判定
    threshold := time.Duration(policy.SlowThresholdMs) * time.Millisecond
    windowSize := policy.SlowWindowSize
    if windowSize <= 0 { windowSize = 10 }
    ratioThreshold := policy.SlowRatioThreshold
    if ratioThreshold <= 0 { ratioThreshold = 0.5 }

    if len(s.recentSamples) >= windowSize/2 {  // 至少有半个窗口的样本
        slowInWindow := 0
        considered := 0
        for i := len(s.recentSamples) - 1; i >= 0 && considered < windowSize; i-- {
            considered++
            if s.recentSamples[i].kind == OutcomeSlow {
                slowInWindow++
            }
        }
        if float64(slowInWindow)/float64(considered) >= ratioThreshold {
            return HealthDegraded
        }
    }

    // 3. lastSlowAt recency
    recoverySeconds := 60
    if policy.SlowRecoverySeconds != nil {
        recoverySeconds = *policy.SlowRecoverySeconds
    }
    recovery := time.Duration(recoverySeconds) * time.Second
    if recovery > 0 && !s.lastSlowAt.IsZero() && now.Sub(s.lastSlowAt) < recovery {
        return HealthDegraded
    }

    return HealthHealthy
}
```

### 3.7 Active Probe 调整（[plugins/governance/active_probe.go](../../plugins/governance/active_probe.go)）

| 项 | 现状 | 改后 |
|---|---|---|
| 默认开关 | `Enabled: false` | **保持 `false` 不动**（Codex 评审建议保守，避免一次性扩大行为面）。文档 §10.3.3 标注"建议手动启用"。需要时由用户在 governance 全局设置勾选 |
| Interval 默认 | 15s | 60s（保持调整，仅影响**手动启用后**的行为） |
| IdlePause 默认 | 30 分钟 | 5 分钟（同上） |
| Timeout 默认 | 5s | 5s（不变） |
| 兜底层处理 | 与其他 target 等同 | **跳过 `FallbackOnly=true` 的组**（不再依赖位置；在 `buildActiveProbePlans` 里识别 `group.FallbackOnly`） |
| stream-only 模型 | unsupported 直接跳过 | **改用 ChatCompletion 类型探针请求**（max_tokens=1, temperature=0），无视真实业务请求类型 |
| 探测结果写入 | 容易被理解为与真实流量同状态机 | **只调用 `RecordProbeResult`**。成功/失败仅更新 last probe 状态，不清除 cooldown、不触发 cooldown、不影响 slow ratio / p95 / routing health |

Interval / IdlePause 继续映射到 governance 全局设置里的现有字段：`ActiveHealthProbeIntervalSeconds`、`ActiveHealthProbeIdlePauseMinutes`、`ActiveHealthProbePassiveFreshnessSeconds`，不新增新的全局配置字段。

active probe 的定位降级为“这个 key/model 是否彻底不可达”的辅助信号。真实业务请求和 half-open 请求才是路由健康状态的权威来源。

### 3.8 UI 展示（[ui/app/workspace/adaptive-routing](../../ui/app/workspace/adaptive-routing)）

`HealthSnapshot` API 新增字段：

```typescript
interface HealthSnapshot {
  // 现有
  status: "available" | "cooldown";
  failure_count: number;
  consecutive_failures: number;
  cooldown_until?: string;
  last_failure_msg: string;

  // 新增
  health_level: "healthy" | "degraded" | "cooldown";
  p95_latency_ms?: number;
  slow_count?: number;
  slow_ratio?: number;
  cooldown_streak?: number;
  last_outcome_kind?: "success" | "slow" | "soft_fail" | "hard_fail";
  half_open_in_flight?: boolean;
}
```

UI Badge 颜色：
- `healthy` → 绿
- `degraded` → 黄（同时显示 `slow_ratio` 与 `p95`）
- `cooldown` → 红（显示倒计时 + cooldown_streak）

---

## 4. 配置 Schema 变更

### 4.1 `transports/config.schema.json`

`HealthPolicyRequest` 与 `HealthPolicy` 都需扩展，新字段 **全部 optional**，类型与 §3.1.1 一致。

### 4.2 默认值统一

新建 `plugins/governance/health_tracker.go::ApplyHealthPolicyDefaults()`：

```go
func ApplyHealthPolicyDefaults(p *HealthPolicy) {
    if p.FailureThreshold == 0 { p.FailureThreshold = 2 }
    if p.FailureWindowSeconds == 0 { p.FailureWindowSeconds = 30 }
    if p.CooldownSeconds == 0 { p.CooldownSeconds = 30 }
    // ConsecutiveFailures: 0 保留显式语义，运行时解释为使用 FailureThreshold，不填默认
    // 新字段
    if p.SlowThresholdMs == 0 { p.SlowThresholdMs = 45000 }
    if p.SlowWindowSize == 0 { p.SlowWindowSize = 10 }
    if p.SlowRatioThreshold == 0 { p.SlowRatioThreshold = 0.5 }
    if p.SlowRecoverySeconds == nil {
        v := 60
        p.SlowRecoverySeconds = &v
    }
    // RequestDeadlineMs 当前版本默认禁用；保留为后续 per-attempt deadline 改造字段
    if p.SoftCooldownMultiplier == 0 { p.SoftCooldownMultiplier = 2.0 }
    if p.CooldownBackoffFactor == 0 { p.CooldownBackoffFactor = 2.0 }
    if p.CooldownMaxSeconds == 0 { p.CooldownMaxSeconds = 600 }
    if p.CooldownMaxSeconds < p.CooldownSeconds { p.CooldownMaxSeconds = p.CooldownSeconds }
    // HalfOpenProbe 是 *bool。nil → 默认 true；非 nil 保留用户原意
    if p.HalfOpenProbe == nil {
        v := true
        p.HalfOpenProbe = &v
    }
}
```

---

## 5. 上线策略

### 5.1 一次性全做（用户已确认）

不分阶段。落地顺序按依赖：

0. 上线前按 [2026-05-02-adaptive-health-routing-rollout-runbook.md](./2026-05-02-adaptive-health-routing-rollout-runbook.md) 把现有 `gemini-auto` 最终兜底 group 显式标记为 `fallback_only=true`。
1. `HealthPolicy` 字段扩展 + 默认值。
2. `OutcomeKind` 分类器。
3. `TargetHealthState` 字段扩展 + `RecordOutcome` 统一入口。
4. `PostLLMHook` 切换到 `RecordOutcome`，引入 latency 测量。
5. `PreLLMHook` 注入 attempt 起始时间（不注入 deadline）。
6. `GetTargetHealth` 三态判定 + half-open。
7. `buildGroupedRoutingDecision` 重写为三态选择 + 兜底层特殊处理。
8. `buildActiveProbePlans` 跳过兜底层；`applyActiveProbeResult` 只记录 liveness，不写主健康状态机。
9. `transports/config.schema.json` + `governance.go` 验证逻辑。
10. UI（`HealthSnapshot` 类型 + Badge + 字段渲染）。
11. 单元测试 + 集成测试。

### 5.2 灰度

线上开启：
- 第 1 天：仅启用 §3.5 latency 测量（其他逻辑通过 `policy.SlowRatioThreshold = 999` 等手段禁用三态判定），观察日志。
- 第 2 天：启用三态判定 + half-open（不影响兜底层）。
- 第 3 天：如需要，再由用户手动开启 active probe；默认仍保持关闭。

> 灰度是部署层的事，代码侧仍是一次性全部 merged。

---

## 6. 测试计划

### 6.1 新增单元测试（`plugins/governance/health_tracker_test.go`）

| 用例 | 描述 | 期望 |
|---|---|---|
| `TestRecordOutcome_SlowSuccess_DoesNotResetFailures` | 模拟先 5 次 hard fail 触发 cooldown，然后 1 次 slow success | failures 不被清零，仍为 cooldown 直到时间到 |
| `TestGetTargetHealth_DegradedBySlowRatio` | 注入 6/10 slow + 4/10 success | 返回 Degraded |
| `TestGetTargetHealth_RecoversToHealthy` | 6/10 slow → 后续连续 10 次 success | 最终回到 Healthy |
| `TestHalfOpenProbe_OnlyOneInFlight` | cooldown 结束后并发 5 次请求 | 只有 1 次被放行做探针 |
| `TestExponentialBackoff_StreakIncrement` | 连续 3 次进入 cooldown | cooldown 时长依次为 base、base×2、base×4，封顶 max |
| `TestClassifyOutcome_429IsSoftFail` | 模拟 429 / saturated 错误 | 返回 SoftFail |
| `TestClassifyOutcome_TimeoutIsSoftFail` | 模拟 provider timeout / 未来 per-attempt deadline error | 返回 SoftFail |

### 6.2 路由器测试（`plugins/governance/grouped_router_test.go`，新建或扩展）

| 用例 | 输入 | 期望 |
|---|---|---|
| `TestRouter_HealthOverCost_DegradedYieldsToNextGroup` | 第 1 组 Degraded，第 2 组 Healthy | primary = 第 2 组 |
| `TestRouter_LastLayerOnlyAsLastResort` | 前两组都 Cooldown，第 3 组 Healthy | primary = 第 3 组（兜底） |
| `TestRouter_LastLayerNeverPromotedWhenAnyEarlierAvailable` | 第 1 组 Degraded，第 2 组 Cooldown，第 3 组 Healthy | primary = 第 1 组 Degraded，兜底追加在 fallback 链末尾 |
| `TestRouter_HalfOpenProbeSinksToFallbackTail` | 第 1 组 Cooldown 但允许 half-open，第 2 组 Healthy | primary = 第 2 组，第 1 组在 fallback 链尾部 |
| `TestRouter_FallbackOnlyAppendedAfterRegularHealthyExists` | 普通组 Healthy，兜底组 Healthy | primary = 普通组，兜底组不提前晋升，但会追加到 fallback 链末尾 |
| `TestRouter_FallbackOnlyAppendedAfterRegularDegradedExists` | 普通组 Degraded，兜底组 Healthy | primary = 普通组 Degraded，兜底组追加到 fallback 链末尾 |
| `TestRouter_FallbackOnlyRetryLimitRepeatsSingleTarget` | 兜底组只有 1 个 target 且 `retry_limit=2` | fallback 链中重复兜底 target 3 次 |
| `TestRouter_DedupAcrossHealthBuckets` | 同一 target 同时出现在多个组或健康桶 | fallback chain 中只出现一次 |
| `TestRouter_RetryLimitStillAppliesPerGroup` | 普通单组多 target 且设置 retry_limit | 每组最多选择 `1 + retry_limit` 个不同 target |
| `TestBuildGroupedRoutingDecision_CostOrdersHealthyTargetsWithinGroup` | 同组多个 Healthy target 且价格不同 | 同桶内优先选择更便宜 target，缺价格目标排在已知价格目标之后 |
| `TestBuildGroupedRoutingDecision_TargetDoesNotRecoverFromActiveProbeSuccess` | target 已 cooldown，随后 active probe success | 路由健康仍保持 cooldown，不被主动探测拉回 healthy |

### 6.3 Active Probe / Health Detection API 测试

| 用例 | 输入 | 期望 |
|---|---|---|
| `TestApplyActiveProbeResult_RecordsProbeFailureWithoutMutatingRuleHealth` | active probe 失败 | last probe 状态更新；rule health 不进入 cooldown |
| `TestApplyActiveProbeResult_RecordsProbeSuccessWithoutClearingRuleCooldown` | target 已 cooldown，active probe 成功 | cooldown 不被清空 |
| `TestBuildActiveProbePlans_UsesChatProbeForStreamOnlyHistory` | 最近真实请求只有 stream 类型 | 生成 ChatCompletion 探针计划 |
| `TestGetHealthDetectionTargets_SupportsStreamOnlyHistory` | health target 的 last real access 为 stream | API support_status 返回 supported |

### 6.4 端到端

参考 [2026-04-14-hybrid-health-probing-test-plan.md](./2026-04-14-hybrid-health-probing-test-plan.md) 的形式，新增 `2026-05-02-adaptive-routing-test-plan.md`（在第 11 步后撰写）。

本地黑盒仿真使用 [tests/manual/grouped-routing-lab](../../tests/manual/grouped-routing-lab)：

```bash
MOCK_PORT=19111 BIFROST_PORT=18081 tests/manual/grouped-routing-lab/run_lab.sh
```

该实验用本地 mock relay 模拟线上同名 provider/model，验证低成本正常优先、慢成功后切到下一普通组、普通链路失败后同一请求进入 `fallback_only` 兜底、以及 pricing overrides 只在同组同健康档内排序。

---

## 7. 风险与回滚

| 风险 | 缓解 |
|---|---|
| 三态判定误把临时抖动判为 Degraded | `SlowWindowSize=10` + `SlowRatioThreshold=0.5` 需要至少 5 次 slow 才触发，单次抖动不会切走 |
| 慢请求本身无法被立即切走 | 当前版本不改 core/provider 请求生命周期，只保证慢成功会影响后续路由；如需本次请求立即 fallback，另做 per-attempt deadline 改造 |
| half-open 探针仍然慢 | 探针失败立刻指数退避，避免短期内重复探测 |
| 兜底层被频繁触发 | 兜底不抢普通组 primary，只在普通链路同一请求内都失败或普通目标全 Cooldown 时启用，配合 half-open 恢复机制让前两层逐步恢复 |
| 旧规则未升级 policy → 新字段全 0 | `ApplyHealthPolicyDefaults` 兜底，行为等价于"启用三态健康与退避"，但 `RequestDeadlineMs` 默认仍为 0（禁用）。如需保持旧行为，在配置层显式设 `SlowRatioThreshold=999` 等 |

**回滚**：所有改动局限在 `plugins/governance/` + `framework/configstore/tables/routing_rules.go` 的字段扩展 + UI。回滚 = revert 这一组 commit；DB schema 仅追加 JSON 字段，不破坏旧版本读取。

---

## 8. 变更影响清单

| 文件 | 变更类型 |
|---|---|
| [framework/configstore/tables/routing_rules.go](../../framework/configstore/tables/routing_rules.go) | `HealthPolicy` 增字段（向后兼容） |
| [plugins/governance/health_tracker.go](../../plugins/governance/health_tracker.go) | 新增 OutcomeKind / HealthLevel / RecordOutcome / GetTargetHealth / 半开逻辑 / 退避 |
| [plugins/governance/outcome_classifier.go](../../plugins/governance/outcome_classifier.go) | **新建** |
| [plugins/governance/grouped_router.go](../../plugins/governance/grouped_router.go) | 重写 `buildGroupedRoutingDecision` |
| [plugins/governance/main.go](../../plugins/governance/main.go) | PreLLMHook 注入 attempt 起始时间；PostLLMHook 切到 RecordOutcome |
| [plugins/governance/active_probe.go](../../plugins/governance/active_probe.go) | 默认值调整 + 兜底层跳过 + stream 模型探针策略；探测结果仅记录 liveness |
| [transports/bifrost-http/handlers/governance.go](../../transports/bifrost-http/handlers/governance.go) | `validateHealthPolicy` 接受新字段；`getHealthStatus` 输出三态字段 |
| [transports/bifrost-http/handlers/providers.go](../../transports/bifrost-http/handlers/providers.go) | provider `pricing_overrides` 校验、保存、ModelCatalog reload |
| [framework/modelcatalog](../../framework/modelcatalog) | provider pricing overrides 编译、匹配、覆盖 datasheet 价格 |
| [transports/config.schema.json](../../transports/config.schema.json) | 健康策略 schema 扩展 |
| [ui/app/workspace/adaptive-routing](../../ui/app/workspace/adaptive-routing) | 三态 Badge + p95 / slow ratio / streak 显示 |
| [ui/app/workspace/providers](../../ui/app/workspace/providers) | Provider Pricing Overrides 页签 + $/1M token 转 per-token 辅助填写 |
| [ui/lib/types/routingRules.ts](../../ui/lib/types/routingRules.ts) | `HealthPolicy` / `HealthSnapshot` 类型扩展 |
| `plugins/governance/health_tracker_test.go` / `grouped_router_test.go` | 用例补齐 |

---

## 9. 待用户最终确认

- [x] slow 阈值 = 45s
- [x] 当前版本不启用端到端 deadline；`RequestDeadlineMs` 仅作为后续 per-attempt 改造预留字段
- [x] 健康优先于成本（A 方案）
- [x] 兜底层不主动探测、不影子流量
- [x] active probe 只做 liveness，不参与主健康评分
- [x] 成本排序复用 provider pricing overrides，不新建 routing 专用价格
- [x] 一次性全做（不分阶段）

除 per-attempt deadline 外，以上参数均可通过 `HealthPolicy` 默认值或数据库覆盖调整；真正的 deadline 快速 fallback 需要后续 core/provider 代码改造。

---

## 10. 用户视角与界面变更

### 10.1 是改原系统还是新一套？

**完全在原 grouped routing 基础上原地升级**，不引入第二套规则系统：

- 数据库 `TableRoutingRule` / `HealthPolicy` 只是**追加字段**，老字段语义完全不变。
- `buildGroupedRoutingDecision` 是**原地重写**，没有"新路由器 / 旧路由器"两份代码。
- **没有"启用新版"开关**。所有 grouped routing 规则升级后统一走新逻辑。

### 10.2 怎么"控制要不要用新行为"？

通过 **policy 字段取值**控制，每条规则独立：

| 想要的效果 | 配法 |
|---|---|
| 全部启用新功能（默认推荐） | 新字段保持默认值即可，无需任何点击 |
| 关闭 slow 判定（回到二态） | `slow_ratio_threshold = 999` + `slow_recovery_seconds = 0` |
| 关闭指数退避 | `cooldown_backoff_factor = 1.0` |
| 关闭半开探针 | `half_open_probe = false`（显式写 false，不是不填） |
| 完全等价旧行为 | 显式设置 `slow_ratio_threshold=999`、`slow_recovery_seconds=0`、`cooldown_backoff_factor=1.0`、`half_open_probe=false`、`request_deadline_ms=0` |

也就是说，单条规则可以"全启用"，另一条规则可以"全关闭"，互不影响。

### 10.3 后台界面变更

#### 10.3.1 路由规则编辑页 `/workspace/routing-rules`

启用 "Grouped Health Routing" 时，现有 Health Policy 表单**多出一组"高级"折叠区**，默认折叠：

```
┌─ Health Policy ────────────────────────────┐
│ Failure threshold:        [ 2  ]           │  ← 现有字段，不变
│ Failure window (s):       [ 30 ]           │
│ Cooldown (s):             [ 30 ]           │
│ Consecutive failures:     [ 0  ]           │
│                                            │
│ ▸ 高级（latency / 退避 / 半开）  ← 默认折叠 │
│   Slow threshold (ms):       [ 45000 ]     │
│   Slow window size:          [ 10    ]     │
│   Slow ratio threshold:      [ 0.5   ]     │
│   Slow recovery (s):         [ 60    ]     │
│   Request deadline (ms):     [ 0     ]     │  ← 预留字段，当前版本禁用
│   Soft cooldown multiplier:  [ 2.0   ]     │
│   Cooldown backoff factor:   [ 2.0   ]     │
│   Cooldown max (s):          [ 600   ]     │
│   Half-open probe:           [ ☑ on  ]     │
└────────────────────────────────────────────┘
```

每个字段旁带 ⓘ 悬停解释。表单未展开 = 全部用默认值 = 启用当前版本的新健康行为（deadline 预留字段仍为 0 / 禁用）。

#### 10.3.2 健康状态总览页 `/workspace/adaptive-routing`

现有列：`status / failures / consec / cooldown_until / last_msg`（二态）。

升级后变三态 + 新增列：

```
Rule: gemini-auto
┌────────────────────────────┬───────────┬─────────┬──────────┬───────────┬─────────┬────────────────┐
│ Target                     │ Status    │ p95 lat │ Slow     │ Cooldown  │ Streak  │ Last msg       │
├────────────────────────────┼───────────┼─────────┼──────────┼───────────┼─────────┼────────────────┤
│ poloapi/gemini-3.1-pro…    │ 🟡 Degr.  │ 78s     │ 6/10     │ —         │ 0       │ —              │
│ 4sapi/...medium            │ 🟢 Healthy│ 4.2s    │ 0/10     │ —         │ 0       │ —              │
│ openrouter/gemma-4-31b-it  │ 🔴 Cooldn.│ —       │ —        │ in 124s   │ 2       │ saturated      │
└────────────────────────────┴───────────┴─────────┴──────────┴───────────┴─────────┴────────────────┘
```

- Status 从二态（available / cooldown）变三态（**Healthy / Degraded / Cooldown**），绿 / 黄 / 红 badge。
- 新增列：**p95 latency**、**Slow 比例**、**Cooldown streak**（指数退避计数）。

#### 10.3.3 governance 插件全局设置

`active_health_probe_*` 几个字段位置不变，**默认开关保持关闭**，仅调整手动开启后的推荐参数：

| 字段 | 旧默认 | 新默认 / 推荐值 |
|---|---|---|
| `active_health_probe_enabled` | false | **false**（保持不变，用户手动开启） |
| `active_health_probe_interval_seconds` | 15 | **60**（手动开启后推荐） |
| `active_health_probe_passive_freshness_seconds` | 30 | **300**（手动开启后推荐） |

新建实例和已有实例都不会自动开启 active probe。需要时由用户在 governance 全局设置手动启用。

手动启用后也只用于 liveness：它可以帮助发现 target 是否彻底不可达，但不会把 target 从 cooldown 拉回 healthy，也不会因为探测失败让 target 进入 cooldown。路由健康仍以真实业务请求和 half-open 请求为准。

### 10.4 升级 / 回滚操作路径

| 场景 | 操作 |
|---|---|
| 代码上线后老规则的行为 | 启动时 `ApplyHealthPolicyDefaults` 把缺失新字段填默认值 → 自动启用三态健康、慢成功识别、退避与 half-open；deadline 仍保持禁用 |
| 想关掉某条规则的新行为 | 进规则编辑页 → 展开"高级" → 调字段 → 保存。规则级独立 |
| 完全回滚 | 代码 revert；DB schema 不需要 down（新字段是 JSON 追加），旧版本忽略未知字段即可 |

---

## 11. 对 Codex 评审反馈的处理

本节记录第一轮 Codex 评审提出的 5 点意见以及本文档的处理决策。

### 11.1 采纳的意见（4 项，已同步到文档）

| # | Codex 意见 | 核查结论 | 文档调整 |
|---|---|---|---|
| 1 | `BifrostContext.SetDeadline` 不存在；`MakeRequestWithContext` 不能真 abort 底层 fasthttp 请求，`defer wait()` 会卡住；共享 ctx cancel 后也无法继续 fallback | **成立**。watchdog 方案不纳入当前版本 | §3.4 重写：当前版本不做强制 deadline，只记录 attempt latency 并把慢成功纳入健康；真正 per-attempt deadline + 快速 fallback 需另做 core/provider 请求生命周期改造 |
| 2 | "向后兼容"措辞不准：schema 兼容 ≠ 行为兼容 | **成立**。原表述有歧义 | §0 / §2 G8 / §10 改为"schema 完全兼容 + 行为统一升级，需显式覆盖 policy 以保留旧行为" |
| 3 | `HalfOpenProbe bool` 零值区不出未填 vs 显式 false | **成立** | §3.1.1 改为 `*bool`，`ApplyHealthPolicyDefaults` 中 nil 则填默认 true |
| 4 | "最后一组就是兜底层"硬编码脆弱 | **成立** | §3.1.1.1 新增 `RouteGroup.FallbackOnly` 显式字段；§3.3 / §3.7 所有路由器与 active probe 逻辑改为识别该字段；UI §10.3.1 增加复选框 |

### 11.2 不采纳的意见（1 项）

**Codex 建议：拆成 6 步 MVP，不要一次性全做。**

**决策：不采纳，保持一次性全部代码提交。**

理由：

1. 他列举的 6 步 MVP 实际是把本方案按上线顺序重新排列，代码量不减少，review 难度不减少，只会延长业务侧"100s 假成功"问题的存活期。
2. 代码风险用**部署级灰度**控制起句（§5.2 已给出）：由 policy 字段开关控制"老行为 / 新行为"，按 rule 独立勾选，能实现与 MVP 同等的逐步启用效果。
3. review 风险用**单元测试 + 文件级可独立 revert** 控制：每个改动点封装在新函数/新文件里，旧路径只动 1–2 处调用点。
4. **active probe 默认值保持 `Enabled=false`**，不随本改造一起开。需要时用户在 governance 全局设置手动启用。

### 11.3 需要提醒 reviewer 看的重点节

- §3.4.1 / §3.4.2 / §3.4.3：为什么当前版本不做 watchdog deadline，以及后续 per-attempt deadline 的必要改造。
- §3.1.1.1：`RouteGroup.FallbackOnly` 新字段，可能需要 DB 迁移脚本把现有 `gemini-auto` 最后一组刷为 `true`。
- §8：完整影响范围清单，以该表为准（§0 TL;DR 仅说"核心收敛在 plugins/governance"，不是唯一变更点）。

### 11.4 第二轮任务清单对齐复核（已采纳）

第二轮复核指出任务清单比设计文档更严格，本文档已同步以下修正：

| # | 问题 | 文档调整 |
|---|---|---|
| 1 | `SlowRecoverySeconds` 用 `int` 会导致显式 `0` 被默认值覆盖，无法关闭 last-slow recency 判定 | §3.1.1 改为 `*int`；§3.6 改为 nil-check + 解引用；§4.2 改为 nil 时填默认 60，非 nil 保留 |
| 2 | `ConsecutiveFailures=0` 语义容易被默认值填充破坏 | §3.1.1 明确 0 = 使用 `FailureThreshold`；§4.2 明确不填默认 |
| 3 | "完全等价旧行为"配法不够明确 | §10.2 显式列出 5 个字段：`slow_ratio_threshold=999`、`slow_recovery_seconds=0`、`cooldown_backoff_factor=1.0`、`half_open_probe=false`、`request_deadline_ms=0` |
| 4 | 默认值函数名不统一 | 全文统一为 `ApplyHealthPolicyDefaults` |
| 5 | `cooldown_max_seconds` 可能小于 base cooldown | §4.2 加入 `CooldownMaxSeconds < CooldownSeconds` 时自动抬升 |
| 6 | `governLargePayload` 路径可能漏掉 attempt start | §3.5 明确普通路径和 `governLargePayload` 路径都要写入 attempt 起始时间 |
| 7 | active probe 推荐值与现有配置字段映射不清楚 | §3.7 明确继续使用现有 `ActiveHealthProbeIntervalSeconds` / `ActiveHealthProbeIdlePauseMinutes` / `ActiveHealthProbePassiveFreshnessSeconds` |
| 8 | 路由器测试缺少 fallback-only、dedup、retry_limit 回归用例 | §6.2 补齐 3 个测试用例 |

### 11.5 第三轮实现复核（已同步）

本轮代码实现后又补充了两类行为，文档同步如下：

| # | 实现事实 | 文档调整 |
|---|---|---|
| 1 | active probe 已改为 liveness-only：`applyActiveProbeResult` 只调用 `RecordProbeResult` | §3.7、§6.3、§10.3.3 明确探测结果不参与 slow ratio / cooldown / routing health |
| 2 | stream-only target 的 health detection 支持已落地：真实请求是 stream 时探针使用 ChatCompletion | §1.3、§3.7、§6.3 更新为 supported 行为 |
| 3 | 成本排序复用 ModelCatalog 和 provider `pricing_overrides`，不新建价格表 | §2 G9、§3.3.4、§8 补齐影响范围 |
| 4 | 已有本地黑盒仿真覆盖同名 provider/model 的真实链路 | §6.4 加入 grouped-routing-lab 命令和验证范围 |

---
