# Routing Rules 与 Adaptive Routing 局部汉化任务清单

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 只汉化 `/workspace/routing-rules` 与 `/workspace/adaptive-routing` 两个高频后台页面，让日常配置路由规则和查看自适应路由状态更直观。

**Architecture:** 不引入 i18n 框架，不做全站语言包，不修改侧边栏导航标题。直接在这两个页面相关组件内做局部中文文案替换，并保留必要技术名词，改完同步测试断言。

**Tech Stack:** Next.js 15, React 19, TypeScript, shadcn/Radix UI, Vitest.

---

## 0. 范围边界

- [ ] 只处理这两个页面：
  - `/workspace/routing-rules`
  - `/workspace/adaptive-routing`
- [ ] 不修改侧边栏导航标题：
  - 不改 `ui/components/sidebar.tsx`
  - 不改 `ui/lib/config/routeTitle.ts`
- [ ] 不引入 `next-intl`、`i18next` 或任何新 i18n 依赖。
- [ ] 不修改后端 API、配置 schema、路由算法、健康检测逻辑。
- [ ] 不翻译模型名、Provider 名、Key ID、API path、CEL 表达式、配置字段名。
- [ ] 不翻译代码示例里的字段名和请求参数。

## 1. 保留术语表

以下术语保持英文，或采用“英文 + 中文解释”的形式，不硬翻：

| 术语 | 推荐显示 |
|---|---|
| CEL | CEL |
| Provider | Provider |
| Model | Model |
| API Key | API Key |
| Virtual Key | Virtual Key |
| Key ID | Key ID |
| MCP | MCP |
| Token | Token |
| Fallback | Fallback / Fallback 兜底 |
| Retry | Retry / Retry 次数 |
| Cooldown | Cooldown / 冷却 |
| Probe | Probe / 存活探测 |
| P95 | P95 |
| P90 | P90 |
| JSON | JSON |
| Header | Header |
| Request | Request / 请求 |
| Response | Response / 响应 |
| Team | Team |
| Customer | Customer |
| Scope | 作用范围 |
| Priority | 优先级 |
| Target | 目标 |
| Route Group | 路由分组 |
| Health Policy | 健康策略 |
| Weight | 权重 |
| Expression | 表达式 |
| Rule Builder | 规则条件 |
| Load-balanced | 负载均衡 |
| Rate Limit | 速率限制 |
| Budget | 预算 |
| Threshold | 阈值 |
| Window | 窗口 |
| Timeout | 超时 |
| Deadline | Deadline / 截止时间 |
| Half-open | Half-open |

通用动作可以汉化：

| 英文 | 中文 |
|---|---|
| Save | 保存 |
| Cancel | 取消 |
| Delete | 删除 |
| Edit | 编辑 |
| Create | 创建 |
| Update | 更新 |
| Refresh | 刷新 |
| Loading | 加载中 |
| Enabled | 已启用 |
| Disabled | 已停用 |
| Status | 状态 |
| Actions | 操作 |

## 2. 文件清单

### Adaptive Routing

- `ui/app/workspace/adaptive-routing/healthStatusView.tsx`
  - 页面标题、说明、统计卡、规则健康表头、空状态。
- `ui/app/workspace/adaptive-routing/healthDetectionSettingsCard.tsx`
  - 存活探测设置卡片、表单字段、toast、错误/加载文案。
- `ui/app/workspace/adaptive-routing/healthDetectionTargetsTable.tsx`
  - 存活探测目标表、列名、空状态、错误/加载文案、开关说明。
- `ui/app/workspace/adaptive-routing/healthDetectionTargets.ts`
  - 状态标签和状态说明。
- `ui/app/workspace/adaptive-routing/healthDetectionConfig.ts`
  - `Probes On/Off` 等模式标签。
- `ui/app/workspace/adaptive-routing/healthDetectionConfig.test.ts`
  - 模式标签断言。
- `ui/app/workspace/adaptive-routing/healthDetectionTargets.test.ts`
  - 状态标签、状态说明、健康状态标签断言。

### Routing Rules

- `ui/app/workspace/routing-rules/views/routingRulesView.tsx`
  - 页面标题、页面说明、新建按钮。
- `ui/app/workspace/routing-rules/views/routingRulesEmptyState.tsx`
  - 空状态标题、说明、按钮 aria-label。
- `ui/app/workspace/routing-rules/views/routingRulesTable.tsx`
  - 表头、搜索框、空结果、删除确认框、toast。
- `ui/app/workspace/routing-rules/views/routingRuleSheet.tsx`
  - 新建/编辑抽屉、表单标签、校验错误、健康策略、分组路由、目标配置、Fallback 配置、按钮文案。
- `ui/lib/types/routingRules.ts`
  - 作用范围下拉里的 `Global` 显示名。
- `ui/lib/utils/routingRules.ts`
  - 规则列表作用范围徽章里的 `Global` 显示名。
- `ui/app/workspace/routing-rules/components/celBuilder/celRuleBuilder.tsx`
  - CEL Builder 加载文案、按钮、表达式预览、空表达式说明。
- `ui/app/workspace/routing-rules/components/celBuilder/actionButton.tsx`
  - 兼容中文 `添加条件` / `添加条件组` 按钮标签与删除按钮 aria-label。
- `ui/app/workspace/routing-rules/components/celBuilder/fieldSelector.tsx`
  - 字段选择 placeholder、Header key 提示。
- `ui/app/workspace/routing-rules/components/celBuilder/operatorSelector.tsx`
  - 操作符选择 placeholder。
- `ui/app/workspace/routing-rules/components/celBuilder/valueEditor.tsx`
  - 值输入 placeholder 和辅助说明。
- `ui/lib/config/celFieldsRouting.ts`
  - Rule Builder 中可见的字段标签、占位提示和空 Provider 状态。
- `ui/lib/config/celOperatorsRouting.ts`
  - Rule Builder 中可见的操作符标签。
- `ui/app/workspace/routing-rules/views/routeGroupState.test.ts`
  - 如果函数输出标签被汉化，更新对应断言；如果函数仅输出内部显示名，保持不动。

## 3. 推荐汉化风格

- [ ] 页面标题简洁，偏后台系统语气，不做营销文案。
- [ ] 技术名词保留英文，必要时补中文解释。
- [ ] 表单说明优先解释“这个设置会影响什么”，避免逐字直译。
- [ ] 错误提示明确指出用户需要改什么。
- [ ] 按钮使用动词，例如“保存规则”“创建规则”“删除”。
- [ ] 空状态告诉用户下一步做什么，例如“还没有路由规则，创建一条规则后请求才会按条件分流。”
- [ ] `Fallback only` 建议显示为 `Fallback 兜底组` 或 `仅 Fallback`，不要翻译成“倒退”。
- [ ] `Grouped Health Routing` 建议显示为 `分组健康路由`。
- [ ] `Liveness Probes` 建议显示为 `存活探测`。

## 4. Adaptive Routing 任务

### Task A1: 汉化存活探测模式与状态 helper

**Files:**
- Modify: `ui/app/workspace/adaptive-routing/healthDetectionConfig.ts`
- Modify: `ui/app/workspace/adaptive-routing/healthDetectionTargets.ts`
- Test: `ui/app/workspace/adaptive-routing/healthDetectionConfig.test.ts`
- Test: `ui/app/workspace/adaptive-routing/healthDetectionTargets.test.ts`

- [ ] 将 `Probes On` 改为 `存活探测开启`。
- [ ] 将 `Probes Off` 改为 `存活探测关闭`。
- [ ] 将 `Supported` 改为 `支持`。
- [ ] 将 `Unsupported` 改为 `不支持`。
- [ ] 将 `Off` 改为 `关闭`。
- [ ] 将 `Waiting for First Probe` 改为 `等待首次探测`。
- [ ] 将 `Probe Eligible` 改为 `可探测`。
- [ ] 将 `Paused: Idle` 改为 `空闲暂停`。
- [ ] 将 `Healthy` 改为 `健康`。
- [ ] 将 `Degraded` 改为 `降级`。
- [ ] 将 `Cooldown` 改为 `冷却中`。
- [ ] 更新状态说明文案，保持“存活探测不影响路由健康”的含义。
- [ ] 更新两个 test 文件里的断言。

**Run:**

```bash
cd ui
npx vitest run app/workspace/adaptive-routing/healthDetectionConfig.test.ts app/workspace/adaptive-routing/healthDetectionTargets.test.ts
```

**Expected:** 12 tests pass.

### Task A2: 汉化 Adaptive Routing 页面主体

**Files:**
- Modify: `ui/app/workspace/adaptive-routing/healthStatusView.tsx`

- [ ] 页面 H2 `Adaptive Routing` 可保留英文，副标题改为中文说明。
- [ ] 统计卡汉化：
  - `Rules with Health Routing` → `启用健康路由的规则`
  - `Targets in Unified List` → `目标总数`
  - `Probes Enabled` → `已启用探测`
- [ ] 提示卡汉化：
  - 解释 `Probe State` 只是存活活动，不是最终路由健康。
  - 解释运行时活动只代表当前 gateway 节点。
- [ ] 规则健康区汉化：
  - `Rule Health by Routing Rule` → `按路由规则查看健康状态`
  - `Probe Mode` → `探测模式`
  - 空状态和按钮文案改为中文。
- [ ] 表头汉化：
  - `Target` → `目标`
  - `Group` → `分组`
  - `Status` → `状态`
  - `Source` → `来源`
  - `Window Fail` → `窗口失败`
  - `Consecutive` → `连续失败`
  - `Slow Ratio` → `慢请求占比`
  - `Samples` → `样本`
  - `Streak` → `冷却次数`
  - `Last Observed` → `最后观测`
  - `Cooldown Until` → `冷却到`
  - `Last Failure` → `最后失败`

**Manual check:**

```bash
open http://localhost:3000/workspace/adaptive-routing
```

确认页面无文字溢出，表格横向滚动仍正常。

### Task A3: 汉化存活探测设置卡与目标表

**Files:**
- Modify: `ui/app/workspace/adaptive-routing/healthDetectionSettingsCard.tsx`
- Modify: `ui/app/workspace/adaptive-routing/healthDetectionTargetsTable.tsx`

- [ ] 设置卡标题 `Liveness Probes` → `存活探测`。
- [ ] `Background probes` → `后台存活探测`。
- [ ] Select 选项：
  - `Off - real traffic only` → `关闭 - 只看真实请求`
  - `On - liveness only` → `开启 - 仅做存活探测`
- [ ] 字段：
  - `Probe interval (seconds)` → `探测频率（秒）`
  - `Advanced probe limits` → `高级探测限制`
  - `Probe timeout (seconds)` → `探测超时（秒）`
  - `Max concurrency` → `最大并发`
  - `Idle pause (minutes)` → `空闲暂停（分钟）`
- [ ] 按钮：
  - `Discard Changes` → `放弃更改`
  - `Save` → `保存`
- [ ] toast 和错误文案改为中文。
- [ ] 目标表：
  - `Liveness Probe Targets` → `存活探测目标`
  - `Probe Enabled` → `启用探测`
  - `Probe State` → `探测状态`
  - `Last Probe` → `最后探测`
  - `Last Probe Result` → `最后探测结果`
  - `Referenced By` → `引用规则`
  - `Routing Groups` → `路由分组`
  - `Rule Health` → `规则健康`
  - `Rule Health Summary` → `规则健康汇总`
  - `Last Real Access` → `最后真实请求`
- [ ] 保留 `Provider`、`Model`、`Key ID` 英文。

**Run:**

```bash
cd ui
npx tsc --noEmit
```

**Expected:** exits 0.

## 5. Routing Rules 任务

### Task R1: 汉化列表页与空状态

**Files:**
- Modify: `ui/app/workspace/routing-rules/views/routingRulesView.tsx`
- Modify: `ui/app/workspace/routing-rules/views/routingRulesEmptyState.tsx`
- Modify: `ui/app/workspace/routing-rules/views/routingRulesTable.tsx`

- [ ] 列表页标题 `Routing Rules` → `路由规则`。
- [ ] 页面说明改为“按 CEL 条件将请求分流到不同 Provider / Model”。
- [ ] `New Rule` → `新建规则`。
- [ ] 搜索框：
  - `Search by name...` → `按名称搜索...`
  - aria-label 同步中文。
- [ ] 表头：
  - `Name` → `名称`
  - `Targets` → `目标`
  - `Scope` → `作用范围`
  - `Priority` → `优先级`
  - `Expression` → `表达式`
  - `Status` → `状态`
  - `Actions` → `操作`
- [ ] 状态：
  - `Enabled` → `已启用`
  - `Disabled` → `已停用`
- [ ] 删除确认框：
  - `Delete Routing Rule` → `删除路由规则`
  - `Cancel` → `取消`
  - `Delete` → `删除`
  - `Deleting...` → `删除中...`
- [ ] toast：
  - `Routing rule deleted successfully` → `路由规则已删除`
- [ ] 空状态文案和按钮 aria-label 改为中文。

### Task R2: 汉化规则抽屉基础信息与校验

**Files:**
- Modify: `ui/app/workspace/routing-rules/views/routingRuleSheet.tsx`

- [ ] 标题：
  - `Edit Routing Rule` → `编辑路由规则`
  - `Create New Routing Rule` → `新建路由规则`
- [ ] 描述：
  - 编辑态说明改为“更新这条路由规则的配置。”
  - 新建态说明改为“创建一条基于 CEL 条件的请求分流规则。”
- [ ] 基础字段：
  - `Description` → `描述`
  - `Enable Rule` → `启用规则`
  - `Scope` → `作用范围`
  - `Priority` → `优先级`
  - `Rule Builder` → `规则条件`
- [ ] placeholder：
  - `Describe what this rule does...` → `说明这条规则的用途...`
  - `Select scope...` → `选择作用范围...`
  - `Select a team...` → `选择 Team...`
  - `Select a customer...` → `选择 Customer...`
  - `Select a virtual key...` → `选择 Virtual Key...`
- [ ] 保留 `Team`、`Customer`、`Virtual Key` 英文。
- [ ] 校验错误改为中文：
  - rule name required
  - priority required / min / max
  - scope required
  - missing route groups / targets / weights
  - health policy 数值校验
- [ ] 保存 toast：
  - `Routing rule updated successfully` → `路由规则已更新`
  - `Routing rule created successfully` → `路由规则已创建`
- [ ] 底部按钮：
  - `Update Rule` → `更新规则`
  - `Save Rule` → `保存规则`

### Task R3: 汉化健康策略区

**Files:**
- Modify: `ui/app/workspace/routing-rules/views/routingRuleSheet.tsx`

- [ ] `Grouped Health Routing` → `分组健康路由`。
- [ ] `Health Policy` → `健康策略`。
- [ ] `Window Threshold` → `窗口阈值`。
- [ ] `Failure Window (s)` → `失败窗口（秒）`。
- [ ] `Consecutive Failures` → `连续失败`。
- [ ] `Cooldown (s)` → `Cooldown 冷却（秒）`。
- [ ] `Advanced health signals` → `高级健康信号`。
- [ ] `Slow Threshold (ms)` → `慢请求阈值（毫秒）`。
- [ ] `Slow Window Size` → `慢请求窗口大小`。
- [ ] `Slow Ratio Threshold` → `慢请求占比阈值`。
- [ ] `Slow Recovery (s)` → `慢请求恢复窗口（秒）`。
- [ ] `Soft Cooldown Multiplier` → `软失败 Cooldown 倍率`。
- [ ] `Cooldown Backoff Factor` → `Cooldown 退避倍率`。
- [ ] `Max Cooldown (s)` → `最大 Cooldown（秒）`。
- [ ] `Request Deadline (ms)` → `请求 Deadline（毫秒）`。
- [ ] `Half-open probe` → `Half-open 恢复探测`。
- [ ] 辅助说明全部改成中文，保留 `Cooldown`、`Half-open`、`Deadline` 英文。

### Task R4: 汉化分组路由和目标配置

**Files:**
- Modify: `ui/app/workspace/routing-rules/views/routingRuleSheet.tsx`

- [ ] `Route Groups` → `路由分组`。
- [ ] `Routing Targets` → `路由目标`。
- [ ] `Fallbacks` → `Fallback 兜底`。
- [ ] `No fallbacks configured` → `还没有配置 Fallback`。
- [ ] `Fallbacks will be used in the order they are defined` → `Fallback 会按配置顺序依次使用`。
- [ ] `Group Name` → `分组名称`。
- [ ] `Retry Limit` → `Retry 次数`。
- [ ] `Fallback only` → `仅作为 Fallback`。
- [ ] `Targets` → `目标`。
- [ ] `Provider...` 保留或改为 `选择 Provider...`。
- [ ] `Model...` 保留或改为 `选择 Model...`。
- [ ] `Select key (optional)` → `选择 API Key（可选）`。
- [ ] `API Key (optional — leave unset for load-balanced selection)` → `API Key（可选；留空则使用负载均衡选择）`。
- [ ] `must equal 1` → `总和必须等于 1`。
- [ ] 上移、下移、删除 aria-label 改成中文。
- [ ] `Global` 作用范围显示为 `全局`，`Team`、`Customer`、`Virtual Key` 保持英文。

### Task R5: 汉化 CEL Builder 局部文案

**Files:**
- Modify: `ui/app/workspace/routing-rules/components/celBuilder/celRuleBuilder.tsx`
- Modify: `ui/app/workspace/routing-rules/components/celBuilder/actionButton.tsx`
- Modify: `ui/app/workspace/routing-rules/components/celBuilder/fieldSelector.tsx`
- Modify: `ui/app/workspace/routing-rules/components/celBuilder/operatorSelector.tsx`
- Modify: `ui/app/workspace/routing-rules/components/celBuilder/valueEditor.tsx`
- Modify: `ui/lib/config/celFieldsRouting.ts`
- Modify: `ui/lib/config/celOperatorsRouting.ts`

- [ ] `Loading CEL builder...` → `正在加载 CEL 条件编辑器...`。
- [ ] `Add Rule` → `添加条件`。
- [ ] `Add Rule Group` → `添加条件组`。
- [ ] `CEL Expression Preview` → `CEL 表达式预览`。
- [ ] `No rules defined yet` → `还没有定义条件`。
- [ ] `Select field...` → `选择字段...`。
- [ ] `Select operator...` → `选择操作符...`。
- [ ] `has key` → `包含 key`。
- [ ] Header key placeholder 保留 `x-api-key` 示例。
- [ ] 将 `Global`、空 Provider 状态、操作符标签等页面可见英文补齐为中文。
- [ ] 保留 `Provider`、`Model`、`Header`、`Request`、`regex`、API operation 名称等技术术语。

## 6. 测试与检查

- [ ] 运行相关 Vitest：

```bash
cd ui
npx vitest run \
  app/workspace/adaptive-routing/healthDetectionConfig.test.ts \
  app/workspace/adaptive-routing/healthDetectionTargets.test.ts \
  app/workspace/routing-rules/views/routeGroupState.test.ts
```

Expected: all tests pass.

- [ ] 运行 TypeScript：

```bash
cd ui
npx tsc --noEmit
```

Expected: exits 0.

- [ ] 运行 diff 检查：

```bash
git diff --check
```

Expected: no output.

- [ ] 浏览器检查 `/workspace/routing-rules`：
  - 列表页中文正常。
  - 搜索框、空状态、删除确认框中文正常。
  - 新建/编辑抽屉中文正常。
  - Health Policy、Route Groups、Fallback、Targets 中文正常。
  - CEL Builder 没有破坏交互。
  - 专用名词没有被误翻。

- [ ] 浏览器检查 `/workspace/adaptive-routing`：
  - 存活探测设置卡中文正常。
  - 高级探测限制折叠正常。
  - 存活探测目标表中文正常。
  - 规则健康表中文正常。
  - 横向表格无明显列名挤压或异常换行。

## 7. 验收标准

- [ ] 两个页面主体文案基本中文化。
- [ ] 侧边栏仍显示原英文标题。
- [ ] 浏览器标题仍保持原英文路由标题。
- [ ] 技术名词按术语表保留，不做生硬翻译。
- [ ] 所有相关测试通过。
- [ ] `npx tsc --noEmit` 通过。
- [ ] `git diff --check` 通过。
- [ ] 本地浏览器目视检查通过。

## 8. 非目标

- [ ] 不做全站汉化。
- [ ] 不做中英文切换。
- [ ] 不新增语言包。
- [ ] 不汉化侧边栏。
- [ ] 不汉化日志详情中的原始请求/响应字段。
- [ ] 不汉化 Provider / Model / API Key 等配置数据本身。
- [ ] 不改任何线上配置。

## 9. 执行复核记录

**执行时间:** 2026-05-03

**术语复核结论:** 已补齐并执行。保留 `Provider`、`Model`、`API Key`、`Key ID`、`Virtual Key`、`Team`、`Customer`、`CEL`、`Header`、`Request`、`Fallback`、`Retry`、`Cooldown`、`Probe`、`Half-open`、`Deadline`、`P95`、`JSON`、`regex`、模型名、Provider 名、API operation 名称和配置字段名；`Global` 因为是普通作用范围标签，页面显示为 `全局`。

**实现范围:** 已完成 `/workspace/routing-rules` 与 `/workspace/adaptive-routing` 两个页面的局部汉化；未修改侧边栏导航标题、后端 API、配置 schema、路由算法或健康检测逻辑。

**额外对齐:** 复核时发现 CEL Builder 的操作符标签、空 Provider 状态和作用范围徽章也是页面可见内容，已同步纳入任务清单并完成汉化。

**已跑检查:**

```bash
cd ui
npx vitest run app/workspace/adaptive-routing/healthDetectionConfig.test.ts app/workspace/adaptive-routing/healthDetectionTargets.test.ts app/workspace/routing-rules/views/routeGroupState.test.ts
npx tsc --noEmit
cd ..
git diff --check
```

**结果:** 17 个 Vitest 测试通过，TypeScript 检查通过，`git diff --check` 通过。
