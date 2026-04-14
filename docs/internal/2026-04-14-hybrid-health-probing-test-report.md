# 混合健康探测本地测试报告

- 日期：2026-04-14
- 结论：`通过`
- 最终实验产物：`/Users/fight/Desktop/2026/bifrost/tmp/hybrid-health-probing-lab/20260414-122447`
- 对应测试清单：`/Users/fight/Desktop/2026/bifrost/docs/internal/2026-04-14-hybrid-health-probing-test-plan.md`

## 1. 本次确认的结果

本次针对“被动优先、静默后主动探测”的混合健康探测做了四类确认：

1. 配置与启动正确
   - `config.json` 已可合法声明主动探测配置
   - 本地实验室启动过程中未再出现配置校验报错
   - Governance plugin 能在真实 server bootstrap 后拿到 Bifrost client 并开始后台扫描

2. 混合逻辑正确
   - 最近有被动流量时不会额外主动探测
   - 静默超过新鲜时间后会自动主动探测
   - 主动探测成功会让已进入冷却的主目标恢复参与选路
   - 主动探测失败不会误恢复，后续真实请求仍会跳过异常目标

3. 健康状态正确
   - `/api/governance/health-status` 能返回：
     - `last_observed_at`
     - `last_observed_request_type`
     - `last_observation_source`
   - 观测来源能在 `passive -> active` 之间正确切换

4. 证据链完整
   - 每个场景都保留了 mock 事件、健康状态快照和请求结果
   - 关键恢复/失败场景额外保留了单目标探测后快照，避免最终真实请求覆盖观测来源

## 2. 本次补上的真实阻塞点

### A. `config.schema.json` 未声明主动探测字段

现象：

- 代码已经支持 `active_health_probe_*` 配置
- 但 JSON schema 仍把这些字段当成非法字段
- 结果是正式环境若通过 `config.json` 开启主动探测，会先打出配置校验错误

处理：

- 已把以下字段补入 `transports/config.schema.json`
  - `active_health_probe_enabled`
  - `active_health_probe_interval_seconds`
  - `active_health_probe_passive_freshness_seconds`
  - `active_health_probe_timeout_seconds`
  - `active_health_probe_max_concurrency`
- 已补针对性校验测试，确认 schema 与本次配置一致

结果：

- 本次实验室启动日志中未再出现 `config validation failed`
- `go test ./transports/bifrost-http/lib -run TestValidateConfigSchema_GovernancePlugin_ActiveProbeConfigValid -count=1` 通过

## 3. 场景结果

| 场景 | 结果 | 关键证据 |
|---|---|---|
| `passive_freshness_skips_probe` | 通过 | `site-a-ok` 仅命中 1 次，无无用户探测事件 |
| `stale_target_triggers_active_probe` | 通过 | `site-a-ok` 共命中 2 次，其中 1 次为无用户主动探测 |
| `active_probe_success_recovers_cooldown` | 通过 | 主目标探测后快照为 `available + active`，后续真实请求重新命中主目标 |
| `active_probe_failure_keeps_cooldown` | 通过 | 主目标探测后快照为 `cooldown + active`，后续真实请求继续走备用目标 |
| `health_status_observation_fields` | 通过 | 同一目标先出现 `passive` 快照，再出现 `active` 快照，且请求类型一致 |

## 4. 代表性证据

### 被动优先成立

文件：

- `/Users/fight/Desktop/2026/bifrost/tmp/hybrid-health-probing-lab/20260414-122447/evidence/passive_freshness_skips_probe/health.json`

可见内容：

- `last_observation_source = passive`
- 同场景 `mock_events.json` 里只有 1 次真实请求

### 静默后主动探测成立

文件：

- `/Users/fight/Desktop/2026/bifrost/tmp/hybrid-health-probing-lab/20260414-122447/evidence/stale_target_triggers_active_probe/health.json`

可见内容：

- `last_observation_source = active`
- `last_observed_request_type = chat_completion`

### 主动探测成功可恢复主目标

文件：

- `/Users/fight/Desktop/2026/bifrost/tmp/hybrid-health-probing-lab/20260414-122447/evidence/active_probe_success_recovers_cooldown/after_probe_target.json`
- `/Users/fight/Desktop/2026/bifrost/tmp/hybrid-health-probing-lab/20260414-122447/evidence/active_probe_success_recovers_cooldown/final_events.json`

可见内容：

- 探测后单目标快照：`available`
- 探测后观测来源：`active`
- 后续真实请求重新命中 `site-d-hardfail`

### 主动探测失败不会误恢复

文件：

- `/Users/fight/Desktop/2026/bifrost/tmp/hybrid-health-probing-lab/20260414-122447/evidence/active_probe_failure_keeps_cooldown/after_probe_target.json`
- `/Users/fight/Desktop/2026/bifrost/tmp/hybrid-health-probing-lab/20260414-122447/evidence/active_probe_failure_keeps_cooldown/final_events.json`

可见内容：

- 探测后单目标快照：`cooldown`
- 探测后观测来源：`active`
- 后续真实请求只命中 `site-a-ok`

## 5. 回归验证

已执行并通过：

```bash
go test ./transports/bifrost-http/lib -run TestValidateConfigSchema_GovernancePlugin_ActiveProbeConfigValid -count=1
go test ./plugins/governance -count=1
go test ./transports/bifrost-http/handlers -run TestGetHealthStatus_GroupedRulesAreRuleScoped -count=1
git diff --check
./tests/manual/hybrid-health-probing-lab/run_lab.sh
```

## 6. 当前判断

- 这次混合健康探测在本地“接近真实运行”的条件下已经满足递交标准
- 新发现的真实阻塞点只有 schema 漏字段，已修正并验证
- 剩余未在本次 lab 里展开的点主要是：
  - 多规则共享同一 target 的主动探测扇出
  - 多 request type（responses / text completion）的主动探测

这两类没有留在手工实验室里，但已有自动化测试覆盖核心逻辑，不构成本次递交阻塞
