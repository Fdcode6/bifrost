# 分组健康路由本地测试报告

- 日期：2026-04-13
- 结论：`通过`
- 最终实验产物：`/Users/fight/Desktop/2026/bifrost/tmp/grouped-routing-lab/20260413-225243`
- 对应测试清单：`/Users/fight/Desktop/2026/bifrost/docs/internal/2026-04-13-grouped-health-routing-test-plan.md`

## 1. 本次确认的结果

本次针对 grouped health routing 做了三类确认：

1. 路由行为正确
   - 同请求内失败 target 不会再次命中
   - 同组可切换时优先留在当前组
   - 当前组不可用时会进入备用组
   - 全链路都失败时会清晰返回错误

2. 健康状态正确
   - 失败达到阈值后进入冷却
   - 冷却期间会被跳过
   - 冷却结束后会重新尝试原 target
   - 健康状态按 `provider + model + key_id + rule` 隔离

3. 可观测性正确
   - 健康状态 API 可返回规则级状态
   - `/api/logs` 列表接口可返回 `routing_engine_logs`
   - 实验室证据中已能看到每次请求的智能路由过程

## 2. 本次修正的关键问题

### A. fallback 的 `key_id` 计划没有真正落到底层执行

现象：

- fallback 链路表面上保存了 `FallbackKeyIDs`
- 但 fallback 真实执行时，`key_id` 恢复动作放在 plugin pre-hook
- 该阶段对保留键写入会被静默拦掉
- 结果就是后续 fallback 会重新随机选 key，导致站点身份漂移

修正：

- 新增通用上下文键：`BifrostContextKeyFallbackKeyIDs`
- 在 grouped routing 命中后，把 fallback `key_id` 计划写进 context
- 由 core fallback loop 在 `clearCtxForFallback()` 之后、真正发起 fallback 之前恢复 `APIKeyID`
- 保留 governance 自身的当前 attempt pin，用于健康记录

结果：

- `same_request_no_repeat`
- `cross_group_failover`
- `timeout_failover`
- `pressure_mixed_failures`

以上原本失败或不稳定的场景全部恢复正常

### B. 冷却计时起点错误，导致“恢复”晚一拍

现象：

- 失败达到阈值后，如果中间没有立即再来一次请求做评估
- 冷却不会从“最后一次失败时刻”开始算
- 而是等到下一次路由评估时才开始算
- 结果就是睡了 31 秒后再发请求，目标反而刚开始进入冷却

修正：

- 健康评估改为按 `lastFailureTime` 计算冷却起点
- 即使冷却是懒评估触发，也不会把冷却窗口错误地往后顺延
- 如果下一次评估时冷却本应已经结束，则立即恢复为可用

结果：

- `cooldown_recovery` 从失败转为通过
- 恢复后会重新命中原主站点

### C. `/api/logs` 列表接口漏掉了 `routing_engine_logs`

现象：

- 数据库里已有路由明细
- 但日志列表接口的查询列没有把 `routing_engine_logs` 取出来
- 页面和测试取证都会看到空字符串

修正：

- `framework/logstore/rdb.go` 的 `listSelectColumns()` 已补入 `routing_engine_logs`
- 实验室取证脚本同时补了日志可见性等待，避免异步落库时过早读取

结果：

- 最新实验产物里，`cross_group_failover/logs.json` 已可直接看到完整路由明细

## 3. 场景结果

| 场景 | 结果 | 关键证据 |
|---|---|---|
| `baseline_primary_success` | 通过 | 只命中 `site-a-ok` 1 次 |
| `same_request_no_repeat` | 通过 | `site-d-hardfail` 1 次，`site-a-ok` 1 次 |
| `cross_group_failover` | 通过 | 主组失败后命中 `site-f-recover`，并带有 route logs |
| `cooldown_skip` | 通过 | `site-d-hardfail-id` 进入 `cooldown`，第三次请求跳过 |
| `cooldown_recovery` | 通过 | 等待 31 秒后重新命中 `site-d-hardfail` 1 次，状态恢复为 `available` |
| `timeout_failover` | 通过 | `site-e-timeout` 1 次，随后 `site-f-recover` 1 次 |
| `final_group_rescue` | 通过 | 前两组失败，最终 `site-b-slow` 成功 |
| `pressure_mixed_failures` | 通过 | `30/30` 成功，未发现同请求重复命中失败站点 |
| `all_groups_down_boundary` | 通过 | 三组都失败时返回错误，链路可解释 |

## 4. 代表性证据

### 路由明细已可见

文件：

- `/Users/fight/Desktop/2026/bifrost/tmp/grouped-routing-lab/20260413-225243/evidence/cross_group_failover/logs.json`

可见内容：

- 规则命中：`lab-cross-group`
- 主组选择：`site-d-hardfail-id`
- 备用组 fallback：`site-f-recover-id`

### 恢复路径已生效

文件：

- `/Users/fight/Desktop/2026/bifrost/tmp/grouped-routing-lab/20260413-225243/results.json`
- `/Users/fight/Desktop/2026/bifrost/tmp/grouped-routing-lab/20260413-225243/evidence/cooldown_recovery/mock_events.json`

可见内容：

- `cooldown_recovery.passed = true`
- 恢复后 `site-d-hardfail` 命中 1 次
- 健康状态中 `site-d-hardfail-id = available`

### 压力场景满足“不重复打失败 target”

文件：

- `/Users/fight/Desktop/2026/bifrost/tmp/grouped-routing-lab/20260413-225243/evidence/pressure_mixed_failures/duplicate_violations.json`
- `/Users/fight/Desktop/2026/bifrost/tmp/grouped-routing-lab/20260413-225243/results.json`

可见内容：

- 并发 10，总请求 30，成功 30
- 未发现同请求重复命中失败站点

## 5. 回归验证

已执行并通过：

```bash
go test ./core -run 'TestRestoreFallbackAPIKeyIDFromContext|TestExecuteRequestWithRetries_SuccessScenarios/GroupedRoutingDisablesProviderRetries' -count=1
go test ./framework/logstore -run 'TestListSelectColumns_IncludesRoutingEngineLogs' -count=1
go test ./plugins/governance/... -count=1
go test ./transports/bifrost-http/handlers/... -count=1
go test ./transports/schema_test/... -count=1
go build ./core/... ./framework/... ./plugins/governance/... ./transports/...
./tests/manual/grouped-routing-lab/run_lab.sh
```

## 6. 额外说明

- `go test ./core/... -count=1` 仍会遇到现有失败：`TestHandleProviderRequest_OCROperationNotAllowed`
- 该失败与本次 grouped health routing 改动无关，本次未修改其相关逻辑
- 本报告的通过结论基于本功能直接相关的回归命令、handler/schema 验证，以及整套实验室场景实测
