# Adaptive Health Routing 上线 Runbook

日期：2026-05-02

## 目标

本 Runbook 只处理本次评审中采纳的运维项：上线前把现有 `gemini-auto` 规则里的最终兜底 group 显式标记为 `fallback_only=true`，并确认 provider pricing overrides 已按线上模型价格补齐。

不在本步骤修改代码，不自动开启 active probe，不调整 slow / cooldown policy。若线上已经手动开启 active probe，当前代码下它只做 liveness，不参与 routing health / cooldown。

## 背景

新版 grouped routing 不再用“最后一组天然是兜底层”的位置约定，而是读取 `route_groups[].fallback_only` 显式字段。

如果线上现有 `gemini-auto` 的最终兜底组没有该字段，代码上线后它会被当作普通 group 参与健康排序和探测计划，不符合“兜底不抢主路由，但普通链路失败时必须在当前请求最后尝试”的设计。

## 上线前检查

1. 确认只处理 `name='gemini-auto'` 且 `grouped_routing_enabled=true` 的规则。
2. 确认最终兜底 group 是 OpenRouter / `gemma-4-31b-it` 这类高成功率兜底 provider。
3. 确认该 group 修改前没有 `fallback_only=true`。
4. 确认该 group 的 `retry_limit` 是预期值；若设置为 `2`，单 target 兜底在普通链路都失败后最多会尝试 3 次。
5. 确认 active probe 全局是否为 disabled。若不是 disabled，确认这是人工选择；上线后它只会更新 last probe 状态，不会改变主路由健康评分。
6. 确认参与同组成本排序的 provider/model 已配置有效价格；自定义模型名优先用 provider `pricing_overrides` 补齐。

## PostgreSQL 迁移模板

> 只作为上线操作模板。执行前先在只读查询中确认 rule 与 group 顺序。

### 1. 备份目标规则

```sql
CREATE TABLE routing_rules_backup_20260502_adaptive_health AS
SELECT *
FROM routing_rules
WHERE name = 'gemini-auto'
  AND grouped_routing_enabled = true;
```

### 2. 查看现有 route groups

```sql
SELECT
  id,
  name,
  jsonb_pretty(route_groups::jsonb) AS route_groups
FROM routing_rules
WHERE name = 'gemini-auto'
  AND grouped_routing_enabled = true;
```

### 3. 只把最后一个 group 标为 fallback_only

```sql
BEGIN;

WITH target_rule AS (
  SELECT
    id,
    route_groups::jsonb AS groups
  FROM routing_rules
  WHERE name = 'gemini-auto'
    AND grouped_routing_enabled = true
  FOR UPDATE
),
patched AS (
  SELECT
    id,
    jsonb_agg(
      CASE
        WHEN ordinality = jsonb_array_length(groups)
          THEN elem || '{"fallback_only": true}'::jsonb
        ELSE elem
      END
      ORDER BY ordinality
    ) AS groups
  FROM target_rule,
       jsonb_array_elements(groups) WITH ORDINALITY AS group_item(elem, ordinality)
  GROUP BY id
)
UPDATE routing_rules rr
SET
  route_groups = patched.groups::text,
  updated_at = NOW()
FROM patched
WHERE rr.id = patched.id;

SELECT
  id,
  name,
  jsonb_pretty(route_groups::jsonb) AS route_groups
FROM routing_rules
WHERE name = 'gemini-auto'
  AND grouped_routing_enabled = true;

COMMIT;
```

## 验证标准

验证查询中应看到：

```json
{
  "name": "Group 3",
  "fallback_only": true,
  "targets": [...]
}
```

同时前面的普通 group 不应出现 `fallback_only=true`。

## Provider pricing overrides 验证

成本排序复用 provider 原有价格系统，不需要单独维护 routing 价格。上线前在后台 Provider 配置页检查：

1. 打开对应 provider 的 Pricing Overrides 页签。
2. 对线上自定义模型名填入 `$ / 1M input tokens` 与 `$ / 1M output tokens`。
3. 保存后刷新 provider，确认 JSON 中落库的是 per-token 字段：

```json
[
  {
    "model_pattern": "gemini-3.1-pro-preview*",
    "match_type": "wildcard",
    "request_types": ["chat_completion", "chat_completion_stream"],
    "input_cost_per_token": 0.00000125,
    "output_cost_per_token": 0.00001
  }
]
```

同组同健康桶内，router 使用 `input_cost_per_token + output_cost_per_token` 排序；不同组、不同健康桶、`fallback_only` 最后兜底的语义不受价格覆盖影响。

## 回滚

如果需要回滚该运维配置：

```sql
BEGIN;

UPDATE routing_rules rr
SET
  route_groups = backup.route_groups,
  updated_at = NOW()
FROM routing_rules_backup_20260502_adaptive_health backup
WHERE rr.id = backup.id;

COMMIT;
```

代码回滚不依赖删除该 JSON 字段；旧版本会忽略未知字段。
