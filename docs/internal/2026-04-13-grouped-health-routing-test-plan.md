# 分组健康路由本地验证清单

- 日期：2026-04-13
- 目标：用本地可控假站点，完整验证 grouped health routing 的成功路径、跳过异常、冷却恢复、切组、日志可见性与并发行为
- 产出：
  - 可复用假接口：`/Users/fight/Desktop/2026/bifrost/tests/manual/grouped-routing-lab/mock_openai_server.go`
  - 一键执行脚本：`/Users/fight/Desktop/2026/bifrost/tests/manual/grouped-routing-lab/run_lab.sh`
  - 执行程序：`/Users/fight/Desktop/2026/bifrost/tests/manual/grouped-routing-lab/run_lab.go`
  - 本次运行报告：`/Users/fight/Desktop/2026/bifrost/docs/internal/2026-04-13-grouped-health-routing-test-report.md`

## 1. 验证范围

本次验证覆盖以下能力：

- 组内目标失败后，本请求内不会再次打到同一站点
- 当前组有其它可用目标时，只在当前组内切换
- 当前组不可用时会切到备用组
- 失败达到阈值后会进入冷却，后续请求会直接跳过异常站点
- 冷却结束后目标会恢复参与选择
- 超时路径会按规则切换
- 多个分组都失败前，最终兜底组仍可产出结果
- 压力场景下仍保持成功返回与不重复打失败站点
- `Adaptive Routing` 对应的健康状态 API 能反映规则级健康状态
- `Logs` 中的 `Routing Decision Logs` 能看到本次智能路由过程

## 2. 测试环境

- Bifrost：本地临时 app dir，SQLite config store + SQLite logs store
- Logging：开启
- Provider：1 个 custom provider，名称 `mock-lab`，base provider type 为 `openai`
- 上游：1 个本地 OpenAI-compatible 假接口，通过不同 API key 模拟多个中转站
- Provider 网络配置：
  - `base_url` 指向本地假接口
  - `default_request_timeout_in_seconds = 2`
  - `max_retries = 2`
  - 目的是主动验证 grouped routing 已禁止“同请求内对刚失败站点做原地重试”

## 3. 站点角色

使用 6 把 key 固定代表 6 个站点角色：

| Key ID | 站点代号 | 用途 |
|---|---|---|
| `site-a-ok-id` | `site-a-ok` | 稳定成功 |
| `site-b-slow-id` | `site-b-slow` | 慢成功，作为最终兜底 |
| `site-c-flaky-id` | `site-c-flaky` | 可做波动场景 |
| `site-d-hardfail-id` | `site-d-hardfail` | 固定错误 |
| `site-e-timeout-id` | `site-e-timeout` | 固定超时 |
| `site-f-recover-id` | `site-f-recover` | 可做恢复场景 |

说明：

- 健康粒度按 `provider + model + key_id`
- grouped target 一律显式填写 `provider` 与 `model`

## 4. 执行场景

### 场景 A：主站点直接成功

- 目的：确认基础成功路径成立
- 预期：
  - 请求成功
  - 只命中主站点一次
  - 日志中能看到规则命中与主路由选择

### 场景 B：同请求内不重复打刚失败的站点

- 目的：验证“某 target 一旦失败，本请求内不再重复尝试”
- 设计：
  - 同一组内第一个站点固定失败
  - 同组第二个站点成功
  - provider `max_retries = 2`
- 预期：
  - 失败站点只被打 1 次
  - 同请求切到本组其他站点成功

### 场景 C：主组失败后切备用组

- 目的：验证跨组切换
- 预期：
  - 主组目标失败
  - 备用组目标成功
  - 最终返回成功

### 场景 D：触发冷却并在冷却期跳过异常站点

- 目的：验证 `2 次 / 30 秒 / 冷却 30 秒`
- 设计：
  - 同一站点连续失败两次
  - 第三次请求检查是否直接跳过
- 预期：
  - 健康状态中该站点为 `cooldown`
  - 第三次请求不再命中该站点

### 场景 E：冷却结束后恢复可用

- 目的：验证自动恢复
- 设计：
  - 先触发冷却
  - 等待 31 秒
  - 把该站点切回成功
- 预期：
  - 再次请求会重新尝试该站点
  - 成功后恢复正常服务

### 场景 F：超时后切换

- 目的：验证 timeout 路径
- 设计：
  - 主站点故意超过 provider 超时时间
  - 备用站点成功
- 预期：
  - 请求最终成功
  - 超时站点只命中一次

### 场景 G：前两组都不可用，最终兜底组产出结果

- 目的：验证多组兜底
- 预期：
  - 前两组失败
  - 最终组慢成功
  - 整体仍返回结果

### 场景 H：中等并发压力下保持成功与正确切换

- 目的：验证并发下的行为正确性
- 设计：
  - 30 个请求，10 并发
  - 主站点固定失败，同组备用站点成功
- 预期：
  - 全部请求成功
  - 任一请求都不会对同一失败站点重复命中
  - 冷却生效后，后续请求直接跳过异常站点

### 场景 I：全链路都失败时的边界行为

- 目的：确认系统边界，避免误解为绝对不会报错
- 预期：
  - 当所有分组都失败时，请求仍会返回错误
  - 日志中能明确看出尝试链路

## 5. 取证方式

每个场景都保留以下证据：

- Bifrost API 请求结果
- 本地假接口收到的事件明细
- 健康状态 API 快照
- 日志 API 返回的 `routing_engine_logs`

证据目录由执行脚本自动生成，默认放到：

- `/Users/fight/Desktop/2026/bifrost/tmp/grouped-routing-lab/<timestamp>/`

## 6. 通过标准

本次验证视为通过，需要同时满足：

- A 到 H 场景全部通过
- I 场景按预期失败，且失败原因清晰可解释
- 压力场景成功率为 100%
- 压力场景中不存在“同一请求对同一失败站点重复命中”的证据
- 健康状态 API 与日志取证结果一致

## 7. 执行命令

默认执行命令：

```bash
./tests/manual/grouped-routing-lab/run_lab.sh
```

可选端口覆盖：

```bash
MOCK_PORT=19101 BIFROST_PORT=18080 ./tests/manual/grouped-routing-lab/run_lab.sh
```
