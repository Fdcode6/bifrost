# 2026-11-19 智能分流引擎改造升级与运维注意事项

> **文档说明**：本文档记录 2026-11-19 完成的价格优先智能分流引擎重构、多中转站单分组拆分上线，以及后续日常维护、新增渠道、调价与回滚的关键注意事项。本文档**不含任何密钥明文、密码或服务器公网 IP**（本地密钥台账位于已排除 Git 的 `docs/internal/2026-11-19-relay-keys-and-pricing.md`）。

---

## V2.2 发布和回退

`gemini-auto` 与 `gemini-auto-bif` 共用一条分组规则。V2.2 在本地实现，旧规则缺省关闭。发布时先备份规则，核实分组路由和自适应排名已启用，再为 StoreAPI AI Studio 与 OpenLux Aistudio-3 分别设置目标字段 `primary_attempt_timeout_ms=22000` 与 `28000`。保留规则的 35 秒首跳、20 秒救援上限、58 秒总预算及最多五跳。

通过管理接口将完整 `health_policy` 中的 `adaptive_v22_enabled` 更新为 `true`，两个别名的新根请求即使用新策略。恢复时在同一规则把该字段改为 `false`，回读配置并核对各实例下一笔请求。已开始的请求继续使用原快照。

按本规则的 15 秒阈值，首跳快速失败且预算充足时，优先顺延到健康主力，沿用目标的单跳时限。达到阈值、探针失败或已只够最后一跳时，按救援排名选当前可用目标。多跳时为末跳预留 14 秒；剩余 3 至不足 17 秒时，把余量给排名最高的合格候选。整轮仍受 58 秒和五次尝试限制。

日志按 `adaptive-v22` 和根请求 ID 关联每跳。网关派发前排队到期记作 `local_queue_timeout`，耗时计入总预算，上游健康统计排除该次。普通缓冲响应复用 Provider 的连接池，取消时关闭本次占用的连接。流式另有 `stream_end`，记录首个有效输出和提交后的中断。生产验收按业务记录核对按时成功、第二轮挽回、最终成功及真实扣费。

## 一、本次改造升级核心内容

### 1. 代码层引擎重构（`core/`、`framework/`、`plugins/governance/`、`transports/bifrost-http/`）

1. **V2 90 分钟时间衰减信誉账本 + 快慢错分级权重（`plugins/governance/health_tracker.go`、`adaptive_router.go`）**
   - 采用 90 分钟（`5400s`）指数衰减统计真实业务长请求的有效成功率与 `P80` 耗时，兼顾长线历史人品与近期表现；
   - **区分“秒退快错”与“慢卡死超时”**：`<3s` 瞬时 `503/429` 仅记 `0.35` 轻权重（高信誉主力偶发报错排名纹丝不动；仅当连续报错 `≥5` 次才触发 `60s` 短避让，恢复后凭 90 分钟厚底子直接重回榜首）；超时/协议崩（如 `451 Protocol generation failed`）重记 `2.0` 权重；`401/402/403/404` 及 `No available channel` 记 `6.0` 致命权重；
   - **保底活跃池锁（`min_active_targets = 3`）**：即使遭遇上游全局网络波动导致全员成功率短暂低于 `70%`，主力池仍强制保留得分最优的前 3 个折扣渠道在岗，杜绝“全员掉级无路可走”。
2. **动态单跳超时 + “快慢组合救场”（`core/bifrost.go`、`core/providers/utils/utils.go`）**
   - 引入无损单跳截止时间 `BifrostContextKeyAttemptDeadline`（不提前取消父级请求上下文），线上 V2.1 首跳给予 `35s` 宽裕时间吃满“慢但稳”的低价渠道利润；
   - 当请求已耗时间 `<15s`（如首跳秒报 `503`），下一跳继续按低价优先顺延；
   - 当请求已耗时间 `≥15s`（如首跳 `35s` 超时），自动切入 **快枪手救场模式（Fast-Rescue Mode）**：跳过慢渠道与未证明速度的低价渠道，直抓实测高成功率且 `P80 ≤ 16s` 的快渠道（单跳限时 `20s`），配合整单 `58s` 总预算，对齐下游 `60s`（侧边栏插件）与 `70s`（主引擎）超时线。
3. **不稳低价渠道“观察席定时真单体检” + 彻底废除短消息假探活（`plugins/governance/adaptive_router.go`、`active_probe.go`）**
   - 开启 `adaptive_ranking_enabled` 后，自动停用无参考价值的 1-token `"ping"` 短上下文主动探活；
   - 成功率 `≤70%` 的低价渠道自动移入观察席（日常 `0` 流量），每隔 `5~15 分钟`（退避递增）仅抽取 **1 单单并发真实请求** 置于第 0 跳实测（限时 `32~35s`），第 1 跳紧接快枪手托底；一旦连续 `2` 单真实请求跑通即自动摘帽升回主力池榜首省钱。
4. **四区利润红线硬隔离（`RouteGroup.FallbackOnly` + `RouteGroup.LastResort`）**
   - `L1 赚钱主力池（≤60%）`：合格渠道按综合代价分排序，第 1 名 100% 吃满流量，回退跨站打散仅在同层内部进行，严禁跨层把高价渠道提前抓出；
   - `L2 薄利后备池（70%~95%，fallback_only=true）`：平时 `0` 调用，仅当 L1 可用渠道当场全败或无可用快枪手时顶上；
   - `L3 100% 原价终极底牌（100%，last_resort=true）`：硬锁仅在整单重试链的**最后一次机会**才允许出场救命。

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
   - 已停用并移出 `NightRouter`；OpenLux 不含 `3.1-pro` 的 6 个非 Pro 分组令牌及 PoloAPI 无可用渠道的 3 个分组令牌均已在对应后台清理，仅保留 StoreAPI `Gemini反重力渠道` 在本地台账备后续复测。
3. **上线验证结果**
   - 容器健康检查 `200 ok`，全量停用无参考价值的 1-token 短 `"ping"` 主动探测，改为真实请求自适应信誉追踪；
   - 端到端 `gemini-auto` 实测与真实业务流量均正常通过，成本计算与 `pricing_overrides` 核对精确到小数点后 7 位。

---

## 二、后续运维与变更注意事项（必读）

### 1. 新增或替换单分组供应商时，必须同步做完 3 步
仅在管理台新建 Provider 不够，**漏掉第 2 步会导致线上请求直接鉴权拒绝或跳过该渠道**：
1. **建 Provider 与单价覆盖**：
   - 模型 ID 务必核对准确：**B.AI 必须是 `gemini-3.1-pro`**，**StoreAPI / OpenLux / PoloAPI 必须是 `gemini-3.1-pro-preview`**；
   - `network_config.max_retries` 设为 **`0`**（重试交给 Bifrost 分流引擎跨上游处理，不要在单个坏节点上原地重试）；Provider 静态兜底超时统一设为 `42s / 42s`（实际单跳由自适应引擎动态裁剪）；
   - `pricing_overrides` **必须同时填写** `input_cost_per_token`、`output_cost_per_token`、`above_128k`、`above_200k` 和 `cache_read_input_token_cost`（防止长上下文或命中缓存时回退到官方目录原价导致成本统计虚高）。特别注意 **StoreAPI 缓存读是官方的 `4.41%`（输入/输出是 `2.21%`）**，不可直接按同一比例套用。
2. **同步加入虚拟密钥白名单（极易遗漏！）**：
   - 线上虚拟密钥 `bif` 配置了显式 `provider_configs` 白名单。每新增一个 Provider，必须把它和允许的 `allowed_models`、`key_ids` 一起更新进 `bif` 的 `provider_configs`，否则路由选到它时会被虚拟密钥拦截。
3. **加入 `gemini-auto` 路由规则并保证每组权重之和为 `1.0`**：
   - Admin API 校验每个 `route_group` 内所有 `targets` 的 `weight` 之和必须等于 `1.0`（哪怕 `retry_limit: 0` 按价格排序，提交时也必须归一化权重）。自适应规则下无需开启短 `"ping"` 主动健康探测（保持 `detection_enabled: false`）。

### 2. 账单核对与中转站暗调价防范
- 当前各通道单价是按平台报价页与固定汇率 `6.8` 换算的理论单价，**建议上线运行 1~3 天后，抽查四家中转站后台的真实扣费流水**，核对是否存在：
  - 隐性最低消费、思考 token 特殊倍率、或长上下文（>200k）加倍计费；
  - 某中转站悄悄调整分组倍率或充值优惠比例。
- 如发现偏差，直接通过 `PUT /api/providers/{provider}` 修正 `pricing_overrides` 即可，分流排序与利润统计会自动同步生效。
- **2026-11-20 00:00 PoloAPI 调价跟进提醒**：PoloAPI 于 `2026-11-19 18:34` 发布公告称所有 Gemini 直连分组（aistudio、vertex 等非逆向资源）自 `2026-11-20 00:00` 起统一调至 `3x` 倍率（官价 `44.12%`），逆向分组不变。需复核混合分组 `polo-g2-2x`（`Gemini-折扣临时`）与 `polo-relay-2x` 是否跟随上调；若上调至 `3x`，需同步更新其 `pricing_overrides`。

### 3. 无效分组清理与本地留存待复测分组说明
- **OpenLux（已清理）**：6 个超低价分组（`Anti-1/2`、`Cli-1`、`Discounted-1/2`、`Aistudio-1`）已确认为空组 / 纯视频 / 纯生图 / 纯 Flash 分组，连同 2 把重复备用令牌已于 2026-11-19 在云雾后台删除并从本地台账移除。
- **PoloAPI（已清理）**：3 个无用分组（`gemini-cli` 不含裸 `3.1-pro`、`gemini-as` 空壳无可用渠道、`gemini-vertex-2` 超时且重复）已于 2026-11-19 在 PoloAPI 后台删除并从本地台账移除。
- **StoreAPI（保留 1 个待复测）**：仅剩 `Gemini反重力渠道` 保留在本地 git 排除的台账里；**切勿未经单条测试就直接塞进线上路由**，日后复测确认恢复支持裸模型后再按上面 3 步接入即可。

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
