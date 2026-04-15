# Adaptive Routing 统一健康检测控制台设计稿

日期：2026-04-15

状态：待评审

关联文档：

- `docs/superpowers/specs/2026-04-14-adaptive-routing-health-detection-ui-design.md`
- `docs/superpowers/specs/2026-04-14-hybrid-health-probing-design.md`
- `docs/superpowers/specs/2026-04-14-hybrid-health-probing-requirements.md`

说明：

- 本文档在昨天“全局健康检测设置可视化”的基础上继续收敛。
- 本文档覆盖并替代“是否按规则分别配置检测目标”这一未决问题。
- 本文档已经吸收本轮交叉审核意见，重点修正“统一状态”和“现有规则级健康机制”的冲突。

## 1. 结论

`Adaptive Routing` 页面升级为统一健康检测控制台。

页面职责分成两部分：

- 上半部分管理整套主动检测系统的全局运行规则
- 中部管理所有已加入智能路由规则目标的统一检测开关与探测活动视图
- 下半部分继续展示按规则拆分的实际健康状态

本次设计采用以下固定原则：

- 所有配置统一放在 `/workspace/adaptive-routing`
- 不按单条智能路由规则分别配置目标级检测开关
- 同一个具体目标只保留一份检测开关和一份目标级探测活动视图
- 实际健康状态仍然按规则分别计算，不能被统一名单覆盖
- 列表唯一标识按 `Provider + Model + Key ID`，`Key ID` 为空时仍保留可见行
- 新进入列表的支持型目标默认 `关闭检测`
- 关闭检测表示退出主动检测控制，不再参与统一名单里的探测活动判断
- 没有 `Key ID` 或不支持主动检测的目标仍显示，但标记为 `Unsupported`
- 超过设定时间没有真实访问时，常规主动检测自动暂停
- 这里的“真实访问”只计算“通过智能路由实际打到该目标”的请求
- 新开启且尚无真实访问的支持型目标，会触发一次首检，不需要等生产流量
- v1 的目标活动视图明确按 `node-local` 语义设计，不承诺多实例全局一致

## 2. 背景

当前实现已经支持：

- 被动健康观测
- 混合模式下的主动探测
- `Adaptive Routing` 页面中的全局探测参数设置
- 按规则展示 grouped routing 的目标健康状态

但当前方案仍有四个实际问题：

1. 使用者不能明确指定哪些目标要参与主动检测，哪些不要
2. 新增目标必须先吃到真实流量，才能进入主动检测补位逻辑
3. 如果把目标开关放到规则内，同一个目标出现在多条规则里时会产生冲突
4. 当前页面展示的是规则级健康状态，不适合直接扩成“统一目标名单”而不重新定义边界

因此，本次设计目标不是替换现有规则级健康机制，而是补上一层“统一目标控制台”：

- 统一管理是否纳入主动检测
- 统一展示目标级探测活动
- 保留规则级健康状态作为路由的真实判定结果

## 3. 设计目标

- 在一个页面内完成全局健康检测设置与目标级开关管理
- 保证同一个具体目标不会因为多条规则出现冲突配置
- 不改变现有规则级健康状态的判定边界
- 明确哪些目标被纳入主动检测，哪些只能观察不能开启
- 避免长时间无流量的目标持续被后台探测
- 让使用者区分“目标是否纳入主动检测”和“某条规则下是否已经进入 cooldown”
- 让新开启目标可以在生产流量到来前先做一次首检

## 4. 本次不做

- 不按单条规则设置不同的目标开关
- 不按目标设置不同的空闲暂停阈值
- 不让统一名单直接改写规则级 cooldown 结果
- 不把未加入智能路由规则的目标纳入本页管理
- 不按抽象模型名合并多个不同站点
- 不在第一版中扩展到非智能路由请求来源
- 不在第一版中做多实例共享的统一运行态面板

## 5. 方案比较

| 方案 | 做法 | 优点 | 缺点 | 结论 |
| --- | --- | --- | --- | --- |
| A | 每条规则分别设置检测目标 | 控制最细 | 同一目标跨规则会冲突，维护复杂 | 不选 |
| B | 全局统一名单，并用统一名单替代规则级健康状态 | 页面看起来简单 | 与现有规则级健康机制冲突，状态会失真 | 不选 |
| C | 全局统一名单只统一开关与探测活动，规则级健康状态继续保留 | 一处管理，不冲突，边界清楚 | 页面信息层次更多 | 选这个 |

选择方案 C 的原因：

- 满足用户要“一处管理”的需求
- 不破坏现有 rule-scoped 健康判断
- 能在不重写路由核心语义的前提下交付新控制台

## 6. 页面信息架构

页面仍然使用 `/workspace/adaptive-routing`，但内容调整为三段式控制台。

### 6.1 顶部区域

- 标题：`Adaptive Routing`
- 副标题：说明这是智能路由健康检测的统一控制台
- 操作：`Refresh`

### 6.2 全局设置卡

保留并扩展现有全局健康检测设置：

| 字段 | 说明 |
| --- | --- |
| Detection Mode | `Passive only` 或 `Hybrid (Passive + Active)` |
| Idle Pause Minutes | 多少分钟没有真实访问，就暂停常规主动检测 |
| Probe Interval | 后台多久扫描一次可探测目标 |
| Probe Timeout | 单次主动检测最长等待时间 |
| Max Concurrency | 一轮最多同时检测多少个目标 |

交互规则：

- 全局设置继续采用“编辑后统一保存”
- `Detection Mode` 切到 `Passive only` 后，常规主动检测与首检都停用
- 主动检测相关数值保留，不自动清空

### 6.3 统一目标名单

全局设置卡下面新增统一目标列表。

每一行表示一个“具体目标”，而不是一个抽象模型名。

这张表只负责两件事：

- 统一管理目标是否纳入主动检测
- 统一展示目标级探测活动

它不直接替代规则级健康表。

建议列包含：

| 列名 | 说明 |
| --- | --- |
| Provider | 提供方 |
| Model | 模型名 |
| Key ID | 具体站点或凭证标识，可为空 |
| Referenced By | 被哪些智能路由规则引用 |
| Support Status | 是否支持主动检测，若不支持显示原因 |
| Detection Enabled | 检测开关 |
| Probe State | 目标级探测活动状态 |
| Rule Health Summary | 当前有多少条引用它的规则处于 cooldown |
| Last Real Access | 最近一次通过智能路由打到该目标的真实访问时间 |
| Last Probe | 最近一次主动检测时间 |
| Last Probe Result | 最近一次主动检测结果 |

页面说明必须明确：

- 统一名单里的 `Probe State` 不是规则级健康状态
- 真正的路由健康结果仍以下方按规则展示的健康表为准

### 6.4 规则级健康状态区

页面下半部分继续保留现有规则级健康状态展示。

继续展示：

- 每条规则
- 每条规则下的目标状态
- cooldown
- failure count
- consecutive failures
- last failure

这部分是路由行为的权威视图。

## 7. 目标唯一标识与去重规则

统一名单按以下三个字段识别同一个目标：

| 字段 | 作用 |
| --- | --- |
| Provider | 区分服务来源 |
| Model | 区分模型 |
| Key ID | 区分具体站点或凭证，允许为空 |

去重规则：

- 三个字段完全相同，视为同一个目标
- 即使同一目标被多条规则引用，统一名单中也只显示一行
- 同一个模型如果使用不同 `Key ID`，必须拆成多行显示

不采用“仅按模型名合并”的原因：

- 不同站点的真实可用性可能不同
- 不同凭证的限流和故障可能不同
- 合并后会导致状态解释失真

## 8. 目标进入名单的条件

目标只要满足以下条件就进入统一名单：

- 至少被一条启用了 grouped health routing 的规则引用
- 目标具备完整 `Provider + Model` 身份

说明：

- `Key ID` 不再是“进入名单”的硬条件
- 缺少 `Key ID` 的目标依然显示，避免页面把现有线上目标直接隐藏掉

进入名单后的支持性分类：

### 8.1 Supported

满足以下条件：

- `Provider`
- `Model`
- `Key ID`
- 存在支持的主动检测请求类型

这类目标允许：

- 开启主动检测
- 首检
- 空闲暂停
- 显示完整探测活动视图

### 8.2 Unsupported

以下任一情况即为 `Unsupported`：

- 缺少 `Key ID`
- 当前请求类型不支持主动检测
- 其他无法安全执行主动检测的情况

这类目标：

- 仍显示在统一名单中
- `Detection Enabled` 为只读禁用
- 页面显示明确原因，例如 `Missing Key ID`
- 下方规则级健康状态仍照常展示

## 9. 统一名单的状态模型

统一名单展示的是“目标级探测活动状态”，不是规则级健康状态。

状态定义如下：

| 状态 | 含义 | 是否会主动探测 |
| --- | --- | --- |
| `Unsupported` | 目标可见，但当前不能纳入主动检测 | 否 |
| `Off` | 手动关闭，未纳入主动检测 | 否 |
| `Pending First Probe` | 已开启，但还没有真实访问，等待首检 | 仅在 `Hybrid` 模式执行 |
| `Eligible` | 已开启，满足常规主动检测资格 | 是 |
| `Paused (Idle)` | 已开启，但因长时间无真实访问而暂停常规主动检测 | 否 |

补充说明：

- 上表描述的是目标级探测活动
- 规则级 `Cooldown` 不在这张统一名单里直接显示为单一状态
- 统一名单通过 `Rule Health Summary` 展示“当前有多少条规则处于 cooldown”
- 当全局模式为 `Passive only` 时，`Pending First Probe` 与 `Eligible` 都不会真正执行主动探测

### 9.1 `Unsupported`

进入条件：

- 缺少 `Key ID`
- 或当前不支持主动检测

行为：

- 仍显示在统一名单中
- 不能开启主动检测
- 不参与首检、常规主动检测与空闲暂停判断

### 9.2 `Off`

进入条件：

- 使用者手动关闭该目标的检测开关

行为：

- 不参与主动检测
- 不参与统一名单里的探测活动判断
- 清空该目标的首检等待标记

恢复方式：

- 只能手动重新开启

### 9.3 `Pending First Probe`

进入条件：

- 目标为 `Supported`
- 使用者刚开启检测
- 当前还没有 `last_real_access_at`

行为：

- 不等待生产流量
- 在 `Hybrid` 模式下立即安排一次首检
- 首检只更新主动检测元数据，不伪造真实访问时间

首检结果：

- 成功后进入 `Eligible`
- 失败后保留最近探测结果，并按现有主动探测语义影响引用该目标的规则级健康状态

### 9.4 `Eligible`

进入条件：

- 目标为 `Supported`
- 已开启检测
- 不在首检等待中
- 最近真实访问未超过空闲阈值

行为：

- 正常参与常规主动检测

### 9.5 `Paused (Idle)`

进入条件：

- 目标为 `Supported`
- 已开启检测
- 当前不是 `Pending First Probe`
- 超过 `Idle Pause Minutes` 没有真实访问

行为：

- 暂停常规主动检测
- 不代表目标不可用
- 仍保留在统一名单中

恢复方式：

- 一旦再次收到真实访问，自动恢复为 `Eligible`

## 10. 规则级健康状态模型

规则级健康状态不变，继续保持当前语义：

- 失败计数按规则分别记录
- consecutive failures 按规则分别记录
- cooldown 按规则分别计算
- 同一个目标可以在不同规则下同时处于不同健康状态

统一名单不能直接覆盖这部分结果。

页面上的职责分工是：

- 统一名单回答“这个目标有没有被纳入主动检测、最近有没有探测、是否因为空闲暂停”
- 规则级健康表回答“这条规则现在还会不会选这个目标”

## 11. 状态优先级

统一名单中的 `Probe State` 按以下优先级决定：

1. `Unsupported`
2. `Off`
3. `Pending First Probe`
4. `Paused (Idle)`
5. `Eligible`

解释：

- 不支持优先于一切，避免误开关
- 手动关闭优先于自动状态
- 首检等待优先于空闲暂停

## 12. 真实访问与探测元数据

本设计要求把“真实访问”和“主动探测”拆成两组独立字段。

### 12.1 真实访问定义

`last_real_access_at` 只计算：

- 通过智能路由实际打到该目标的请求

明确不包含：

- 未经过智能路由的普通请求
- 其他页面或其他功能的直接调用
- 主动探测请求

### 12.2 主动探测定义

主动探测单独记录：

- `last_probe_at`
- `last_probe_result`
- `last_probe_error`

### 12.3 为什么必须拆开

当前系统里的 `lastObservedAt` 会同时被真实请求和主动探测更新，这不足以支撑新页面。

如果不拆开：

- `Last Real Access` 会被主动探测污染
- `Paused (Idle)` 会算错
- 首检是否完成也无法判断

因此，统一名单不能直接复用当前唯一的 `lastObservedAt` 作为核心来源。

## 13. 交互设计

### 13.1 全局设置

继续采用“统一编辑、统一保存”的交互。

原因：

- 这些参数会整体影响系统行为
- 多字段一起调整时更容易控制

保存成功：

- toast：`Health detection settings updated`

保存失败：

- toast：`Failed to update health detection settings`

### 13.2 目标开关

目标开关采用“逐行立即生效”。

行为定义：

| 操作 | 结果 |
| --- | --- |
| 对 `Unsupported` 目标点击开关 | 不允许操作，显示原因 |
| 关闭开关 | 立即变成 `Off` |
| 打开开关，且已有真实访问 | 进入 `Eligible` |
| 打开开关，但没有真实访问 | 进入 `Pending First Probe` |

### 13.3 首检

首检是本次设计新增的明确行为。

规则：

- 只对 `Supported` 目标生效
- 只在 `Hybrid` 模式下执行
- 在目标第一次被开启、且没有真实访问记录时触发
- 成功后写入 `last_probe_at` 和 `last_probe_result=success`
- 失败后写入失败结果，并按现有主动探测语义影响引用它的规则

### 13.4 全局模式切换

切到 `Passive only`：

- 停止常规主动检测
- 停止首检
- 统一名单与开关保留
- `Probe State` 不强制改成 `Off`
- 页面明确说明“当前只使用真实访问结果”

切回 `Hybrid`：

- 重新评估所有支持型且已开启目标
- 无真实访问且待首检的，恢复 `Pending First Probe`
- 长期无访问的，进入 `Paused (Idle)`
- 满足条件的，进入 `Eligible`

## 14. 目标来源与生命周期

### 14.1 新目标加入

当某个具体目标第一次出现在智能路由规则中：

- 自动进入统一名单
- 若为 `Supported`，默认 `Detection Enabled = false`
- 默认 `Probe State = Off`
- 若为 `Unsupported`，显示为只读行

### 14.2 目标被移除

当某个目标不再被任何智能路由规则引用：

- 从页面当前名单中隐藏
- 停止主动检测
- 不再参与运行态计算

但它的目标级偏好不删除。

### 14.3 目标重新加入

当同一个目标再次被某条智能路由规则引用：

- 重新进入统一名单
- 恢复上一次保存的目标级开关选择
- 若恢复为开启且无真实访问，则进入 `Pending First Probe`

## 15. 持久化与运行态边界

### 15.1 需要长期保存的内容

本次设计新增一份“目标级偏好存储”。

建议新增专用表，例如：

- `health_detection_target_preferences`

建议字段：

| 字段 | 说明 |
| --- | --- |
| `target_key` | 稳定键，映射 `provider + model + key_id` |
| `provider` | 提供方 |
| `model` | 模型名 |
| `key_id` | 站点或凭证标识，可为空 |
| `detection_enabled` | 手动开关结果 |
| `updated_at` | 最近修改时间 |

这张表负责：

- 新目标默认关闭
- 逐行即时开关
- 目标移除后再加入时恢复上次选择

### 15.2 v1 运行态范围

v1 明确采用 `node-local` 运行态。

这意味着以下字段由当前节点本地维护，不保证多实例一致：

- `Probe State`
- `last_real_access_at`
- `last_probe_at`
- `last_probe_result`
- `last_probe_error`

页面必须显示明确提示，例如：

- `Runtime activity is shown for the current gateway node.`

这不是开放问题，而是 v1 的明确边界。

### 15.3 为什么 v1 不做共享运行态

- 现有健康状态本来就是进程内内存态
- 每次真实请求都写共享存储会带来高频写入成本
- 本轮优先交付统一控制能力，不在第一版引入分布式运行态聚合

## 16. 接口与数据模型建议

### 16.1 全局配置接口

继续使用或扩展现有全局接口：

- `GET /api/governance/health-detection-config`
- `PUT /api/governance/health-detection-config`

新增字段：

- `idle_pause_minutes`

### 16.2 统一名单接口

建议新增：

- `GET /api/governance/health-detection-targets`
- `PUT /api/governance/health-detection-targets/{target_id}`

其中：

- `GET` 返回去重后的目标列表、支持状态、目标级偏好与目标级活动视图
- `PUT` 只更新某个目标的 `detection_enabled`

推荐返回字段：

| 字段 | 说明 |
| --- | --- |
| `target_id` | 页面和接口使用的稳定目标标识 |
| `provider` | 提供方 |
| `model` | 模型名 |
| `key_id` | 站点或凭证标识，可为空 |
| `referenced_rule_ids` | 当前引用它的规则 |
| `referenced_rule_names` | 当前引用它的规则名 |
| `support_status` | `supported / unsupported` |
| `support_reason` | 不支持时给出原因 |
| `detection_enabled` | 是否手动开启 |
| `probe_state` | `unsupported / off / pending_first_probe / eligible / paused_idle` |
| `rule_health_summary` | 例如 `cooldown_rule_count` 和 `total_rule_count` |
| `last_real_access_at` | 最近真实访问 |
| `last_probe_at` | 最近主动检测 |
| `last_probe_result` | 最近主动检测结果 |
| `last_probe_error` | 最近主动检测错误 |
| `runtime_scope` | 固定返回 `node_local` |

### 16.3 规则级健康接口

现有：

- `GET /api/governance/health-status`

继续保留，并作为页面下半部分唯一权威来源。

统一名单接口不能替代它。

## 17. 页面文案建议

### 17.1 全局说明

- `Passive only`: `Use real request outcomes only. No background probes.`
- `Hybrid (Passive + Active)`: `Use passive signals first. When a target has not been reached by real traffic, the gateway can validate it with a lightweight probe.`

### 17.2 统一名单状态说明

| 状态 | 文案 |
| --- | --- |
| `Unsupported` | `This target is visible but cannot be enrolled in active probing.` |
| `Off` | `Active probing is turned off for this target.` |
| `Pending First Probe` | `The target is enabled and waiting for an initial validation probe.` |
| `Eligible` | `The target is enabled and eligible for background probing.` |
| `Paused (Idle)` | `Background probing is paused because this target has not received recent real traffic.` |

### 17.3 页面级提示

当模式为 `Passive only`：

- `Background probing is disabled globally. Only real request outcomes update rule health.`

当运行态为 `node-local`：

- `Runtime activity reflects the current gateway node only.`

当当前没有任何智能路由规则：

- `No eligible targets found. Add grouped health routing targets to manage detection here.`

## 18. 风险与处理

| 风险 | 说明 | 处理方式 |
| --- | --- | --- |
| 统一名单和规则级状态混淆 | 使用者会把 `Probe State` 当成最终路由状态 | 页面拆成两段，并明确规则级健康表是权威来源 |
| 同一目标跨规则配置冲突 | 多条规则都引用同一目标 | 统一名单只统一目标级开关，不统一规则级健康状态 |
| 既有无 `Key ID` 目标被页面隐藏 | 上线后会出现“线上目标消失” | 无 `Key ID` 目标继续显示，但标为 `Unsupported` |
| 新开启目标仍要等流量才能验证 | 控制台价值不足 | 新增首检 |
| 长期无流量目标持续被探测 | 浪费资源，造成噪音 | 增加 `Idle Pause Minutes` |
| 多实例页面看起来像统一视图 | 实际状态会不一致 | 文档明确 v1 仅承诺 `node-local` 运行态，并在页面加提示 |

## 19. 验收标准

满足以下条件即可认为本次设计完成：

- 使用者能在 `Adaptive Routing` 页面统一配置全局检测规则
- 使用者能在同一页中看到去重后的具体目标名单
- 同一目标即使被多条规则引用，统一名单中也只显示一行
- 统一名单中的开关不再与规则级健康状态冲突
- 无 `Key ID` 目标不会消失，而是以 `Unsupported` 形式显示
- 新目标默认关闭，不会自动开始探测
- 新开启且没有真实访问的支持型目标会执行首检
- 超过空闲阈值没有真实访问时，目标自动进入 `Paused (Idle)`
- 一旦再次收到符合条件的真实访问，目标自动恢复 `Eligible`
- 页面下半部分继续保留规则级健康状态，且仍是路由健康的权威来源
- 页面明确告知 v1 的运行态为 `node-local`

## 20. 推荐实现顺序

1. 新增目标级偏好存储
2. 新增统一名单接口
3. 在运行态中拆分 `last_real_access_at` 与 `last_probe_at`
4. 加入首检调度
5. 在 `Adaptive Routing` 页面接入统一名单表格
6. 保留现有规则级健康表并补充关系说明

## 21. 非阻塞后续项

以下内容不阻塞本轮实现，但可以作为后续阶段继续推进：

- 多实例共享运行态
- 统一名单中的跨节点聚合视图
- 规则列表页展示某个目标是否已纳入主动检测
- 更丰富的首检历史记录与失败排查入口
