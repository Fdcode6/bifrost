# Adaptive Routing 健康检测可视化设计稿

日期：2026-04-14

## 1. 结论

主动健康探测的可视化主入口放在 `Adaptive Routing` 页面，不放进单条 `Routing Rule` 的编辑弹窗。

页面职责明确分成两层：

- `Routing Rules` 继续负责每条规则自己的健康策略
  - `failure_threshold`
  - `failure_window_seconds`
  - `cooldown_seconds`
  - `consecutive_failures`
- `Adaptive Routing` 新增全局健康检测设置
  - `Passive only`
  - `Hybrid (Passive + Active)`
  - 主动探测相关参数

这样最符合当前代码结构，也最符合使用者心智：

- 规则级参数留在规则页，不混淆作用范围
- 全局主动探测放在健康状态页，同页可设置、可观察、可验证
- `Plugins` 页面仍然保留 JSON 入口，但只作为高级入口，不作为主入口

## 2. 背景

当前混合健康探测已经在代码中实现，但配置入口仍然停留在 `governance` 插件配置层。

实际问题有三个：

1. 使用者会自然去 `Adaptive Routing` 或 `Routing Rules` 找健康检测设置，不会先想到 `Plugins`
2. 当前 UI 没有把主动探测配置显式暴露出来
3. 如果把全局主动探测直接塞进单条规则，会让人误以为这是规则级开关

因此，本次设计目标不是新增一套能力，而是把已存在的全局能力放到正确的位置，用更直观的方式呈现出来。

## 3. 设计目标

本次设计需要满足以下目标：

- 让使用者在智能路由相关页面里完成健康检测设置
- 明确区分“全局检测设置”和“单条规则健康策略”
- 让设置和当前健康状态展示在同一页面内闭环
- 在不大改底层结构的前提下复用现有接口与配置模型
- 后续即使增加多条 grouped routing 规则，页面语义仍然清楚

## 4. 方案比较

| 方案 | 做法 | 优点 | 缺点 | 结论 |
| --- | --- | --- | --- | --- |
| A | 放进单条 `Routing Rule` 编辑页 | 当前只有一条规则时看起来顺手 | 实际是全局配置，语义错误；多规则后容易误导 | 不选 |
| B | 放在 `Plugins` 页面 | 与底层配置位置一致 | 太隐蔽，不符合使用习惯 | 不选做主入口 |
| C | 放在 `Adaptive Routing` 页面，作为全局健康检测设置 | 设置、状态、验证都在一页；语义清楚 | 需要额外写清楚“作用于所有 grouped routing rules” | 选这个 |

## 5. 信息架构

### 5.1 页面归属

主入口页面：

- `/workspace/adaptive-routing`

保留页面：

- `/workspace/routing-rules`

辅助入口：

- `/workspace/plugins`

### 5.2 页面职责分工

| 页面 | 职责 |
| --- | --- |
| `Adaptive Routing` | 配置全局健康检测模式、主动探测参数、查看当前健康状态 |
| `Routing Rules` | 配置单条 grouped routing 规则的健康策略与 route groups |
| `Plugins` | 高级 JSON 配置入口，保留但不作为主入口 |

## 6. 页面结构设计

`Adaptive Routing` 页面调整为三段式布局。

### 6.1 顶部 Header

左侧：

- 标题：`Adaptive Routing`
- 副标题：`Configure health detection for grouped routing and monitor current target status.`

右侧：

- `Refresh`

作用：

- `Refresh` 手动刷新当前状态与全局设置

### 6.2 全局设置卡

卡片标题：

- `Health Detection`

卡片说明：

- `Choose how grouped routing targets are monitored and recovered.`

卡片头部右侧放一个轻量入口：

- `Open Routing Rules`

作用：

- 让使用者快速跳到规则配置页
- 不和页面右上角的 `Refresh` 竞争视觉优先级

卡片内容分成两层。

第一层：检测模式

- `Passive only`
- `Hybrid (Passive + Active)`

第二层：主动探测参数，仅在 `Hybrid` 模式下展开或高亮展示

- `Probe interval`
- `Passive freshness window`
- `Probe timeout`
- `Max concurrency`

卡片底部固定显示：

- `Applies globally to all grouped health routing rules.`

### 6.3 健康状态区

沿用现有 `Health Status` 页面主体，但位置下移到全局设置卡之后。

保留内容：

- 规则数量统计
- 可用目标数量
- cooldown 目标数量
- 每条规则下的目标健康表格

继续展示：

- `Status`
- `Window Fail`
- `Consecutive`
- `Cooldown Until`
- `Last Failure`
- `Last Observed`
- `Source`

## 7. 推荐线框图

```text
+-------------------------------------------------------------------+
| Adaptive Routing                                              [Refresh] |
| Configure health detection for grouped routing...                 |
+-------------------------------------------------------------------+
| Health Detection                                 [Open Routing Rules] |
| Choose how grouped routing targets are monitored and recovered.   |
|                                                                   |
| Detection Mode                                                    |
| ( ) Passive only                                                  |
| (o) Hybrid (Passive + Active)                                     |
|                                                                   |
| Probe interval                [ 15 ] seconds                      |
| Passive freshness window      [ 30 ] seconds                      |
| Probe timeout                 [  5 ] seconds                      |
| Max concurrency               [  4 ] targets                      |
|                                                                   |
| Applies globally to all grouped health routing rules.             |
|                                      [Discard Changes] [Save]     |
+-------------------------------------------------------------------+
| Detection Mode: Hybrid (Passive + Active)                         |
| Rules with Health Routing | Available Targets | In Cooldown       |
+-------------------------------------------------------------------+
| Rule A                                                            |
|  policy: threshold=2 window=30s cooldown=30s consecutive=2        |
|  target table ...                                                 |
+-------------------------------------------------------------------+
| Rule B                                                            |
|  target table ...                                                 |
+-------------------------------------------------------------------+
```

## 8. 字段设计

### 8.1 模式字段

UI 层增加一个显式模式字段：

- `Passive only`
- `Hybrid (Passive + Active)`

它不是后端新增字段，而是对现有配置的更直白包装。

对外接口以 `mode` 作为唯一的模式字段，不要求前端直接处理 `active_health_probe_enabled`。

映射关系：

| UI 模式 | 后端配置 |
| --- | --- |
| `Passive only` | `active_health_probe_enabled = false` |
| `Hybrid (Passive + Active)` | `active_health_probe_enabled = true` |

第一版不提供 `Active only` 模式，原因是这会违反当前混合探测的设计原则。

### 8.2 主动探测参数

| 字段 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `Probe interval` | 秒 | `15` | 后台多久扫描一次可探测目标 |
| `Passive freshness window` | 秒 | `30` | 如果目标在这个窗口内已有观测，则跳过主动探测 |
| `Probe timeout` | 秒 | `5` | 单次主动探测的超时时间 |
| `Max concurrency` | 整数 | `4` | 单轮最多并发探测多少个目标 |

### 8.3 数值校验

第一版沿用现有 schema 的基础约束：

- `Probe interval >= 1`
- `Passive freshness window >= 1`
- `Probe timeout >= 1`
- `Max concurrency >= 1`

第一版不额外增加下面这类联动限制：

- `Probe interval` 必须大于等于 `Probe timeout`
- `Passive freshness window` 必须大于等于 `Probe interval`

原因：

- 当前主动探测循环本身是串行轮转，不会因为 `timeout` 大于 `interval` 就并发叠加同一轮扫描
- 某些部署场景下，使用者可能就是希望“每次扫描时，只要目标不新鲜就探测”
- 这两类关系更适合作为建议值提示，不适合第一版直接做成硬阻断

### 8.4 文案要求

所有字段旁边都要给简短说明，重点避免以下误解：

- 误以为这是单条规则配置
- 误以为 `Probe interval` 等于页面刷新频率
- 误以为开启主动探测后会替代被动检测

推荐辅助文案：

- `Passive only`: `Use real request outcomes only. No background probes.`
- `Hybrid (Passive + Active)`: `Use passive signals first. When a target has not been observed recently, run a lightweight active probe.`

页面顶部或健康状态摘要区增加一个只读标签：

- `Detection Mode: Passive only`
- `Detection Mode: Hybrid (Passive + Active)`

这样在查看目标状态时，使用者能直接知道当前系统是纯被动还是混合模式。

## 9. 交互设计

### 9.1 初始加载

页面进入时同时读取：

- 当前 grouped routing 健康状态
- 当前主动探测全局配置

推荐改成两个独立接口：

- `GET /api/governance/health-status`
- `GET /api/governance/health-detection-config`

其中：

- `health-status` 只负责状态快照
- `health-detection-config` 只负责返回当前生效的全局检测模式与主动探测参数

如果当前没有持久化配置记录：

- `health-detection-config` 仍返回当前运行中的生效值
- 页面不暴露“尚未创建插件配置记录”这类内部细节
- 对使用者只表现为“当前设置值”

如果 `configStore` 不可用：

- 页面仍显示当前生效值
- 设置区进入只读态
- 显示说明：`Health detection is currently managed by config.json. Enable a config store to edit settings here.`

### 9.2 编辑态

当用户修改任一字段后：

- 显示页面级 dirty 状态
- 启用 `Save`
- 启用 `Discard Changes`

未修改时：

- `Save` 禁用
- `Discard Changes` 禁用

### 9.3 模式切换

切到 `Passive only`：

- 参数区保留展示但置灰
- 保存后只更新 `mode=passive`
- 原有参数值保留，不清空

切到 `Hybrid`：

- 参数区恢复可编辑
- 若之前有保存值则回显保存值
- 若无保存值则回显默认值

### 9.4 保存反馈

保存成功：

- toast：`Health detection settings updated`

保存失败：

- toast：`Failed to update health detection settings`
- 页面保留用户当前输入，不强制回滚

`Discard Changes` 的语义固定为：

- 恢复到服务端最近一次已保存的值
- 不恢复到默认值
- 不清空用户已有设置

### 9.5 空状态

如果当前没有任何 grouped routing rule：

- 全局设置卡仍然显示并可保存
- 健康状态区域显示空态
- 空态附带按钮：`Create Routing Rule`

## 10. 数据读取与保存设计

### 10.1 最小风险原则

第一版不新增专用配置表，但新增一个专用接口，避免直接复用通用 `Plugins` 更新接口。

原因：

- 现有 `PUT /api/plugins/{name}` 是整包覆盖配置，不适合只写主动探测这 5 个字段
- 如果直接复用通用插件更新接口，容易把 `governance` 其他配置一并覆盖掉
- 插件记录不存在时，通用更新链路还可能把 `Enabled` 处理错，导致内置 `governance` 被错误停用

### 10.2 新增接口

推荐新增两个接口：

- `GET /api/governance/health-detection-config`
- `PUT /api/governance/health-detection-config`

`GET` 响应结构建议包含：

- `mode`
- `active_health_probe_interval_seconds`
- `active_health_probe_passive_freshness_seconds`
- `active_health_probe_timeout_seconds`
- `active_health_probe_max_concurrency`
- `editable`
- `read_only_reason`（可选）

其中：

- `mode` 为唯一的模式字段
- `editable` 和 `read_only_reason` 只出现在响应中，不属于前端提交字段

`PUT` 请求体建议包含：

- `mode`
- `active_health_probe_interval_seconds`
- `active_health_probe_passive_freshness_seconds`
- `active_health_probe_timeout_seconds`
- `active_health_probe_max_concurrency`

`PUT` 成功响应建议与 `GET` 保持同一结构，直接返回最新完整配置视图，避免前端保存后再额外补一次读取。

HTTP 状态建议：

- `GET` 正常返回 `200`
- `GET` 在 `configStore` 不可用时仍返回 `200`，并通过 `editable=false` 表达只读
- `PUT` 成功返回 `200`
- `PUT` 参数校验失败返回 `400`
- `PUT` 当前不可编辑时返回 `409`

### 10.3 读取来源

`GET /api/governance/health-detection-config` 的读取逻辑：

1. 读取当前运行中的 `governance` 插件生效配置
2. 如果存在持久化插件配置记录，用于补充来源信息或回写基础
3. 如果没有持久化记录，仍返回当前运行值
4. 如果 `configStore` 不可用，返回 `editable=false`

这样可以保证：

- 页面永远能看到真实生效值
- 纯配置文件模式下也能只读展示
- 不要求先有一条 `governance` 插件记录

### 10.4 保存方式

`PUT /api/governance/health-detection-config` 的保存逻辑：

1. 先读取现有 `governance` 插件完整配置
2. 将请求体中的 `mode` 映射到内部 `active_health_probe_enabled`
3. 只更新主动探测相关字段
4. 保留 `governance` 其他配置不变
5. 若配置记录不存在，则创建新的 `governance` 配置记录
6. 创建时必须保证不会把内置 `governance` 插件错误置为禁用
7. 保存后重新加载运行中的 `governance` 插件配置
8. 返回最新完整配置视图给前端

写入范围仅限：

- `mode`（服务端内部映射为 `active_health_probe_enabled`）
- `active_health_probe_interval_seconds`
- `active_health_probe_passive_freshness_seconds`
- `active_health_probe_timeout_seconds`
- `active_health_probe_max_concurrency`

这个接口内部可以继续复用插件配置存储，但对前端屏蔽“插件整包覆盖”这类风险。

### 10.5 为什么不直接复用 Plugins API

第一版明确不建议前端直接调用：

- `GET /api/plugins/governance`
- `PUT /api/plugins/governance`

原因：

- 前端自己 merge 容易遗漏字段
- 并发写入时更容易产生覆盖问题
- 页面不应承担内置插件启停语义
- `Adaptive Routing` 只需要健康检测设置，不应直接暴露插件层细节

## 11. 与现有页面的边界

### 11.1 `Routing Rules` 页面保留内容

继续保留：

- `Grouped Health Routing` 开关
- `Health Policy`
- `Route Groups`

不新增：

- 主动探测开关
- 主动探测频率
- 主动探测超时
- 主动探测并发

### 11.2 `Plugins` 页面角色

`Plugins` 页面继续保留 JSON 级别配置能力，但只承担高级入口角色。

使用者原则上不需要去这里完成日常健康检测设置。

如果 `Plugins` 页面改了同一组配置：

- `Adaptive Routing` 页面在重新进入或手动刷新后重新读取最新值
- 不做页面间的即时双向联动
- 保持单一数据源，一次刷新即可对齐

## 12. 实现边界建议

第一版只做以下能力：

- `Adaptive Routing` 页面增加全局健康检测设置卡
- 通过专用治理接口读写主动探测配置
- 继续展示已有健康状态与观测元数据
- 从页面直接跳转 `Routing Rules`

第一版不做：

- 单条规则级主动探测开关
- 不同规则使用不同主动探测频率
- `Plugins` 页面与 `Adaptive Routing` 页面双向复杂联动
- 第二套专门的后端配置模型

## 13. 风险与处理

| 风险 | 说明 | 处理方式 |
| --- | --- | --- |
| 作用范围误解 | 使用者可能以为设置只影响当前规则 | 页面内固定显示“Applies globally...” |
| 配置覆盖 | 直接走 Plugins 通用更新接口可能覆盖其他治理配置 | 改为专用治理接口，服务端只更新主动探测相关字段 |
| 配置来源隐蔽 | `governance` 配置记录可能不存在 | 通过专用读取接口返回当前生效值，保存时安全创建记录 |
| 无持久化存储 | 纯配置文件模式下无法在线写入 | 设置区只读展示，并给出说明 |
| 模式理解错误 | 可能误以为主动探测会取代被动检测 | 模式文案明确写成 `Hybrid` |
| 页面职责混乱 | 规则级与全局级设置混在一起 | 保持两个页面职责清晰分离 |

## 14. 验收标准

满足以下条件即可认为本次可视化设计成功：

- 使用者能在 `Adaptive Routing` 页面内完成主动探测开关与参数设置
- 使用者能清楚区分规则级健康策略与全局主动探测设置
- 页面文案能明确说明主动探测是全局设置
- 没有 grouped routing 规则时，页面仍可配置或查看全局设置
- 不需要进入 `Plugins` 页面也能完成常规设置
- 页面下方能继续直接看到当前健康状态变化结果
- `governance` 其他配置不会因为本页保存而被覆盖
- `configStore` 不可用时，页面能正确只读展示当前设置

## 15. 后续建议

如果第一版上线后使用体验稳定，下一步再考虑：

- 在 `Routing Rules` 列表页显示某条规则是否受全局主动探测影响
- 增加最近一次主动探测结果摘要
- 为全局设置补充更细的帮助说明或文档链接
