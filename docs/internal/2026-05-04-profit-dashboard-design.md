# 利润统计面板设计

状态：设计草案，待确认  
日期：2026-05-04  
范围：新增一个独立后台菜单面板，用中文展示和配置利润统计；不改现有价格覆盖、智能路由、日志清理的核心语义。

## 0. 目标

新增一个独立页面，例如 `/workspace/profit`，菜单显示为“利润统计”。这个页面负责两件事：

1. 配置我们的出售价格，默认输入 `$2 / 100 万 input tokens`、输出 `$12 / 100 万 output tokens`。
2. 查看按当前真实请求沉淀出来的收入、成本、利润、毛利率，支持今日、昨日、最近 7 日、全部累计。
3. 查看按中转站和模型聚合的节点利润明细，用来定位哪个节点利润高、哪个节点成本缺失或毛利偏低。

核心约束：

- 售价保存后只影响之后的新请求。
- 历史利润不因为后续改售价而变化。
- 利润数据不跟随日志清空一起清空。
- 页面文案使用中文，但保留必要术语，例如 `input tokens`、`output tokens`、`USD`、`API`。

本版本不做：

- 不计算 MCP tool logs 的利润；MCP 工具日志只有执行成本，没有稳定的销售 token 口径。
- 不新增供应商成本价格体系；供应商成本继续来自现有模型库价格和 Provider `pricing_overrides`。
- 不做客户、团队、虚拟 key 的分账结算；先做全局经营利润视图。
- 不做历史售价重算；如未来需要，必须设计成明确的危险操作。

## 1. 现有基础

当前日志表已经有利润计算需要的大部分字段：

- token：`prompt_tokens`、`completion_tokens`、`total_tokens`、`cached_read_tokens`
- 成本：`cost`，单位是美元
- 维度：`provider`、`model`、`selected_key_id`、`selected_key_name`、`virtual_key_id`、`virtual_key_name`、`routing_rule_id`、`routing_rule_name`
- 状态：`status`，目前有 `success`、`error`、`processing`
- 时间：`timestamp`

现有“清空日志”只清理 `logs` 和 `mcp_tool_logs`，所以利润数据可以放在独立表中，避免被清空。

## 2. 推荐方案

采用“利润事件表 + 当前售价设置表”的方案。

### 为什么不直接查日志实时计算

直接查日志实时算最省代码，但有两个硬伤：

1. 日志清空后利润也没了，不符合“利润长期保存”。
2. 出售价格变动后，历史利润会被新价格重算，不符合“历史不用变动”。

### 为什么不把利润塞回日志表

不推荐改日志表承担利润长期账本。日志表本来可以被清空，也有大量请求详情字段；利润是经营数据，生命周期和日志不同，应该隔离。

### 最终选择

新增独立表：

- `profit_settings`：保存当前出售价格。
- `profit_events`：保存每条请求在完成时计算出来的收入、成本、利润。

每条 `profit_events` 都保存“当时使用的出售价格”，所以后面改售价不会影响旧数据。

## 3. 业务口径

### 3.1 计算公式

供应商成本来源沿用现有日志里的 `cost` 字段。这个字段已经由模型库价格和 Provider `pricing_overrides` 计算出来，利润模块只消费结果，不重新判断中转站充值比例或模型成本。

收入：

```text
revenue_usd =
  prompt_tokens * sell_input_per_1m_usd / 1,000,000
  + completion_tokens * sell_output_per_1m_usd / 1,000,000
```

成本：

```text
cost_usd = logs.cost，如果为空则按 0 处理，并计入“缺失成本”诊断
```

利润：

```text
profit_usd = revenue_usd - cost_usd
```

毛利率：

```text
gross_margin = profit_usd / revenue_usd
```

如果 `revenue_usd = 0`，毛利率显示为 `—`，不显示 0%，避免误导。

### 3.2 成功、失败、重试的口径

| 请求结果 | 收入 | 成本 | 利润口径 |
|---|---:|---:|---|
| 最终成功请求 | 按 token 和出售价格计算 | 按日志 cost | 正常利润 |
| 失败请求但 provider 已产生 cost | 0 | 按日志 cost | 负利润 |
| 失败请求且没有 cost | 0 | 0 | 只计入失败次数 |
| processing 未完成 | 不入账 | 不入账 | 等状态落定 |

这样可以反映真实经营结果：用户只为成功结果付费；但 provider 失败时如果已经扣费，仍然算作成本。

### 3.3 多次 fallback 的口径

一次用户请求如果经过多个尝试：

- 成功的最终尝试产生收入。
- 所有已经产生 `cost` 的尝试都算成本。
- 因此一次用户请求可能对应多条 `profit_events`，聚合时收入只来自成功结果，成本来自所有有成本的尝试。

这和现有日志模型一致，也能真实反映“某个中转站失败后又切到兜底”的额外成本。

### 3.4 节点利润明细

节点利润明细按 `provider + model` 聚合，不再继续拆虚拟 key、客户或路由规则，保持页面简单可读。

每个节点展示：

- 请求数、成功数、失败数
- 输入 tokens、输出 tokens、总 tokens
- 收入、成本、利润、毛利率
- 缺失成本次数、缺失 token 次数

排序默认按收入从高到低，方便优先检查主力节点。

## 4. 数据模型

### 4.1 `profit_settings`

保存当前生效的出售价格。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 固定为 `default` |
| `sell_input_per_1m_usd` | decimal/float | 输入出售价格，美元 / 100 万 input tokens |
| `sell_output_per_1m_usd` | decimal/float | 输出出售价格，美元 / 100 万 output tokens |
| `timezone` | string | 默认 `Asia/Shanghai` |
| `created_at` | time | 创建时间 |
| `updated_at` | time | 更新时间 |

默认值：

```json
{
  "sell_input_per_1m_usd": 2,
  "sell_output_per_1m_usd": 12,
  "timezone": "Asia/Shanghai"
}
```

### 4.2 `profit_events`

每条日志对应一条利润事件，使用 `log_id` 做幂等主键。

| 字段 | 类型 | 说明 |
|---|---|---|
| `log_id` | string | 主键，对应 `logs.id` |
| `timestamp` | time | 请求时间 |
| `business_day` | date/string | 按 `Asia/Shanghai` 换算后的业务日期 |
| `provider` | string | Provider |
| `model` | string | 模型 |
| `selected_key_id` | string | key id |
| `selected_key_name` | string | key 名称 |
| `virtual_key_id` | string nullable | 虚拟 key |
| `virtual_key_name` | string nullable | 虚拟 key 名称 |
| `routing_rule_id` | string nullable | 路由规则 |
| `routing_rule_name` | string nullable | 路由规则名称 |
| `status` | string | success / error |
| `prompt_tokens` | int | 输入 tokens |
| `completion_tokens` | int | 输出 tokens |
| `total_tokens` | int | 总 tokens |
| `cached_read_tokens` | int | 缓存读取 tokens |
| `cost_usd` | decimal/float | provider 成本 |
| `revenue_usd` | decimal/float | 销售收入 |
| `profit_usd` | decimal/float | 利润 |
| `sell_input_per_1m_usd` | decimal/float | 计算该事件时使用的输入售价 |
| `sell_output_per_1m_usd` | decimal/float | 计算该事件时使用的输出售价 |
| `missing_cost` | bool | 日志 cost 是否缺失 |
| `missing_tokens` | bool | 成功请求是否缺 token |
| `created_at` | time | 创建时间 |
| `updated_at` | time | 更新时间 |

建议索引：

- `idx_profit_events_business_day`
- `idx_profit_events_timestamp`
- `idx_profit_events_provider_model`
- `idx_profit_events_routing_rule`

## 5. 写入链路

### 5.1 正常写入

日志插件在写入日志成功后，调用利润服务：

```text
日志完成写入
  → 读取当前 profit_settings
  → 根据 log status / tokens / cost 计算收入、成本、利润
  → Upsert profit_events(log_id)
```

使用 upsert 的原因：

- 避免重复写入。
- 大响应或流式响应可能先写日志、后补 token，用 upsert 可以更新同一条利润事件。

### 5.2 Deferred usage 更新

当前日志插件存在“先写日志，后异步补 token”的路径。利润功能必须覆盖这条路径：

```text
deferred usage 更新 logs.prompt_tokens / completion_tokens 成功
  → 再次调用 UpsertProfitEventFromLog
  → 更新同一条 profit_events
```

这样可以避免流式或大响应请求一开始 token 为空，导致利润被算低。

### 5.3 手动回填

新增一个手动操作：“从现有日志回填利润”。

用途：

- 新功能上线后，把当前还没有清理的日志补进利润表。
- 修复历史上因为 token 延迟更新导致的利润事件不完整。
- 修复历史上因为利润同步失败导致的 `logs` 有记录、`profit_events` 缺记录的问题。

口径：

- 回填只处理当前 `logs` 表里还存在的记录。
- 回填时使用当前出售价格。
- 回填优先且只处理缺失 `profit_events` 的日志，按 `logs.timestamp ASC` 分批推进，避免多次回填反复处理同一批早期日志。
- 已经存在的 `profit_events` 不通过回填刷新；token / cost / 维度字段的同步由正常日志写入、deferred usage 更新和成本重算链路负责。如果未来需要“强制重算”，另做明确危险操作。

### 5.4 定时完整性校对

为避免偶发写入失败、镜像时区数据缺失、进程重启等问题造成利润账本缺口，日志插件启动后会自动运行轻量校对：

```text
启动后延迟 2 分钟
  → 每 10 分钟扫描一次缺失 profit_events 的非 processing 日志
  → 每次最多补 1000 条
```

这个兜底只补缺失事件，不修改日志，不重算已经存在的历史售价；因此可以长期打开，主要用于保证“日志数量”和“利润事件数量”最终收敛。

### 5.5 完整性状态展示

利润统计页面展示“利润账本完整性”状态，用来解释为什么利润统计数量可能暂时少于日志数量：

- 实时缺失数：查询 `logs` 中非 `processing`、但没有对应 `profit_events` 的记录数。
- 上次自动校对时间和结果：来自 logging 插件内存状态。
- 下次自动校对时间：按当前 10 分钟间隔估算。
- 手动补齐入口：复用现有“从现有日志回填利润”，执行后刷新完整性状态。

该状态不另建持久化表。缺失数量实时查数据库，最关键；上次/下次校对时间属于进程运行状态，服务重启后可以重新开始计算。

## 6. API 设计

新增一组 HTTP 接口，建议归在日志/观测相关 handler 下，或单独建 `ProfitHandler`。

### 6.1 获取售价设置

```http
GET /api/profit/settings
```

响应：

```json
{
  "sell_input_per_1m_usd": 2,
  "sell_output_per_1m_usd": 12,
  "timezone": "Asia/Shanghai",
  "updated_at": "2026-05-04T12:00:00Z"
}
```

### 6.2 保存售价设置

```http
PUT /api/profit/settings
```

请求：

```json
{
  "sell_input_per_1m_usd": 2,
  "sell_output_per_1m_usd": 12
}
```

校验：

- 两个价格必须 `>= 0`
- 至少一个价格大于 0
- 保存只影响之后新产生的 `profit_events`

### 6.3 获取汇总

```http
GET /api/profit/summary?preset=today
GET /api/profit/summary?preset=yesterday
GET /api/profit/summary?preset=7d
GET /api/profit/summary?preset=all
```

响应字段：

```json
{
  "preset": "today",
  "revenue_usd": 12.34,
  "cost_usd": 4.56,
  "profit_usd": 7.78,
  "gross_margin": 0.63,
  "request_count": 120,
  "success_count": 118,
  "error_count": 2,
  "prompt_tokens": 1234567,
  "completion_tokens": 345678,
  "missing_cost_count": 0,
  "missing_tokens_count": 1
}
```

### 6.4 获取每日明细

```http
GET /api/profit/daily?days=30
```

用于页面图表和表格。

### 6.5 手动回填

```http
POST /api/profit/backfill
```

请求：

```json
{
  "limit": 5000
}
```

响应：

```json
{
  "processed": 5000,
  "created": 4300,
  "updated": 700,
  "skipped": 0
}
```

## 7. 前端面板设计

页面路径：`/workspace/profit`  
菜单名称：`利润统计`  
权限：复用 Observability 查看权限；保存设置需要 Observability 更新权限，或者沿用现有日志设置权限。

### 7.1 页面结构

1. 顶部标题
   - 标题：`利润统计`
   - 副标题：`按真实请求、成本和出售价格计算收入与利润`

2. 售价设置卡片
   - `输入售价（美元 / 100 万 input tokens）`
   - `输出售价（美元 / 100 万 output tokens）`
   - 当前生效时间
   - 保存按钮：`保存出售价格`
   - 提示文案：`保存后只影响之后的新请求，历史利润不会重算。`

3. 快速统计卡片
   - `今日收入`
   - `今日成本`
   - `今日利润`
   - `今日毛利率`
   - 可切换：今日 / 昨日 / 最近 7 日 / 全部累计

4. 趋势图
   - 每日收入、成本、利润折线或柱状图
   - 默认最近 30 天

5. 每日明细表
   - 日期
   - 请求数
   - 成功数
   - 输入 tokens
   - 输出 tokens
   - 收入
   - 成本
   - 利润
   - 毛利率

6. 数据诊断
   - 缺失成本数量
   - 缺失 token 数量
   - 最近回填时间
   - 手动按钮：`从现有日志回填利润`

### 7.2 中文文案原则

保留：

- Provider
- Model
- input tokens
- output tokens
- USD
- API

汉化：

- Profit → 利润
- Revenue → 收入
- Cost → 成本
- Gross Margin → 毛利率
- Backfill → 回填
- Today / Yesterday / Last 7 days / All time → 今日 / 昨日 / 最近 7 日 / 全部累计

## 8. 日志清理关系

利润数据独立保存，清空日志不清空：

```text
清空日志：
  删除 logs
  删除 mcp_tool_logs
  不删除 profit_events
  不删除 profit_settings
```

页面上需要明确提示：

`清空日志不会清空利润统计。利润统计是独立账本。`

如果未来需要清空利润，应单独做“清空利润账本”危险操作，本版本不做。

## 9. 上线和历史数据处理

上线后有两种选择：

1. 不回填历史日志：利润从上线后开始累计，口径最干净。
2. 回填当前还保留的日志：可以立刻看到近期利润，但这些历史日志会使用当前出售价格估算。

推荐：

- 首次上线后先设置出售价格。
- 点一次“从现有日志回填利润”。
- 之后所有新请求自动入账。
- 后面如果清空日志，不影响已经累计的利润。

## 10. 实施任务清单

1. 后端数据结构
   - 新增 `ProfitSettings`
   - 新增 `ProfitEvent`
   - 新增迁移，创建表和索引

2. 利润计算服务
   - 实现 `CalculateProfitEvent(log, settings)`
   - 实现 `UpsertProfitEventFromLog`
   - 实现 `GetProfitSummary`
   - 实现 `GetProfitDaily`
   - 实现 `BackfillProfitEvents`

3. 日志写入接入
   - 普通日志写入后 upsert 利润事件
   - deferred usage 更新后再次 upsert 同一事件
   - cost 重算后同步更新利润事件

4. HTTP API
   - `GET /api/profit/settings`
   - `PUT /api/profit/settings`
   - `GET /api/profit/summary`
   - `GET /api/profit/daily`
   - `POST /api/profit/backfill`

5. 前端页面
   - 新增 `/workspace/profit`
   - 新增中文菜单“利润统计”
   - 售价设置表单
   - 今日 / 昨日 / 最近 7 日 / 全部累计统计卡片
   - 每日趋势和明细表
   - 数据诊断和回填按钮

6. 测试
   - 公式单元测试
   - 改售价不影响历史事件
   - 清空日志不清利润
   - deferred usage 后利润事件会更新
   - API 时间范围按 `Asia/Shanghai`
   - UI 显示中文文案、保存售价、刷新后读取一致

## 11. 风险和处理

| 风险 | 影响 | 处理 |
|---|---|---|
| 部分日志没有 token | 收入偏低 | 标记 `missing_tokens`，诊断区提示 |
| 部分日志没有 cost | 利润偏高 | 标记 `missing_cost`，诊断区提示 |
| 流式请求延迟补 token | 初始利润不准 | deferred usage 更新后 upsert 修正 |
| 售价改动后用户期望历史重算 | 认知不一致 | 页面明确提示“只影响新请求” |
| 日志清空后无法回填 | 不能补历史 | 利润事件提前独立保存 |
| 失败请求产生 provider 成本 | 可能出现负利润 | 失败成本照算，反映真实损耗 |

## 12. 待确认

以下是实现前需要你确认的业务口径：

1. 默认出售价格是否固定为输入 `$2 / 1M`、输出 `$12 / 1M`。
2. 失败请求如果 provider 已经扣费，是否按本文设计计入成本、收入为 0。
3. 首次上线时是否需要回填当前还存在的日志。
4. 页面菜单名称是否使用“利润统计”。

我建议四项都按本文默认值执行。
