# 2026-11-19 智能分流引擎改造升级与运维注意事项

> **文档说明**：本文档记录 2026-11-19 完成的价格优先智能分流引擎重构、多中转站单分组拆分上线，以及后续日常维护、新增渠道、调价与回滚的关键注意事项。本文档**不含任何密钥明文、密码或服务器公网 IP**（本地密钥台账位于已排除 Git 的 `docs/internal/2026-11-19-relay-keys-and-pricing.md`）。

---

## V2.2 发布、超时调优与回退

`gemini-auto` 与 `gemini-auto-bif` 共用一条分组规则。V2.2 已于 2026-11-19 上线启用（`adaptive_v22_enabled = true`），并于 **2026-11-20** 结合早盘真实流量完成了超时配比热调优：

- **当前线上超时配比（2026-11-20 生效）**：
  - 总预算 `request_deadline_ms = 62000`（`62s`，对齐企微助手 `70s` 外层超时留足 `8s` 缓冲）；
  - 默认首跳超时 `primary_attempt_timeout_ms = 24000`（`24s`；仅 `storeapi-aistudio` 单独设 `22000`，`openlux-aistudio-3` 走默认 `24000`）；
  - 快救单跳上限 `rescue_attempt_timeout_ms = 22000`（`22s`）、末跳预留 `14s`、最多 `5` 跳；
  - 实际三跳分配：**首跳 `24s` → 二跳满血快救 `22s` → 末跳一整块兜底 `16s`**（既根治旧版 `35s` 首跳把第 2 跳挤压成 `9s` 的问题，又让前两跳超时后余量 `16s < 17s` 不再触发 `4s + 14s` 废跳拆分）。
- **热回退方式**：通过管理接口将 `health_policy.adaptive_v22_enabled` 改为 `false` 即可秒退回 V2.1；已开始的请求继续使用原快照。

按本规则的 15 秒阈值，首跳快速失败且预算充足时，优先顺延到健康主力，沿用目标的单跳时限。达到阈值、探针失败或已只够最后一跳时，按救援排名选当前可用目标。多跳时为末跳预留 14 秒；剩余 3 至不足 17 秒时，把余量给排名最高的合格候选。整轮受 62 秒和五次尝试限制。

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
2. **线上 `gemini-auto` 路由规则（4 家平台、20 个在用单分组档位，2026-11-20 已移除 `bai-10pct`）**
   - 统一换算口径：官方基准 `gemini-3.1-pro` / `gemini-3.1-pro-preview` 为 `$2.00 / $12.00` per 1M（缓存读 `$0.20 / 1M`），人民币充值平台按固定汇率 `6.8` 折算。
   - **L1 赚钱主力池（14 个，占官价 `2.21% ~ 60.0%`）**：
     - 极低价档（`2.2% ~ 15%`）：`storeapi-aistudio` (2.21%, 限 `22s`)、`polo-g1-1x` (14.71%)、`polo-anti-1x` (14.71%)
     - 中低价档（`19% ~ 30%`）：`openlux-vertex-1` (19.6%)、`openlux-aistudio-2` (25.94%)、`openlux-vertex-2` (29.4%)、`polo-g2-2x` (29.41%)、`polo-relay-2x` (29.41%)
     - 中高价主力档（`40% ~ 60%`）：`bai-40pct` (40.0%)、`openlux-aistudio-3` (43.24%)、`polo-g3-3x` (44.12%)、`polo-vertex-3x` (44.12%)、`polo-pool-3x` (44.12%)、`bai-60pct` (60.0%)
   - **L2 薄利后备池（4 个，占官价 `70.0% ~ 93.68%`，`fallback_only = true`）**：`bai-70pct` (70.0%)、`polo-svip-5.5x` (80.88%)、`openlux-vertex-3` (88.2%)、`openlux-aistudio-4` (93.68%)
   - **L3 终极救命底牌（2 个，占官价 `100.0%`，`last_resort = true`）**：`bai-100pct` (100.0%)、`polo-svip-6.8x` (100.0%)
   - 已停用并移出 `NightRouter` 与 `bai-10pct`（2026-11-20 因 0/30 持续 503 彻底删除）；OpenLux 非 Pro 分组（6 个）及 PoloAPI 无效分组（3 个）均已在对应后台清理，仅保留 StoreAPI `Gemini反重力渠道` 在本地台账备后续复测。
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
- 线上保留的关键镜像标签：
  - 当前线上最新镜像：`bifrost:profit-fix-20261120`（含 V2.2 + 新版自适应经营看板 + Profit 缺失成本与对账自愈修复）
  - 上一版镜像：`bifrost:adaptive-dashboard-20261119`、`bifrost:smart-routing-v2.2-20261119`、`bifrost:rollback-20261119`
- 线上保留的配置备份目录：
  - `/root/bifrost-deploy/backups/pre-smart-routing-20261119/`
  - `/root/bifrost-deploy/backups/pre-v22-20261119/`
  - `/root/bifrost-deploy/backups/before-remove-bai10pct-20261120/`
  - `/root/bifrost-deploy/backups/before-timeout-tune-20261120/`
- **若需仅回滚路由（保留新版引擎）**：通过 Admin API 将对应备份目录中的 `gemini-auto` 规则写回即可热生效。
- **若需整包回滚旧版镜像**：在服务器执行 `docker tag <目标镜像标签> crpi-1nlet4g61989v2tx.cn-shenzhen.personal.cr.aliyuncs.com/fdcode/bifrost:latest && cd /root/bifrost-deploy && docker compose up -d --force-recreate bifrost`。

### 5. 镜像构建与发布提示
- 线上 `config_store` 与 `logs_store` 均使用 PostgreSQL（不依赖 SQLite/CGO）。若远端服务器公网带宽较小导致容器内 `apk add gcc` 过慢，可在本机交叉编译静态 Linux `amd64` 二进制（含 `ui/out` 静态构建产物），再在远端基于精简基座镜像 `bifrost:rollback-20261119` 替换 `/app/main` 与 `/app/config.schema.json`，数秒即可完成镜像更新。
- 涉及密钥、余额导出、账单明细的本地文件（如 `*-relay-keys-and-pricing.md`、`*balance*.json`、`*billing*.json`、`.scratch/`）务必保持在 `.git/info/exclude` 中，提交前必须检查 `git diff --cached`。

---

## 三、2026-11-20 看板升级、利润修复与线上调优记录

1. **全新 Adaptive Routing「智能分流与经营看板」（`/workspace/adaptive-routing`，提交 `0d24de467`）**
   - 彻底移除旧版短消息主动探活页面入口（后端兼容接口仍保留），整页改为纯只读实时监控 + 经营分析；
   - 新增聚合接口 `GET /api/governance/adaptive-dashboard`（`10s` 自动轮询实时内存态 + `15s` 窗口统计缓存）；顶栏按根请求口径展示客户最终成功率与端到端分位耗时，下表按 L1 / L2 / L3 分层展示各分组单跳表现、90m 信誉、10m `≤16s` 救援窗口、下一单路由预览及与 Profit 账本 100% 同源的成本/利润/毛利率（默认固定汇率 `6.8` 人民币主显、美金副显）。
2. **Profit 利润页修复与体验精简（`/workspace/profit`，提交 `d72ac25fe`）**
   - 修正 `missing_cost_count` 仅统计 `status = 'success'` 的成功单，不再将失败尝试误报为缺失成本；
   - 扩展后台定时对账自愈范围（同步修复滞后补齐的成本/token 且保持历史售价固化），流式延迟 usage 回写时自动补算成功单成本；
   - 顶部冗余大横幅收拢为单行轻状态条，删除重复的回填按钮，顶部售价徽章明确区分「默认售价」与「规则独立定价」。
3. **渠道与超时参数调优（2026-11-20 线上热更新）**
   - 删除 `bai-10pct`（累计 `0/30` 持续报 `503` 且偶发 32s 超时），`gemini-auto` 精简为 **20 个在用分组**；
   - 发现原 `58s` 预算 + `35s` 首跳 + `14s` 末跳预留会把第 2 跳压缩至 `9s` 误杀正常快枪手，先热更新为 `62s / 24s / 20s` 后又进一步发现余量 `18s ≥ 17s` 会拆出 `4s` 废跳，最终调优为 **`request_deadline_ms = 62000`、`primary_attempt_timeout_ms = 24000`、`rescue_attempt_timeout_ms = 22000`**（形成零浪费的 `24s + 22s + 16s` 满血三跳窗口）。
