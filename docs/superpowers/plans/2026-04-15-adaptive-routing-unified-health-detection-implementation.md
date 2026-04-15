# Adaptive Routing Unified Health Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `/workspace/adaptive-routing` 交付统一健康检测控制台，包含全局检测参数、去重后的目标名单、逐目标检测开关、首检、空闲暂停，以及保留现有规则级健康状态表。

**Architecture:** 保持 rule-scoped 健康状态作为路由权威来源，不去合并 rule cooldown。新增一层“目标级偏好 + 目标级运行态”活动模型：偏好持久化到 config store 新表，运行态继续保留在治理插件本地内存。Adaptive Routing handler 负责把 grouped routing 目标去重、合并偏好与 node-local 活动信息，对外暴露统一名单接口；前端在现有页面上增加目标表，不改写下半部规则级健康表。

**Tech Stack:** Go, GORM/configstore, governance plugin, FastHTTP handlers, Next.js, RTK Query, TypeScript, Vitest, Playwright

---

## 执行锁定

以下实现规则在本轮先固定，不再边做边改：

1. `target_id` 不复用现有 `TargetKey(provider:model[:key_id])`。
原因：现有 key 在 `key_id=""` 时不可逆，会把“无 Key ID 的可见目标”与普通二段 key 混淆。

2. `target_id` 使用“URL-safe base64(JSON triple)”。
目标三元组固定为：

```json
["provider","model","key_id"]
```

对应约束：

- `target_id` 用于 UI 和 `PUT /api/governance/health-detection-targets/{target_id}`
- 持久化表中的 `target_key` 直接保存同一份 canonical JSON string
- 这样可以稳定保留空 `key_id`

3. `Paused (Idle)` 只在 `last_real_access_at` 非空后才会生效。
原因：如果零值也参与 idle 判断，`Pending First Probe -> Eligible` 无法成立。

4. `Pending First Probe` 通过“显式等待标记 + 本地运行态推导”共同决定：

- 打开目标且没有 `last_real_access_at` 时，立即置 `pending_first_probe=true`
- 关闭目标时清除 `pending_first_probe`
- 任意一次主动探测完成后清除 `pending_first_probe`
- 进程重启后若没有本地探测记录且没有真实访问，已开启目标允许再次进入 `Pending First Probe`

5. 首检的有效请求类型按以下顺序决定：

1. `last_real_access_request_type`
2. `last_probe_request_type`
3. 默认 `chat_completions`

仅以下类型视为支持主动检测：

- `chat_completions`
- `responses`
- `text_completions`

6. `active_health_probe_passive_freshness_seconds` 本轮退出控制台主流程。

- 调度逻辑改为“开启即按 interval 探测，直到超过 idle pause 才停”
- `config.schema.json` 保留旧字段兼容一版，不再在 Adaptive Routing UI / API 中暴露
- 新的控制台字段使用 `idle_pause_minutes`

## 文件结构

### 新增

- `framework/configstore/tables/health_detection_target_preference.go`
  - 目标级检测偏好持久化表模型

- `ui/app/workspace/adaptive-routing/healthDetectionTargets.ts`
  - 统一名单状态文案、状态描述、支持状态描述、行级可编辑判断

- `ui/app/workspace/adaptive-routing/healthDetectionTargets.test.ts`
  - 统一名单纯函数测试

- `ui/app/workspace/adaptive-routing/healthDetectionTargetsTable.tsx`
  - 统一目标名单表格

### 修改

- `framework/configstore/migrations.go`
  - 新增目标偏好表迁移

- `framework/configstore/rdb.go`
  - 目标偏好读取与 upsert

- `framework/configstore/rdb_test.go`
  - 目标偏好存储测试

- `plugins/governance/health_tracker.go`
  - 拆分真实访问与主动探测元数据
  - 增加目标级活动快照与首检等待标记

- `plugins/governance/health_tracker_test.go`
  - 真实访问 / 主动探测拆分测试

- `plugins/governance/active_probe.go`
  - 新 idle pause / 首检调度
  - 目标偏好接入

- `plugins/governance/active_probe_test.go`
  - 调度规则、首检、空闲暂停测试

- `plugins/governance/main.go`
  - 接入新的 probe 配置字段
  - grouped routing 真正命中目标时写入 `last_real_access_at`

- `transports/config.schema.json`
  - 新增 `active_health_probe_idle_pause_minutes`
  - 旧 `active_health_probe_passive_freshness_seconds` 标记 deprecated

- `transports/bifrost-http/handlers/adaptive_routing.go`
  - 全局配置接口增加 `idle_pause_minutes`
  - 新增统一目标名单 GET / PUT
  - 聚合当前 grouped routing 目标、合并偏好和 node-local 活动

- `transports/bifrost-http/handlers/adaptive_routing_test.go`
  - 配置接口与目标名单接口测试

- `ui/lib/types/routingRules.ts`
  - 全局配置与统一名单类型定义

- `ui/lib/store/apis/routingRulesApi.ts`
  - 统一名单查询与切换 mutation

- `ui/app/workspace/adaptive-routing/healthDetectionConfig.ts`
  - 表单转换，改为使用 `idle_pause_minutes`

- `ui/app/workspace/adaptive-routing/healthDetectionConfig.test.ts`
  - 全局配置 payload / defaults 测试

- `ui/app/workspace/adaptive-routing/healthDetectionSettingsCard.tsx`
  - 用 `Idle Pause Minutes` 替换旧的 passive freshness 输入

- `ui/app/workspace/adaptive-routing/healthStatusView.tsx`
  - 新页面布局：顶部卡片 + 统一名单 + 规则级健康状态

- `tests/e2e/features/placeholders/placeholders.spec.ts`
  - 更新 Adaptive Routing 页面断言，移除旧占位页预期

## Task 1: 新增目标级偏好持久化

**Files:**
- Create: `framework/configstore/tables/health_detection_target_preference.go`
- Modify: `framework/configstore/migrations.go`
- Modify: `framework/configstore/rdb.go`
- Modify: `framework/configstore/rdb_test.go`

- [ ] **Step 1: 新增表模型**

```go
type TableHealthDetectionTargetPreference struct {
	TargetKey         string    `gorm:"primaryKey;type:text" json:"target_key"`
	Provider          string    `gorm:"type:varchar(255);not null;index" json:"provider"`
	Model             string    `gorm:"type:varchar(255);not null;index" json:"model"`
	KeyID             *string   `gorm:"type:varchar(255)" json:"key_id,omitempty"`
	DetectionEnabled  bool      `gorm:"not null;default:false" json:"detection_enabled"`
	CreatedAt         time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt         time.Time `gorm:"index;not null" json:"updated_at"`
}
```

- [ ] **Step 2: 新增迁移**

迁移要求：

- 创建 `health_detection_target_preferences`
- 只创建一次，重复执行要幂等
- rollback 只删除这张新表

Run: `go test ./framework/configstore -run 'TestMigrations|TestCreateAndGetPlugin'`
Expected: 迁移相关测试通过，已有表不受影响

- [ ] **Step 3: 在 `RDBConfigStore` 增加读写方法，不扩展全局 `ConfigStore` interface**

```go
func (s *RDBConfigStore) GetHealthDetectionTargetPreferences(ctx context.Context) ([]tables.TableHealthDetectionTargetPreference, error)
func (s *RDBConfigStore) UpsertHealthDetectionTargetPreference(ctx context.Context, pref *tables.TableHealthDetectionTargetPreference) error
```

要求：

- `Upsert` 以 `target_key` 为唯一键覆盖开关结果
- `KeyID=nil` 也允许保存
- 不删除“已不在当前规则中出现”的历史偏好

- [ ] **Step 4: 补存储测试**

新增测试名固定为：

- `TestHealthDetectionTargetPreferenceCRUD`
- `TestHealthDetectionTargetPreferenceUpsertOverwritesToggle`
- `TestHealthDetectionTargetPreferenceAllowsNilKeyID`

Run: `go test ./framework/configstore -run 'TestHealthDetectionTargetPreference'`
Expected: 以上三个测试通过

## Task 2: 拆分目标级运行态

**Files:**
- Modify: `plugins/governance/health_tracker.go`
- Modify: `plugins/governance/health_tracker_test.go`

- [ ] **Step 1: 将单一 observation state 改成目标活动状态**

新增结构建议如下：

```go
type TargetActivityState struct {
	mu                    sync.Mutex
	lastRealAccessAt      time.Time
	lastRealAccessType    schemas.RequestType
	lastProbeAt           time.Time
	lastProbeType         schemas.RequestType
	lastProbeResult       string
	lastProbeError        string
	pendingFirstProbe     bool
}
```

- [ ] **Step 2: 增加最小接口，不改动 rule-scoped 健康桶**

```go
func (ht *HealthTracker) RecordRealAccess(targetKey string, requestType schemas.RequestType, now time.Time)
func (ht *HealthTracker) RecordProbeResult(targetKey string, requestType schemas.RequestType, success bool, failureMsg string, now time.Time)
func (ht *HealthTracker) SetPendingFirstProbe(targetKey string, pending bool)
func (ht *HealthTracker) GetTargetActivity(targetKey string) TargetActivitySnapshot
```

要求：

- `RecordProbeResult` 不得覆盖 `last_real_access_at`
- `RecordRealAccess` 不得清空 `last_probe_*`
- `pending_first_probe` 只由 toggle / probe 完成修改

- [ ] **Step 3: 保持现有规则级健康接口兼容**

兼容规则：

- `GET /api/governance/health-status` 仍返回 `last_observed_at`
- `last_observed_at` 使用“最近真实访问或最近探测中较新的那个”
- `last_observation_source` 同步映射为 `passive` 或 `active`

- [ ] **Step 4: 补行为测试**

新增或改造测试名固定为：

- `TestHealthTracker_RecordRealAccess_DoesNotOverwriteLastProbe`
- `TestHealthTracker_RecordProbeResult_DoesNotOverwriteLastRealAccess`
- `TestHealthTracker_SetPendingFirstProbe_VisibleInSnapshot`
- `TestHealthTracker_GetTargetStatusForRule_PreservesLegacyObservedFields`

Run: `go test ./plugins/governance -run 'TestHealthTracker_'`
Expected: 新旧 health tracker 测试全部通过

## Task 3: 将主动探测改成“首检 + idle pause”

**Files:**
- Modify: `plugins/governance/active_probe.go`
- Modify: `plugins/governance/active_probe_test.go`
- Modify: `plugins/governance/main.go`

- [ ] **Step 1: 扩展运行配置**

将运行配置改为：

```go
type ActiveHealthProbeConfig struct {
	Enabled        bool
	Interval       time.Duration
	IdlePause      time.Duration
	Timeout        time.Duration
	MaxConcurrency int
}
```

同时在 `governance.Config` 新增：

```go
ActiveHealthProbeIdlePauseMinutes *int `json:"active_health_probe_idle_pause_minutes,omitempty"`
```

- [ ] **Step 2: grouped routing 真正命中目标时写入 `last_real_access_at`**

在 `PostLLMHook` 的 grouped routing 分支里，把旧的：

```go
p.healthTracker.RecordObservation(...)
```

改为：

```go
p.healthTracker.RecordRealAccess(targetKey, requestType, time.Now())
```

并继续保留：

- `RecordFailureForRule`
- `RecordSuccessForRule`

- [ ] **Step 3: 重写计划生成逻辑**

新的调度筛选规则必须严格对应设计稿：

1. `mode=passive` 时不产生活动计划
2. `Unsupported` 目标不产生活动计划
3. `detection_enabled=false` 不产生活动计划
4. `pending_first_probe=true` 必须进计划，即使没有真实访问
5. `last_real_access_at` 为空但本地还没做过 probe，也允许首检进计划
6. `last_real_access_at` 非空且未超过 idle pause，按 `probe interval` 常规探测
7. `last_real_access_at` 非空且超过 idle pause，不进计划

- [ ] **Step 4: probe 结果写回同时清理首检等待**

`applyActiveProbeResult` 必须做到：

- 写 `last_probe_at`
- 写 `last_probe_result`
- 写 `last_probe_error`
- 清 `pending_first_probe`
- success 时恢复所有引用规则
- failure 时对所有引用规则记失败

- [ ] **Step 5: 补调度测试**

新增或改造测试名固定为：

- `TestBuildActiveProbePlans_IncludesPendingFirstProbeWithoutRealAccess`
- `TestBuildActiveProbePlans_SkipsTargetsPastIdlePause`
- `TestBuildActiveProbePlans_UsesBootstrapChatRequestTypeForFirstProbe`
- `TestApplyActiveProbeResult_ClearsPendingFirstProbe`
- `TestApplyActiveProbeResult_DoesNotInventRealAccess`

Run: `go test ./plugins/governance -run 'TestBuildActiveProbePlans_|TestApplyActiveProbeResult_'`
Expected: 相关主动探测测试全部通过

## Task 4: 扩展全局配置接口与 schema

**Files:**
- Modify: `transports/bifrost-http/handlers/adaptive_routing.go`
- Modify: `transports/bifrost-http/handlers/adaptive_routing_test.go`
- Modify: `transports/config.schema.json`
- Modify: `plugins/governance/main.go`

- [ ] **Step 1: 全局配置 response / request 改成 `idle_pause_minutes`**

接口结构调整为：

```go
type HealthDetectionConfigResponse struct {
	Mode                             string `json:"mode"`
	IdlePauseMinutes                 int    `json:"idle_pause_minutes"`
	ActiveHealthProbeIntervalSeconds int    `json:"active_health_probe_interval_seconds"`
	ActiveHealthProbeTimeoutSeconds  int    `json:"active_health_probe_timeout_seconds"`
	ActiveHealthProbeMaxConcurrency  int    `json:"active_health_probe_max_concurrency"`
	Editable                         bool   `json:"editable"`
	ReadOnlyReason                   string `json:"read_only_reason,omitempty"`
}
```

- [ ] **Step 2: `updateHealthDetectionConfig` 写入新的 plugin config 字段**

要求：

- 写 `active_health_probe_enabled`
- 写 `active_health_probe_idle_pause_minutes`
- 保留无关 governance config
- 不再往 Adaptive Routing API response 中返回 `active_health_probe_passive_freshness_seconds`

- [ ] **Step 3: 更新 schema**

`transports/config.schema.json` 必须：

- 新增 `active_health_probe_idle_pause_minutes`
- 旧 `active_health_probe_passive_freshness_seconds` 留在 schema 中，但描述加 `Deprecated`

- [ ] **Step 4: 补 handler 测试**

新增或改造测试名固定为：

- `TestGetHealthDetectionConfigReturnsIdlePauseMinutes`
- `TestUpdateHealthDetectionConfigPersistsIdlePauseMinutes`
- `TestUpdateHealthDetectionConfigStillMergesExistingPluginConfig`

Run: `go test ./transports/bifrost-http/handlers -run 'TestGetHealthDetectionConfig|TestUpdateHealthDetectionConfig'`
Expected: Adaptive Routing config 相关测试全部通过

## Task 5: 新增统一目标名单接口

**Files:**
- Modify: `transports/bifrost-http/handlers/adaptive_routing.go`
- Modify: `transports/bifrost-http/handlers/adaptive_routing_test.go`

- [ ] **Step 1: 在 handler 内定义窄接口，避免改全局 `ConfigStore`**

```go
type adaptiveRoutingTargetPreferenceStore interface {
	GetHealthDetectionTargetPreferences(ctx context.Context) ([]configstoreTables.TableHealthDetectionTargetPreference, error)
	UpsertHealthDetectionTargetPreference(ctx context.Context, pref *configstoreTables.TableHealthDetectionTargetPreference) error
}
```

要求：

- `configStore == nil` 或不实现该接口时，GET 仍可读，PUT 返回 conflict
- 这样不会引爆全仓 `ConfigStore` mock

- [ ] **Step 2: 新增 `GET /api/governance/health-detection-targets`**

聚合逻辑必须覆盖：

1. 只收 grouped health routing 规则里的 target
2. 只要 `provider + model` 完整就显示
3. `key_id` 为空也要显示
4. 同一 `provider + model + key_id` 只保留一行
5. 合并 `referenced_rule_ids` / `referenced_rule_names`
6. 合并偏好，没有偏好时默认 `detection_enabled=false`
7. 计算 `support_status`
8. 计算 `probe_state`
9. 计算 `rule_health_summary.cooldown_rule_count`
10. 固定返回 `runtime_scope=node_local`

- [ ] **Step 3: 新增 `PUT /api/governance/health-detection-targets/{target_id}`**

写入规则固定为：

- 只允许更新 `detection_enabled`
- 目标必须存在于当前 grouped routing 去重结果中
- `Unsupported` 目标返回 `409`
- 打开且无真实访问时设置 `pending_first_probe=true`
- 关闭时清 `pending_first_probe`
- 成功后返回更新后的单行目标数据

- [ ] **Step 4: `probe_state` 优先级按设计稿实现**

优先级固定为：

1. `unsupported`
2. `off`
3. `pending_first_probe`
4. `paused_idle`
5. `eligible`

- [ ] **Step 5: 补接口测试**

新增或改造测试名固定为：

- `TestGetHealthDetectionTargets_DeduplicatesAcrossRules`
- `TestGetHealthDetectionTargets_IncludesUnsupportedTargetWithoutKeyID`
- `TestGetHealthDetectionTargets_DefaultsSupportedTargetsToOff`
- `TestGetHealthDetectionTargets_ComputesCooldownRuleSummary`
- `TestUpdateHealthDetectionTarget_EnablesPendingFirstProbe`
- `TestUpdateHealthDetectionTarget_RejectsUnsupportedTarget`

Run: `go test ./transports/bifrost-http/handlers -run 'TestGetHealthDetectionTargets|TestUpdateHealthDetectionTarget'`
Expected: 统一目标名单相关测试通过

## Task 6: 更新前端数据契约

**Files:**
- Modify: `ui/lib/types/routingRules.ts`
- Modify: `ui/lib/store/apis/routingRulesApi.ts`
- Modify: `ui/app/workspace/adaptive-routing/healthDetectionConfig.ts`
- Create: `ui/app/workspace/adaptive-routing/healthDetectionTargets.ts`
- Create: `ui/app/workspace/adaptive-routing/healthDetectionTargets.test.ts`
- Modify: `ui/app/workspace/adaptive-routing/healthDetectionConfig.test.ts`

- [ ] **Step 1: 更新全局配置类型**

删除 UI response / payload 中的：

- `active_health_probe_passive_freshness_seconds`

新增：

- `idle_pause_minutes`

- [ ] **Step 2: 新增统一名单类型**

类型固定包含：

```ts
export type HealthDetectionProbeState =
	| "unsupported"
	| "off"
	| "pending_first_probe"
	| "eligible"
	| "paused_idle";

export interface HealthDetectionTarget {
	target_id: string;
	provider: string;
	model: string;
	key_id?: string;
	referenced_rule_ids: string[];
	referenced_rule_names: string[];
	support_status: "supported" | "unsupported";
	support_reason?: string;
	detection_enabled: boolean;
	probe_state: HealthDetectionProbeState;
	rule_health_summary: {
		total_rule_count: number;
		cooldown_rule_count: number;
	};
	last_real_access_at?: string;
	last_probe_at?: string;
	last_probe_result?: "success" | "failure";
	last_probe_error?: string;
	runtime_scope: "node_local";
}
```

- [ ] **Step 3: 增加 RTK Query endpoints**

```ts
getHealthDetectionTargets
updateHealthDetectionTarget
```

缓存要求：

- `updateHealthDetectionTarget` 成功后 invalidate `RoutingRules`
- 统一名单 toggle 不走批量保存

- [ ] **Step 4: 增加纯函数测试**

新增测试名固定为：

- `describe("buildHealthDetectionUpdatePayload", ...)`
- `describe("getProbeStateLabel", ...)`
- `describe("getProbeStateDescription", ...)`
- `describe("isTargetToggleDisabled", ...)`

Run: `npm --prefix ui exec vitest run app/workspace/adaptive-routing/healthDetectionConfig.test.ts app/workspace/adaptive-routing/healthDetectionTargets.test.ts`
Expected: 纯函数测试通过

## Task 7: 改造 Adaptive Routing 页面

**Files:**
- Modify: `ui/app/workspace/adaptive-routing/healthDetectionSettingsCard.tsx`
- Create: `ui/app/workspace/adaptive-routing/healthDetectionTargetsTable.tsx`
- Modify: `ui/app/workspace/adaptive-routing/healthStatusView.tsx`

- [ ] **Step 1: 设置卡改成设计稿字段**

必须做到：

- 显示 `Detection Mode`
- 显示 `Idle Pause Minutes`
- 显示 `Probe Interval`
- 显示 `Probe Timeout`
- 显示 `Max Concurrency`
- 去掉旧的 `Passive freshness window`
- 全局保存成功 toast 使用 `Health detection settings updated`
- 全局保存失败 toast 使用 `Failed to update health detection settings`

新增 test ids：

- `adaptive-routing-idle-pause`
- 保留已有 save / discard test ids

- [ ] **Step 2: 新增统一目标名单表**

表格至少包含以下列：

- `Provider`
- `Model`
- `Key ID`
- `Referenced By`
- `Support Status`
- `Detection Enabled`
- `Probe State`
- `Rule Health Summary`
- `Last Real Access`
- `Last Probe`
- `Last Probe Result`
- 失败时可直接看到 `last_probe_error`，至少通过 tooltip 或附加说明暴露

新增 test ids：

- `adaptive-routing-targets-table`
- `adaptive-routing-target-toggle-<target_id>`

- [ ] **Step 3: 页面说明与提示必须落地**

页面必须出现以下信息：

- `Probe State is target-level activity, not rule health.`
- `Runtime activity reflects the current gateway node only.`
- `Passive only` 时的全局提示

- [ ] **Step 4: 保留下半部规则级健康表**

要求：

- 继续显示 rule name / policy / target rows
- 不移除已有可用 / cooldown 统计卡
- 不把统一名单状态混入 rule health 列里

- [ ] **Step 5: 行级 toggle 立即生效**

交互要求：

- supported 行可直接开关
- unsupported 行显示原因并禁用
- read-only 模式下所有 toggle 禁用
- 成功 toast 直接说明目标已更新
- 失败 toast 直接显示接口错误

- [ ] **Step 6: 运行最小 UI 验证**

Run: `npm --prefix ui run lint`
Expected: 无 lint 错误

## Task 8: 回归与交付验证

**Files:**
- Modify only if verification uncovers issues

- [ ] **Step 1: 处理旧占位页 E2E**

更新 `tests/e2e/features/placeholders/placeholders.spec.ts` 中的 Adaptive Routing 断言为：

- 页面能打开 `/workspace/adaptive-routing`
- 可见 `Adaptive Routing`
- 可见 `adaptive-routing-health-detection-card`
- 可见 `adaptive-routing-targets-table` 或空状态说明

- [ ] **Step 2: 跑后端定向测试**

Run:

```bash
go test ./framework/configstore -run 'TestHealthDetectionTargetPreference'
go test ./plugins/governance -run 'TestHealthTracker_|TestBuildActiveProbePlans_|TestApplyActiveProbeResult_|TestBuildGroupedRoutingDecision_'
go test ./transports/bifrost-http/handlers -run 'TestGetHealthDetectionConfig|TestUpdateHealthDetectionConfig|TestGetHealthDetectionTargets|TestUpdateHealthDetectionTarget|TestGetHealthStatusUsesInMemoryRoutingRulesWithoutConfigStore'
```

Expected: 全部 PASS

- [ ] **Step 3: 跑前端定向验证**

Run:

```bash
npm --prefix ui exec vitest run app/workspace/adaptive-routing/healthDetectionConfig.test.ts app/workspace/adaptive-routing/healthDetectionTargets.test.ts
npm --prefix ui run lint
npm --prefix ui run build
```

Expected: 测试、lint、构建全部通过

- [ ] **Step 4: 跑 transport 构建**

Run: `make build`
Expected: bifrost-http 构建成功

- [ ] **Step 5: 对照设计稿逐项验收**

必须逐项确认：

- 同一目标跨规则只显示一行
- `Key ID` 为空目标仍可见且为 `Unsupported`
- 新目标默认 `Off`
- 开启无真实访问目标后出现 `Pending First Probe`
- 真实请求命中后 `last_real_access_at` 更新
- 超过 idle pause 后目标变成 `Paused (Idle)`
- 下半部规则健康表仍是权威来源
- 页面明确提示 `node-local`

## 交付顺序

推荐实际执行顺序：

1. Task 1
2. Task 2
3. Task 3
4. Task 4
5. Task 5
6. Task 6
7. Task 7
8. Task 8
