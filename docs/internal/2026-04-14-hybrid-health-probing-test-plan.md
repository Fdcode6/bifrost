# 混合健康探测本地验证清单

- 日期：2026-04-14
- 目标：用本地可控假上游，验证“被动优先、静默后主动探测”的混合健康探测能否按真实运行方式稳定工作
- 产出：
  - 一键执行脚本：`/Users/fight/Desktop/2026/bifrost/tests/manual/hybrid-health-probing-lab/run_lab.sh`
  - 执行程序：`/Users/fight/Desktop/2026/bifrost/tests/manual/hybrid-health-probing-lab/run_lab.go`
  - 复用假接口：`/Users/fight/Desktop/2026/bifrost/tests/manual/grouped-routing-lab/mock_openai_server.go`
  - 本次运行报告：`/Users/fight/Desktop/2026/bifrost/docs/internal/2026-04-14-hybrid-health-probing-test-report.md`

## 1. 验证范围

本次验证覆盖以下能力：

- `config.json` 可合法声明主动探测配置并正常启动
- 最近有被动流量时，不会额外触发主动探测
- 超过被动新鲜时间后，会自动触发主动探测
- 主动探测成功后，已进入冷却的主目标会恢复参与选路
- 主动探测失败后，异常目标仍会继续被跳过
- `/api/governance/health-status` 能返回最后观测时间、请求类型、观测来源
- 主动探测走真实 Bifrost client，不依赖测试专用捷径

## 2. 测试环境

- Bifrost：本地临时 app dir，SQLite config store + SQLite logs store
- Logging：开启
- Provider：1 个 custom provider，名称 `mock-lab`，base provider type 为 `openai`
- 上游：1 个本地 OpenAI-compatible 假接口，通过不同 API key 模拟不同站点
- Governance plugin：通过 `config.json` 的 `plugins` 配置启用主动探测

建议配置：

| 项目 | 值 | 目的 |
|---|---|---|
| `active_health_probe_enabled` | `true` | 打开后台主动探测 |
| `active_health_probe_interval_seconds` | `1` | 缩短实验等待时间 |
| `active_health_probe_passive_freshness_seconds` | `2` | 明确区分“新鲜”与“静默” |
| `active_health_probe_timeout_seconds` | `1` | 让失败/超时场景更快收敛 |
| `active_health_probe_max_concurrency` | `2` | 保持与真实调度一致，但避免实验过度并发 |

## 3. 站点角色

沿用上一轮实验室的 6 个 key 角色：

| Key ID | 站点代号 | 用途 |
|---|---|---|
| `site-a-ok-id` | `site-a-ok` | 稳定成功，主用健康站点 |
| `site-b-slow-id` | `site-b-slow` | 慢成功，备用健康站点 |
| `site-c-flaky-id` | `site-c-flaky` | 预留波动场景 |
| `site-d-hardfail-id` | `site-d-hardfail` | 固定错误 |
| `site-e-timeout-id` | `site-e-timeout` | 固定超时 |
| `site-f-recover-id` | `site-f-recover` | 可从失败切回成功 |

## 4. 执行场景

### 场景 A：最近有被动流量时不主动探测

- 目的：确认“被动优先”成立
- 设计：
  - 先发一笔真实请求，让主站点留下被动观测
  - 在主动探测扫描周期内等待，但不超过被动新鲜时间
- 预期：
  - 假上游只收到 1 次真实请求
  - 不会出现额外的无用户请求探测
  - 健康状态中的 `last_observation_source = passive`

### 场景 B：静默超过新鲜时间后自动主动探测

- 目的：确认“静默补探测”成立
- 设计：
  - 先发一笔真实请求
  - 等待超过 `passive_freshness`
- 预期：
  - 假上游会额外收到 1 次无用户请求的探测
  - 健康状态中的 `last_observation_source = active`
  - `last_observed_request_type = chat_completions`

### 场景 C：主动探测成功后清除冷却并恢复主目标

- 目的：确认恢复路径成立
- 设计：
  - 主目标连续失败两次，触发冷却
  - 站点切回成功后不发用户流量，等待后台主动探测
  - 再发一笔真实请求
- 预期：
  - 冷却期内后台会主动探测该主目标
  - 主动探测成功后，下一笔真实请求重新命中主目标
  - 健康状态中主目标回到 `available`

### 场景 D：主动探测失败后仍继续跳过异常目标

- 目的：确认失败路径不会误恢复
- 设计：
  - 主目标先进入冷却
  - 保持站点持续失败
  - 等待后台主动探测
  - 再发一笔真实请求
- 预期：
  - 后台会再次探测该异常目标
  - 下一笔真实请求仍直接走备用目标
  - 健康状态中的最后观测来源更新为 `active`

### 场景 E：健康状态接口返回观测元数据

- 目的：确认页面和 API 可见性成立
- 设计：
  - 至少跑过一次被动观测和一次主动观测
- 预期：
  - `/api/governance/health-status` 中存在：
    - `last_observed_at`
    - `last_observed_request_type`
    - `last_observation_source`
  - 返回值和 mock 事件时间线一致

## 5. 取证方式

每个场景都保留以下证据：

- Bifrost API 请求结果
- 本地假接口收到的事件明细
- 健康状态 API 快照
- 如有需要，附带日志 API 返回内容

证据目录默认放到：

- `/Users/fight/Desktop/2026/bifrost/tmp/hybrid-health-probing-lab/<timestamp>/`

## 6. 通过标准

本次验证视为通过，需要同时满足：

- 场景 A 到 E 全部通过
- 启动过程中不再出现主动探测配置的 schema 校验失败
- 所有主动探测都只出现在“无用户请求”的静默窗口里
- 主动探测成功与失败的结果，和后续真实选路行为一致
- 健康状态接口返回的观测元数据与 mock 证据一致

## 7. 执行命令

默认执行命令：

```bash
./tests/manual/hybrid-health-probing-lab/run_lab.sh
```

可选端口覆盖：

```bash
MOCK_PORT=19111 BIFROST_PORT=18090 ./tests/manual/hybrid-health-probing-lab/run_lab.sh
```
