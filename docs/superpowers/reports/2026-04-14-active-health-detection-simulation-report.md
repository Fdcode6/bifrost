# Active Health Detection 仿真测试报告

日期：2026-04-14

## 1. 结论

本次改动已经完成，并且经过两层仿真验证：

- Docker 部署形态验证
- 主动健康探测真实场景实验验证

本轮结果为：

- 配置读取正常
- 配置保存正常
- 页面展示正常
- 保存后立即生效
- 重启后配置保留
- 主动探测五个关键场景全部通过

当前可以进入真机试运行。

## 2. 测试范围

### 2.1 Docker 部署形态验证

验证目标：

- 本地 Docker 镜像可正常启动
- `/api/governance/health-detection-config` 可读可写
- `Adaptive Routing` 页面能看到新的设置区
- 页面点击保存后，后端配置实际更新
- 容器重启后，配置仍然保留

### 2.2 主动健康探测行为验证

验证目标：

- 被动新鲜窗口内不误触发主动探测
- 超过新鲜时间后能触发主动探测
- 主目标进入 cooldown 后，主动探测成功可恢复
- 主目标进入 cooldown 后，主动探测失败会继续保持 cooldown
- 健康状态接口返回 `passive` / `active` 观测来源和请求类型

## 3. Docker 仿真结果

测试环境：

- 镜像：`bifrost:latest`
- 端口：`18084`
- config store：SQLite
- logs store：SQLite

实际结果：

| 项目 | 结果 |
| --- | --- |
| 容器启动 | 通过 |
| `/health` | 返回 `200` |
| Docker health | 最终 `healthy` |
| 健康检测配置读取 | 通过 |
| 健康检测配置保存 | 通过 |
| 页面展示设置卡 | 通过 |
| 页面保存后 toast 提示 | 通过 |
| 页面模式标签同步更新 | 通过 |
| 容器重启后配置保留 | 通过 |

关键观察：

- 初始读取返回：
  - `mode=hybrid`
  - `interval=1`
  - `passive_freshness=2`
  - `timeout=1`
  - `max_concurrency=2`
- 页面切换到 `Passive only` 后，保存成功
- 保存后接口返回 `mode=passive`
- 容器重启后再次读取，仍为 `mode=passive`

## 4. 主动探测实验结果

实验输出目录：

- `/Users/fight/Desktop/2026/bifrost/tmp/hybrid-health-probing-lab/20260414-172416`

汇总结果：

| 场景 | 结果 | 耗时 |
| --- | --- | --- |
| `passive_freshness_skips_probe` | 通过 | 1589 ms |
| `stale_target_triggers_active_probe` | 通过 | 2713 ms |
| `active_probe_success_recovers_cooldown` | 通过 | 3027 ms |
| `active_probe_failure_keeps_cooldown` | 通过 | 2815 ms |
| `health_status_observation_fields` | 通过 | 3096 ms |

### 4.1 Passive Freshness

结论：

- 最近有真实流量时，不会多打一枪主动探测

观察结果：

- 总命中 1 次
- 无用户主动探测命中 0 次
- 最后观测来源 `passive`

### 4.2 Stale Target Triggers Active Probe

结论：

- 超过被动新鲜时间后，会触发主动探测

观察结果：

- 无用户主动探测命中 1 次
- 最后观测来源 `active`
- 最后观测请求类型 `chat_completion`

### 4.3 Active Probe Success Recovers Cooldown

结论：

- 主目标故障进入 cooldown 后，如果主动探测成功，会恢复为可用

观察结果：

- 冷却前状态 `cooldown`
- 主动探测后状态 `available`
- 恢复后真实请求重新命中主目标 1 次

### 4.4 Active Probe Failure Keeps Cooldown

结论：

- 主目标故障进入 cooldown 后，如果主动探测仍失败，会继续保持 cooldown

观察结果：

- 失败后最后观测来源 `active`
- 失败后状态 `cooldown`
- 后续真实请求命中备用目标 1 次

### 4.5 Health Status Observation Fields

结论：

- 健康状态接口已经能正确返回观测元数据

观察结果：

- 被动观测来源 `passive`
- 主动观测来源 `active`
- 最后观测请求类型 `chat_completion`

## 5. 产物与证据

实验明细文件位于：

- `/Users/fight/Desktop/2026/bifrost/tmp/hybrid-health-probing-lab/20260414-172416/results.json`

每个场景都保留了原始证据，包括：

- request 结果
- mock server 命中记录
- health status 返回
- cooldown 恢复前后状态

## 6. 这轮测试中顺手修掉的问题

本轮仿真发现并修正了一个 Docker 打包问题：

- 镜像内未携带本地 `config.schema.json`
- 导致容器在 `config.json` 中使用主动探测字段时，退回到线上旧 schema 做校验，启动时出现误报警告

修正后结果：

- 容器启动不再出现这条 schema 校验警告
- Docker health 最终恢复为 `healthy`

## 7. 最终判断

当前状态：

- 功能完成
- 仿真通过
- Docker 镜像已更新

建议：

- 可以进入真机试运行
- 第一轮真机观察重点放在：
  - `Adaptive Routing` 页面里的 `Detection Mode`
  - `Source` 字段是否在 `passive` / `active` 之间合理变化
  - 主目标故障恢复后是否重新回到主路由
