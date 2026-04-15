# Logs 智能路由请求归组与最终落点分析 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `Logs` 页面交付首版“请求归组 + 双成功率 + 最终成功落点分布”，让智能路由切换过程、最终成功落点和真实请求成功率都能被直接看懂。

**Architecture:** 后端继续保留现有尝试级 `/api/logs` 列表，但补齐三类能力：一是把 grouped routing 的层级信息在写日志时直接持久化；二是基于“最终尝试”计算请求级统计与最终成功分布；三是给当前页尝试列表补齐可直接用于前端归组的元数据。前端只在默认 `timestamp desc` 主视角启用请求归组，列表仍基于尝试级分页，顶部统计与最终成功分布走后端统一口径，实时更新只改可见列表，不在前端本地硬算请求级统计。

**Tech Stack:** Go, GORM, FastHTTP, SQLite/Postgres, RTK Query, Next.js, TanStack Table, Vitest, Playwright

---

## Completion Standard

本计划完成时必须同时满足以下条件：

- `Logs` 顶部同时展示 `Request Success Rate`、`Attempt Success Rate`、`Avg Final Latency`、`Total Attempts`、`Total Cost`
- `Logs` 中部展示 `Final Success Distribution`，支持 `model/provider/key/layer`
- `Logs` 主列表在默认 `timestamp desc` 视角下按请求组展示，支持展开查看尝试链
- 详情面板能展示请求摘要与尝试时间线；若当前页没有完整请求组上下文，不伪造最终结果
- 请求级统计与最终成功分布严格遵守设计稿口径：
  - 列表：尝试级筛选
  - 请求成功率 / 最终成功分布：先确定最终尝试，再对最终尝试应用筛选
- `route_layer_index` 不在 handler 里事后猜测，而是在日志写入时直接落库
- Postgres 下现有 attempt-level matview 快路不被破坏；请求级统计额外走 raw logs 查询并合并
- 后端目标测试、前端 Vitest、UI build、日志页 smoke E2E 全部通过

## Revision Notes

本版任务清单已吸收上一轮审查意见，以下规则是实现时必须遵守的硬约束：

- 请求级统计不能对“已筛选 attempts”再求最终结果，必须先从原始 logs 得到每组最终尝试，再对最终尝试应用筛选
- `route_layer_index` 不能依赖 redacted routing rule 在 handler 侧反推，必须在 grouped routing 生效时把层级计划带到 logging 并写入 `logs`
- `group_by=layer` 保留在 V1，但前提是后端把 `route_layer_index` 作为真实字段返回
- `/api/logs` 的 `group_id / attempt_sequence / is_final_attempt` 不能靠 `gorm:"-"` 直接 `Find(&[]Log)` 碰运气，要么用可映射字段，要么用专用 projection struct 再转成 `Log`
- Postgres 的 `/api/logs/stats` 继续复用 matview 产出的 attempt-level 数据，但请求级字段与 `average_final_latency` 单独走 raw logs 查询后合并
- 前端顺序固定为：共享 helper → 页面状态分流 → 实时更新口径修正 → 分布卡 → 归组列表 → 详情面板 → smoke 回归
- E2E 只做 smoke，不假设环境里天然存在 fallback 链路数据
- grouped rows 继续复用现有 TanStack table 的 visible columns / hidden columns / pinning 体系，不另起一套固定表

## File Map

### Backend routing and logging source of truth

- Modify: `plugins/governance/grouped_router.go`
  - 给 grouped routing 决策补齐 primary / fallback 的层级计划
- Modify: `plugins/governance/routing.go`
  - 扩展 `RoutingDecision`，承载层级信息
- Modify: `plugins/governance/main.go`
  - 把当前尝试层级与 fallback 层级计划放入 context，并在 fallback 时切换
- Modify: `plugins/governance/routing_test.go`
  - 覆盖层级计划与 fallback 对齐
- Modify: `plugins/logging/main.go`
  - 从 context 读取当前尝试层级
- Modify: `plugins/logging/operations.go`
  - 在 create / update / streaming update 时写入 `route_layer_index`
- Modify: `plugins/logging/writer.go`
  - 确保 websocket 与异步写入使用同一字段
- Modify: `plugins/logging/utils.go`
  - 透出新 logstore 能力
- Modify: `plugins/logging/operations_test.go`
  - 覆盖日志写入时的 `route_layer_index`

### Backend logstore contracts and queries

- Modify: `framework/logstore/tables.go`
  - 扩展 `Log`、`SearchStats`、最终成功分布响应结构
- Modify: `framework/logstore/store.go`
  - 扩展 `LogStore` 接口
- Modify: `framework/logstore/rdb.go`
  - 接入新查询入口，保留现有 `SearchLogs` / `GetStats` 行为
- Modify: `framework/logstore/matviews.go`
  - 保留 attempt-level 快路，同时与请求级统计做结果合并
- Modify: `framework/logstore/migrations.go`
  - 给 `logs` 增加 `route_layer_index`，并补齐 `parent_request_id` 索引
- Create: `framework/logstore/request_grouping.go`
  - 存放 ranked attempts / final attempts / request-level aggregation helper
- Create: `framework/logstore/request_grouping_test.go`
  - 覆盖请求归组元数据、请求成功率、最终成功分布、层级分布

### HTTP handlers

- Modify: `transports/bifrost-http/handlers/logging.go`
  - 注册 `/api/logs/final-distribution`
  - 暴露新增 stats 与列表字段
- Create: `transports/bifrost-http/handlers/logging_test.go`
  - 覆盖 stats、final distribution、list/detail 新字段透传

### Frontend data and state

- Modify: `ui/lib/types/logs.ts`
  - 增加请求级统计、归组字段、最终成功分布类型
- Modify: `ui/lib/store/apis/logsApi.ts`
  - 增加 final distribution query
- Create: `ui/app/workspace/logs/requestGrouping.ts`
  - 归组 helper、summary formatter、grouped view gate
- Create: `ui/app/workspace/logs/requestGrouping.test.ts`
  - 覆盖 helper 语义

### Frontend UI

- Modify: `ui/app/workspace/logs/page.tsx`
  - 分离 attempt/request 视图状态，修正实时更新策略
- Create: `ui/app/workspace/logs/views/finalSuccessDistributionCard.tsx`
  - 展示最终成功分布
- Create: `ui/app/workspace/logs/views/requestGroupRows.tsx`
  - 专门渲染组头与尝试子行
- Modify: `ui/app/workspace/logs/views/logsTable.tsx`
  - 接入 grouped body 与 test ids
- Modify: `ui/app/workspace/logs/views/columns.tsx`
  - 保留 attempt 行列定义，补齐归组子行复用字段
- Modify: `ui/app/workspace/logs/sheets/logDetailsSheet.tsx`
  - 接入请求摘要、尝试时间线、降级展示

### Regression

- Modify: `tests/e2e/features/logs/pages/logs.page.ts`
  - 增加新卡片、分布卡、请求组入口的 selector
- Create: `tests/e2e/features/logs/logs-grouping.smoke.spec.ts`
  - 放置本次功能专用 smoke，用例不混入现有大而全的 logs suite

## Implementation Notes

### Query semantics split

- `SearchLogs`、`total_requests`、现有 histogram、现有 trend：继续是 attempt-level 语义
- `total_requests` / `Total Attempts` 永远表示当前筛选下的全部 attempts，总数不能从只包含 completed rows 的 matview 直接取
- `Request Success Rate`、`Avg Final Latency`、`Final Success Distribution`：
  - 先按 `group_id = COALESCE(parent_request_id, id)` 找到每组最终已完成尝试
  - 再对这批最终尝试应用筛选条件

### `route_layer_index` source

- 不在 handler 里查 routing rules 再回推
- grouped routing 在生成 primary / fallback chain 时就得到每个 target 的 layer
- governance 把当前 attempt layer 与 fallback layer plan 放入 context
- logging 在写 attempt log 时直接把 `route_layer_index` 落到 `logs`

### Postgres stats behavior

- attempt-level 统计仍优先用 `mv_logs_hourly`
- `total_requests` 和列表总数继续走 raw logs 计数
- request-level 字段与 `average_final_latency` 永远基于 raw logs 计算
- `/api/logs/stats` 最终把三部分结果合并成一个响应对象：
  - raw total attempts
  - matview completed-attempt stats
  - raw final-attempt request stats

### Layer distribution behavior

- `group_by=layer` 使用 `route_layer_index`
- `route_layer_index` 为 `NULL` 的最终成功尝试统一归到 `Unlayered`
- 这样普通请求不会被硬塞进 Layer 1/2/3，也不会让总量对不上
- 最终成功分布建议同时返回稳定 `value` 与展示用 `label`，避免 `group_by=key` 时仅靠名称无法稳定识别

### Detail fallback behavior

- 当选中的 log 所属请求组完整地存在于当前页归组数据里时，详情面板展示完整请求摘要与尝试时间线
- 当当前页只拿到部分 attempts，或者用户直接通过 `selected_log` 打开某条孤立记录时：
  - 不伪造“最终成功对象”
  - 请求摘要只显示“当前页可见 attempts”级别的信息
  - 尝试时间线只渲染当前页可见 attempts，并显式标注 `Partial`
  - 与完整最终结果强绑定的字段留空或显示 `Unavailable in current page`
  - 仍保留当前 attempt 的完整细节

## Task 1: Persist `route_layer_index` at the logging source

**Files:**
- Modify: `plugins/governance/grouped_router.go`
- Modify: `plugins/governance/routing.go`
- Modify: `plugins/governance/main.go`
- Modify: `plugins/governance/routing_test.go`
- Modify: `plugins/logging/main.go`
- Modify: `plugins/logging/operations.go`
- Modify: `plugins/logging/writer.go`
- Modify: `plugins/logging/operations_test.go`
- Modify: `framework/logstore/tables.go`
- Modify: `framework/logstore/migrations.go`

- [ ] 给 grouped routing chain 中的每个 target 补齐 `layer_index`
- [ ] 扩展 `RoutingDecision`，至少能表达：
  - 当前 primary 的 `route_layer_index`
  - fallback chain 对齐的 `fallback_route_layer_indexes`
- [ ] 在 `plugins/governance/main.go` 中保存两类 context 值：
  - 当前尝试层级
  - fallback 层级计划
- [ ] 在 fallback 切换时按 `fallback_index` 更新“当前尝试层级”，与现有 `FallbackKeyIDs` 对齐
- [ ] 给 `framework/logstore.Log` 增加真实字段 `RouteLayerIndex *int`
- [ ] 在 logstore migration 中给 `logs` 表新增 `route_layer_index`，并补齐 `parent_request_id` 索引
- [ ] 在 logging 的真实写入路径里统一带上 `route_layer_index`：
  - `pendingLogs` processing entry
  - `buildInitialLogEntry`
  - `buildCompleteLogEntryFromPending`
  - 无 `pending` 的最小错误补写分支
  - streaming final write
- [ ] 确保 websocket 回调构造的 log entry 与异步最终写入拿到的是同一个 layer 值
- [ ] 增加测试：
  - grouped routing 生成的 fallback 层级计划正确
  - fallback attempt 写日志时层级没有错位
  - processing log、最终完成 log、异常补写 log 的层级一致
  - 未启用 grouped routing 时 `route_layer_index` 为空

**验证：**

- `go test ./plugins/governance -run 'TestBuildGroupedRoutingDecision_' -count=1`
- `go test ./plugins/logging -run 'Test.*RouteLayerIndex' -count=1`
- `go test ./framework/logstore -run 'TestLogMigrations' -count=1`

## Task 2: Build request-group query primitives with correct filter semantics

**Files:**
- Create: `framework/logstore/request_grouping.go`
- Create: `framework/logstore/request_grouping_test.go`
- Modify: `framework/logstore/tables.go`
- Modify: `framework/logstore/store.go`
- Modify: `framework/logstore/rdb.go`
- Modify: `framework/logstore/matviews.go`
- Modify: `plugins/logging/utils.go`
- Modify: `plugins/logging/operations.go`

- [ ] 在 `SearchStats` 中补齐这些字段：
  - `completed_attempts`
  - `successful_attempts`
  - `completed_request_groups`
  - `successful_request_groups`
  - `request_success_rate`
  - `average_final_latency`
- [ ] 新增最终成功分布响应类型：
  - `value`
  - `dimension`
  - `total_success_count`
  - `items[].label`
  - `items[].success_count`
  - `items[].success_ratio`
- [ ] 扩展 `LogStore` / `LogManager`：
  - `GetFinalSuccessDistribution(ctx, filters, groupBy)`
- [ ] 在 `request_grouping.go` 中拆出三个查询 helper：
  - `rankedAttemptsQuery`：从原始 logs 生成 `group_id / attempt_sequence / is_final_attempt`
  - `filteredAttemptRowsQuery`：给列表与 attempt-level count 使用，先做 rank 再套 attempt-level filters
  - `filteredFinalAttemptsQuery`：给请求级统计与最终分布使用，先确定最终尝试再套 filters
- [ ] 把排序规则写成硬约束，不允许各处各算各的：
  - `attempt_sequence`：`fallback_index asc -> timestamp asc -> id asc`
  - `is_final_attempt` / final attempt：`fallback_index desc -> timestamp desc -> id desc`
- [ ] `SearchLogs` 改为使用专用 projection struct 扫描，再映射回 `[]Log`
- [ ] `FindByID` 也要通过同一套 ranked helper 补齐 `group_id / attempt_sequence / is_final_attempt`，避免详情接口只有列表有元数据
- [ ] projection struct 映射回 `Log` 后显式补 `DeserializeFields()`，不能让现有 JSON 字段解析能力丢失
- [ ] 不再依赖 `gorm:"-"` 指望 `group_id / attempt_sequence / is_final_attempt` 自动填充
- [ ] `GetStats` 拆成两段：
  - raw attempt totals：负责 `total_requests`
  - attempt-level completed stats：Postgres 继续用 matview 快路
  - request-level stats：统一走 `filteredFinalAttemptsQuery`
- [ ] `GetStats` 合并结果时保证：
  - `total_requests` 基于全部 attempts
  - `success_rate` 基于 `successful_attempts / completed_attempts`
  - `request_success_rate` 基于 `successful_request_groups / completed_request_groups`
  - `average_final_latency` 只看最终已完成尝试
- [ ] 新增 `GetFinalSuccessDistribution`，支持：
  - `model`
  - `provider`
  - `key`
  - `layer`
- [ ] `group_by=layer` 时对 `NULL route_layer_index` 归类为 `Unlayered`
- [ ] 增加测试：
  - 请求组 attempt sequence 正确且不受外层 attempt filters 影响
  - `FindByID` 返回的单条日志也带有 group metadata
  - 请求成功率按最终尝试计算
  - 最终成功分布不把中途失败算进去
  - `layer` 分布正确处理 `Layer N` 与 `Unlayered`
  - projection 路径不会丢 `routing_engines_used` / `input_history` 等解析字段
  - Postgres matview merge helper 不丢请求级字段

**验证：**

- `go test ./framework/logstore -run 'TestSearchLogsRequestGrouping_|TestGetStatsRequestLevel_|TestGetFinalSuccessDistribution_' -count=1`

## Task 3: Expose new analytics through HTTP handlers

**Files:**
- Modify: `transports/bifrost-http/handlers/logging.go`
- Create: `transports/bifrost-http/handlers/logging_test.go`

- [ ] 注册新路由：`GET /api/logs/final-distribution`
- [ ] 复用现有 logs filters 解析逻辑，不新增一套口径不同的 query parser
- [ ] 直接抽一个 logs filters parser helper，给 `/api/logs`、`/api/logs/stats`、`/api/logs/final-distribution` 共用
- [ ] `/api/logs/stats` 直接返回新增字段：
  - `completed_attempts`
  - `successful_attempts`
  - `completed_request_groups`
  - `successful_request_groups`
  - `request_success_rate`
  - `average_final_latency`
- [ ] `/api/logs` 和 `/api/logs/{id}` 直接透传：
  - `group_id`
  - `attempt_sequence`
  - `is_final_attempt`
  - `route_layer_index`
- [ ] 明确删除“handler 里 enrich route layer”的旧思路，handler 不负责 layer 推断
- [ ] 在测试中创建可编译的 `stubLogManager`：
  - 提供一个 `noop` 基类满足完整 `logging.LogManager`
  - 针对当前测试重写 `Search / GetLog / GetStats / GetFinalSuccessDistribution`
- [ ] 在测试中同时创建 `stubRedactedKeysManager`，避免 `/api/logs` 与 `/api/logs/{id}` 因 redacted lookups 直接报错
- [ ] 增加 handler 测试：
  - `/api/logs/stats` 返回新字段
  - `/api/logs/final-distribution?group_by=layer` 返回正确结构
  - `/api/logs` 和 `/api/logs/{id}` 的 `route_layer_index` 透传存在

**验证：**

- `go test ./transports/bifrost-http/handlers -run 'TestGetLogsStatsIncludesRequestMetrics|TestGetFinalSuccessDistribution|TestLogsResponsesIncludeGroupingFields' -count=1`

## Task 4: Add frontend contracts and shared grouping helpers

**Files:**
- Modify: `ui/lib/types/logs.ts`
- Modify: `ui/lib/store/apis/logsApi.ts`
- Create: `ui/app/workspace/logs/requestGrouping.ts`
- Create: `ui/app/workspace/logs/requestGrouping.test.ts`

- [ ] 在 `LogEntry` 上补齐：
  - `parent_request_id`
  - `group_id`
  - `attempt_sequence`
  - `is_final_attempt`
  - `route_layer_index`
- [ ] 在 `LogStats` 上补齐：
  - `completed_attempts`
  - `successful_attempts`
  - `completed_request_groups`
  - `successful_request_groups`
  - `request_success_rate`
  - `average_final_latency`
- [ ] 新增 `FinalSuccessDistributionResponse`，其中 item 至少包含 `value / label / success_count / success_ratio`
- [ ] 在 RTK Query 增加 `getFinalSuccessDistribution`
- [ ] 在 `requestGrouping.ts` 提供共享 helper：
  - `canUseGroupedRequestView`
  - `buildVisibleRequestGroups`
  - `formatAttemptRateSummary`
  - `formatRequestRateSummary`
  - `formatLayerLabel`
  - `buildGroupDisplayState`
- [ ] `buildGroupDisplayState` 要能区分：
  - 当前页可见 final attempt
  - final attempt 不在当前页或被 filters 排掉
- [ ] Vitest 覆盖：
  - grouped view gate 只在 `timestamp desc` 返回 `true`
  - visible attempts 可以正确组装成请求组
  - `finalAttemptVisible=false` 时不会冒充最终成功对象
  - success summary 文案与分子分母一致
  - layer label 处理 `null -> Unlayered`

**验证：**

- `npm --prefix ui exec vitest run app/workspace/logs/requestGrouping.test.ts`

## Task 5: Refactor page state flow before touching UI layout

**Files:**
- Modify: `ui/app/workspace/logs/page.tsx`

- [ ] 在 `page.tsx` 中把这几类状态明确拆开：
  - attempt-level 列表数据
  - request-level 统计数据
  - final success distribution 数据
  - grouped view 派生数据
- [ ] 在进入 grouped view 之前先定义完整选取状态：
  - `selectedGroupId`
  - `selectedAttemptId`
  - `sheetMode = request | attempt`
  - `expandedGroupIds`
- [ ] grouped view 只在 `pagination.sort_by === "timestamp" && order === "desc"` 时启用
- [ ] 继续保留原始 flat attempts 数据，grouped rows 作为 derived state 计算
- [ ] 移除 websocket 完成事件里的“本地递增 request stats”逻辑
- [ ] websocket 到来后的策略改为：
  - flat view：即时更新当前页可见 attempts
  - grouped view：节流后重新抓取当前页 `/logs`
  - 两种模式都对 stats / distribution / histogram 做节流后重新拉取
- [ ] 将 `Attempt Volume` 文案明确保留为 attempt-level 趋势
- [ ] 为 grouped detail 维护当前页 request-group context，供详情面板注入
- [ ] 定义 `expandedGroupIds` 的重置规则：
  - filters 变化清空
  - sort/order 变化清空
  - 翻页清空
  - grouped view 退出时清空
- [ ] 页面级别增加 smoke-friendly test ids：
  - `request-success-rate-card`
  - `attempt-success-rate-card`
  - `avg-final-latency-card`
  - `final-success-distribution-card`

**验证：**

- `npm --prefix ui exec vitest run app/workspace/logs/requestGrouping.test.ts`
- `npm --prefix ui run lint`

## Task 6: Add Final Success Distribution card

**Files:**
- Create: `ui/app/workspace/logs/views/finalSuccessDistributionCard.tsx`
- Modify: `ui/app/workspace/logs/page.tsx`

- [ ] 创建独立的 `FinalSuccessDistributionCard`，不要把切换逻辑塞进 `page.tsx`
- [ ] 支持 `model / provider / key / layer` 四个 tab
- [ ] 展示每项：
  - stable value
  - 名称
  - success count
  - success ratio
- [ ] `layer` tab 使用统一 label：
  - `Layer 1`
  - `Layer 2`
  - `Unlayered`
- [ ] 提供 loading / empty / no-success 状态
- [ ] 把卡片放在 `Attempt Volume` 图旁边，形成左右双卡
- [ ] 增加稳定 test ids，供 E2E smoke 使用

**验证：**

- `npm --prefix ui run lint`
- `npm --prefix ui exec vitest run app/workspace/logs/requestGrouping.test.ts`

## Task 7: Build grouped request rows and detail sheet

**Files:**
- Create: `ui/app/workspace/logs/views/requestGroupRows.tsx`
- Modify: `ui/app/workspace/logs/views/logsTable.tsx`
- Modify: `ui/app/workspace/logs/views/columns.tsx`
- Modify: `ui/app/workspace/logs/sheets/logDetailsSheet.tsx`
- Modify: `ui/app/workspace/logs/page.tsx`

- [ ] 保持 `columns.tsx` 仍然负责 attempt row 的字段渲染，不把 grouped summary column 逻辑硬塞进去
- [ ] 明确选择 grouped rows 的实现路线：
  - 继续复用现有 TanStack table + visible columns 渲染每格 group header
  - 不另起一套脱离 column visibility / pinning 的固定版型
- [ ] 新建 `requestGroupRows.tsx`，专门渲染：
  - 组头 summary row
  - 展开/收起按钮
  - attempts 子行
- [ ] `requestGroupRows.tsx` 必须兼容：
  - metadata 动态列
  - actions 列
  - hidden columns
  - pinned columns
- [ ] 组头优先展示：
  - Final Status
  - Last Timestamp
  - Message Summary
  - Final Target
  - Attempts
  - Final Layer
  - Final Latency
- [ ] 当当前页看不到最终尝试时：
  - 组头不要伪造 `Final Target`
  - 显示 `Final attempt outside current filter/page` 提示态
  - 对应详情页进入 `sheetMode=request` 的 degraded path
- [ ] 多次尝试组支持展开查看 attempt chain；单次尝试看起来像普通行
- [ ] 点击规则：
  - chevron 只负责展开/收起
  - 组头正文打开“请求视角”的详情
  - 子行打开“当前 attempt 视角”的详情
- [ ] `LogDetailSheet` 新增三段结构：
  - 请求摘要
  - 尝试时间线
  - 当前 attempt 细节
- [ ] 若没有完整 group context：
  - 显示降级提示
  - 请求摘要只显示当前页可见 attempts 的聚合信息
  - 尝试时间线只渲染当前页可见 attempts，并带 `Partial` 标记
  - 最终目标 / 最终层级 / 最终延迟这类字段显示 `Unavailable in current page`
  - 仍保留当前 attempt 的完整原始信息
- [ ] 保持现有 `selected_log` 导航逻辑可用，但 grouped view 下上下切换以请求组为优先
- [ ] 增加 group-level test ids：
  - `request-group-row`
  - `request-group-chevron`
  - `request-attempt-row`
  - `request-group-partial-badge`

**验证：**

- `npm --prefix ui run lint`
- `npm --prefix ui exec vitest run app/workspace/logs/requestGrouping.test.ts`

## Task 8: Regression, smoke tests, and release check

**Files:**
- Modify: `tests/e2e/features/logs/pages/logs.page.ts`
- Create: `tests/e2e/features/logs/logs-grouping.smoke.spec.ts`

- [ ] E2E 只增加 smoke 断言，不依赖测试环境天然存在 fallback 链
- [ ] 不把现有 `logs.spec.ts` 当作这次功能的 smoke 入口；新功能单独放到 `logs-grouping.smoke.spec.ts`
- [ ] 新增 smoke 覆盖：
  - 双成功率卡存在
  - 最终成功分布卡存在
  - 日志表仍可打开详情
  - 如果页面上存在可展开组，则展开按钮可点、子行可见
- [ ] 增加页面级验证：
  - 非 `timestamp desc` 时会退回 flat view
  - 直接通过 `selected_log` 打开详情但当前页没有完整 request group 时，显示 degraded 提示
- [ ] 增加空态验证：
  - `final-distribution` 无成功结果
  - `By Layer` 只剩 `Unlayered`
- [ ] 对“没有可展开组”的情况使用 skip 或条件断言，不把它算成失败
- [ ] 明确 E2E 前提：
  - 本地 Bifrost API 已在 `localhost:8080` 可用
  - 再执行对应的 Playwright smoke
- [ ] 增加最终人工验收清单：
  - 无 filters 时可以看到 grouped request rows
  - `Request Success Rate` 与 `Attempt Success Rate` 数字口径不同且说得通
  - `By Layer` 可以看出 Layer 1/2/3 或 `Unlayered`
  - 切换 filters 后 stats 与 distribution 会重新拉取
  - 实时更新不会把 request-level cards 本地越算越离谱

**验证：**

- `go test ./framework/logstore -count=1`
- `go test ./plugins/governance -count=1`
- `go test ./plugins/logging -count=1`
- `go test ./transports/bifrost-http/handlers -count=1`
- `npm --prefix ui exec vitest run app/workspace/logs/requestGrouping.test.ts`
- `npm --prefix ui run lint`
- `npm --prefix ui run build`
- `cd tests/e2e && npx playwright test features/logs/logs-grouping.smoke.spec.ts`
- 若要验证 Postgres matview 快路：在带 Postgres 的环境或 CI job 中额外执行对应 logstore 测试与 `/api/logs/stats` 回归

## Self-Review Checklist

- [ ] 设计稿里的 V1 范围都能在任务中找到落点
- [ ] 任务清单里不再出现 handler 侧 layer 反推方案
- [ ] 请求级统计与 attempt-level 列表的筛选语义已明确区分
- [ ] `total_requests` 与 `completed_attempts` 的口径已明确拆开
- [ ] Postgres matview 快路与 raw logs merge 已明确写入任务
- [ ] 前端顺序符合“helper → state flow → live updates → distribution → grouped rows → detail → smoke”
- [ ] E2E 没有承诺依赖固定 fallback 测试数据

## Recommended Execution Order

1. Task 1
2. Task 2
3. Task 3
4. Task 4
5. Task 5
6. Task 6
7. Task 7
8. Task 8

Plan complete and saved to `docs/superpowers/plans/2026-04-15-logs-adaptive-routing-request-grouping-implementation.md`.
