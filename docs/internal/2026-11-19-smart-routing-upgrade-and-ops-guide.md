# 2026-11-19 智能分流引擎改造升级与运维注意事项

> **文档说明**：本文档记录 2026-11-19 完成的价格优先智能分流引擎重构、多中转站单分组拆分上线，以及后续日常维护、新增渠道、调价与回滚的关键注意事项。本文档**不含任何密钥明文、密码或服务器公网 IP**（本地密钥台账位于已排除 Git 的 `docs/internal/2026-11-19-relay-keys-and-pricing.md`）。

---

## 一、本次改造升级核心内容

### 1. 代码层引擎重构（`core/`、`framework/`、`plugins/governance/`、`transports/bifrost-http/`）

1. **动态整请求回退时间预算（`BifrostContextKeyFallbackDeadline`）**
   - 在 `core/schemas/bifrost.go`、`core/schemas/context.go`、`core/bifrost.go` 中引入整条回退链的统一截止时间（同步与流式双路径均生效），防止多跳重试累积拖垮业务请求总耗时。
2. **四级错误精准分类（`plugins/governance/outcome_classifier.go`）**
   - `OutcomeFatalChannel`：401/402/403/404、余额不足、`No available channel`、`model_not_found`、`model_not_entitled` 等致命通道错误，**默认连续 1 次即快速熔断 300s**，不再反复打死通道。
   - `OutcomeSoftFail`：408/429/502/503 过载/504/读超时等瞬时故障，支持连续失败次数（默认 2 次）与滑动窗口错误率（默认近 10 次错误率 ≥ 50%）双触发冷却，并带指数退避（`60s → 600s`）。
   - `OutcomeSlow`：慢响应只降级为 `HealthDegraded`（降低优先级），不再粗暴打入硬冷却。
   - `OutcomeIgnore`：499 客户端取消、客户端 400 参数错误，不污染通道健康分。
3. **两阶段原地半开恢复（`plugins/governance/health_tracker.go`、`grouped_router.go`）**
   - 冷却到期目标不再被埋在候选链末尾导致永久饿死，而是以 `HalfOpenCandidate` 身份**回到原始价格顺位参与竞争**；
   - 单次请求最多携带 1 个半开试探目标，且在真正入选最终路由链时才通过原子租约（65s）占坑，避免高并发惊群同时冲击刚解冻渠道；
   - 冷却解冻后自动清空历史失败窗口样本，避免因旧错误率残留导致恢复即二次误熔断。
4. **价格优先 + 跨上游多样性去重 + 全冷却逃生链（`plugins/governance/grouped_router.go`、`routing.go`）**
   - 同组多目标在 `retry_limit <= 0` 时，基于 `TargetCostResolver` 按真实有效成本（结合 `pricing_overrides`）自动从低到高排序；
   - 开启 `enable_upstream_diversity` 后，基于目标 `base_url` 主机名按轮次交错排列回退链（如 StoreAPI → B.AI → PoloAPI → OpenLux），单次请求不会连续把重试次数浪费在同一家波动的中转站上；
   - 当所有目标同时处于冷却期时，构造按“非致命优先、最早解冻优先”排序的逃生链，彻底修复历史“全冷却时落回 `model should be in provider/model format` 误报”的缺陷。
5. **主动健康探测自动唤醒闭环（`plugins/governance/active_probe.go`、`transports/bifrost-http/handlers/adaptive_routing.go`）**
   - 新增可选开关 `active_probe_wakeup`（配合 `RuleHasRecentRealAccess`）：当路由规则存在活跃真实流量时，即使目标处于冷却期，主动探测也会持续定时探活，一旦探活成功即提前解冻恢复低价流量，且前端健康面板 `ProbeState` 同步准确反映探测状态。
6. **配置与 API 向后兼容（`framework/configstore/tables/routing_rules.go`、`transports/bifrost-http/handlers/governance.go`）**
   - `HealthPolicy` 新增字段全部 `omitempty` 且带安全默认值，兼容已有路由规则 JSON，无需手工迁移旧规则即可平滑启动。

---

### 2. 线上配置与渠道拆分升级（2026-11-19）

1. **核心原则：一个价格分组 = 一个独立单分组 Provider + 独立 Key**
   - 彻底告别过去“一个中转站 Key 混绑多个分组（实际落到高价组却按低价记账）”的黑盒问题。
   - 所有新 Key 均关闭中转站侧的 `auto` / 智能路由，仅绑定单一目标分组，把选路权和真实单价统计完全收归 Bifrost。
2. **线上 `gemini-auto` 路由规则（4 家平台、21 个已验证支持裸 3.1 Pro 的单分组档位）**
   - 统一换算口径：官方基准 `gemini-3.1-pro` / `gemini-3.1-pro-preview` 为 `$2.00 / $12.00` per 1M（缓存读 `$0.20 / 1M`），人民币充值平台按固定汇率 `6.8` 折算。
   - **Tier 1 · 极低价冲锋（2% ~ 15%）**：`storeapi-aistudio` (2.21%)、`bai-10pct` (10.0%)、`polo-g1-1x` (14.71%)、`polo-anti-1x` (14.71%)
   - **Tier 2 · 中低价性价比（19% ~ 30%）**：`openlux-vertex-1` (19.6%)、`openlux-aistudio-2` (25.94%)、`openlux-vertex-2` (29.4%)、`polo-g2-2x` (29.41%)、`polo-relay-2x` (29.41%)
   - **Tier 3 · 中高价主力（40% ~ 50%）**：`bai-40pct` (40.0%)、`openlux-aistudio-3` (43.24%)、`polo-g3-3x` (44.12%)、`polo-vertex-3x` (44.12%)、`polo-pool-3x` (44.12%)
   - **Tier 4 · 高稳保底（60% ~ 100%）**：`bai-60pct` (60.0%)、`bai-70pct` (70.0%)、`polo-svip-5.5x` (80.88%)、`openlux-vertex-3` (88.2%)、`openlux-aistudio-4` (93.68%)、`bai-100pct` (100.0%)、`polo-svip-6.8x` (100.0%)
   - 已停用并移出 `NightRouter`；实测返回 `404/503 model_not_found` 的 10 个分组暂不接入路由，保留在本地台账备后续复测。
3. **上线验证结果**
   - 容器健康检查 `200 ok`，21/21 个新目标全部开启主动健康探测；
   - 端到端 `gemini-auto` 实测与真实业务流量均正常通过，成本计算与 `pricing_overrides` 核对精确到小数点后 7 位。

---

## 二、后续运维与变更注意事项（必读）

### 1. 新增或替换单分组供应商时，必须同步做完 4 步
仅在管理台新建 Provider 不够，**漏掉第 2 步会导致线上请求直接鉴权拒绝或跳过该渠道**：
1. **建 Provider 与单价覆盖**：
   - 模型 ID 务必核对准确：**B.AI 必须是 `gemini-3.1-pro`**，**StoreAPI / OpenLux / PoloAPI 必须是 `gemini-3.1-pro-preview`**；
   - `network_config.max_retries` 设为 **`0`**（重试交给 Bifrost 分流引擎跨上游处理，不要在单个坏节点上原地重试）；超时统一 `60s / 60s`；
   - `pricing_overrides` **必须同时填写** `input_cost_per_token`、`output_cost_per_token`、`above_128k`、`above_200k` 和 `cache_read_input_token_cost`（防止长上下文或命中缓存时回退到官方目录原价导致成本统计虚高）。特别注意 **StoreAPI 缓存读是官方的 `4.41%`（输入/输出是 `2.21%`）**，不可直接按同一比例套用。
2. **同步加入虚拟密钥白名单（极易遗漏！）**：
   - 线上虚拟密钥 `bif` 配置了显式 `provider_configs` 白名单。每新增一个 Provider，必须把它和允许的 `allowed_models`、`key_ids` 一起更新进 `bif` 的 `provider_configs`，否则路由选到它时会被虚拟密钥拦截。
3. **加入 `gemini-auto` 路由规则并保证每组权重之和为 `1.0`**：
   - Admin API 校验每个 `route_group` 内所有 `targets` 的 `weight` 之和必须等于 `1.0`（哪怕 `retry_limit: 0` 按价格排序，提交时也必须归一化权重）。
4. **开启主动健康探测（`detection_enabled: true`）**：
   - 路由规则更新后，在健康检测页或通过 `PUT /api/governance/health-detection-targets/{target_id}`（字段名为 `detection_enabled`）开启新目标的主动探测，确保 `active_probe_wakeup` 能自动探活解冻。

### 2. 账单核对与中转站暗调价防范
- 当前各通道单价是按平台报价页与固定汇率 `6.8` 换算的理论单价，**建议上线运行 1~3 天后，抽查四家中转站后台的真实扣费流水**，核对是否存在：
  - 隐性最低消费、思考 token 特殊倍率、或长上下文（>200k）加倍计费；
  - 某中转站悄悄调整分组倍率或充值优惠比例。
- 如发现偏差，直接通过 `PUT /api/providers/{provider}` 修正 `pricing_overrides` 即可，分流排序与利润统计会自动同步生效。

### 3. 本地留存的 10 个“当前不支持”分组复测说明
- OpenLux 6 个超低价分组（`Anti-1/2`、`Cli-1`、`Discounted-1/2`、`Aistudio-1`）、StoreAPI `Gemini反重力渠道`、PoloAPI `gemini-cli` / `gemini-as` / `gemini-vertex-2` 在 2026-11-19 实测均报 `404/503` 无裸 3.1 Pro 渠道。
- Key 已保留在本地 git 排除的台账里；**切勿未经单条测试就直接塞进线上路由**，以免拉低首跳效率。日后复测确认某组恢复支持裸模型后，再按上面 4 步接入即可。

### 4. 备份与紧急回滚方式
- 本次上线前已完整备份：
  - 旧镜像标签：`bifrost:rollback-20261119`
  - 全库与配置目录：`/root/bifrost-deploy/backups/pre-smart-routing-20261119/`
  - 原有旧 Provider（`云雾API`、`poloapi`、`poloapi_vip`、`B.AI`、`B.AI-6fold`、`B.AI-7fold`、`StoreAPI`）均未删除。
- **若需仅回滚路由（保留新版引擎）**：通过 Admin API 将 `/root/bifrost-deploy/backups/pre-smart-routing-20261119/routing_rules.json` 中 `gemini-auto` 的原 `route_groups` 写回即可热生效。
- **若需整包回滚旧版镜像**：在服务器执行 `docker tag bifrost:rollback-20261119 crpi-1nlet4g61989v2tx.cn-shenzhen.personal.cr.aliyuncs.com/fdcode/bifrost:latest && cd /root/bifrost-deploy && docker compose up -d --force-recreate bifrost`。

### 5. 镜像构建与发布提示
- 线上 `config_store` 与 `logs_store` 均使用 PostgreSQL（不依赖 SQLite/CGO）。若远端服务器公网带宽较小导致容器内 `apk add gcc` 过慢，可在本机交叉编译静态 Linux `amd64` 二进制（含 `ui/out` 静态构建产物），再在远端基于现有运行镜像替换 `/app/main` 与 `/app/config.schema.json`，数秒即可完成镜像更新。
- 涉及密钥、余额导出、账单明细的本地文件（如 `*-relay-keys-and-pricing.md`、`*balance*.json`、`*billing*.json`、`.scratch/`）务必保持在 `.git/info/exclude` 中，提交前必须检查 `git diff --cached`。
