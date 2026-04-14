# Hybrid Health Probing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 grouped routing 增加“被动优先、静默后主动补探测”的混合健康探测，并在健康状态页面展示最后观测时间与来源。

**Architecture:** 继续复用 `HealthTracker` 作为唯一健康状态源，在 `governance` 内新增后台探测器，只有目标超过保鲜窗口没有新鲜观测时才触发最小探测请求。选路逻辑保持不变，页面直接读取扩展后的健康快照。

**Tech Stack:** Go, Bifrost core client, governance plugin, FastHTTP handler, React/Next.js UI, Go tests

---

## 文件结构

### 会修改

- `plugins/governance/health_tracker.go`
  - 增加最近观测时间、来源、请求类型
  - 增加主动探测所需的读取与更新方法
  - 调整 success 恢复行为

- `plugins/governance/main.go`
  - 扩展 plugin 配置
  - 在被动结果写回时补充观测元信息
  - 增加 client 注入和主动探测生命周期管理

- `transports/bifrost-http/server/server.go`
  - 在 Bifrost client 初始化完成后注入 governance plugin

- `transports/bifrost-http/handlers/governance.go`
  - 暴露扩展后的健康快照字段

- `ui/app/workspace/adaptive-routing/healthStatusView.tsx`
  - 展示最后观测时间与来源

### 会新增

- `plugins/governance/active_probe.go`
  - 后台扫描与主动探测实现

- `plugins/governance/active_probe_test.go`
  - 主动探测核心逻辑测试

### 会修改的测试

- `plugins/governance/health_tracker_test.go`
- 视实现需要补充 `plugins/governance/routing_test.go`

## Task 1: 固化需求与边界

**Files:**
- Create: `docs/superpowers/specs/2026-04-14-hybrid-health-probing-requirements.md`
- Create: `docs/superpowers/plans/2026-04-14-hybrid-health-probing-implementation.md`

- [x] **Step 1: 写需求清单**

- [x] **Step 2: 写实施计划**

- [x] **Step 3: 自审范围与验收标准**

检查点：

- 范围是否只覆盖 grouped routing
- 是否明确排除了无明确 `provider/model` 的 target
- 是否把页面展示增强也纳入本次

## Task 2: 先写 HealthTracker 失败测试

**Files:**
- Modify: `plugins/governance/health_tracker_test.go`
- Modify: `plugins/governance/health_tracker.go`

- [ ] **Step 1: 写 failing tests**

覆盖：

- success 之后立即清理 cooldown
- health snapshot 带出最后观测时间
- health snapshot 带出来源
- health snapshot 带出最近请求类型

- [ ] **Step 2: 运行单测确认失败**

Run: `go test ./plugins/governance -run 'TestHealthTracker_'`

- [ ] **Step 3: 实现最小改动让测试通过**

- [ ] **Step 4: 再跑单测确认通过**

Run: `go test ./plugins/governance -run 'TestHealthTracker_'`

## Task 3: 写主动探测筛选与状态写回测试

**Files:**
- Create: `plugins/governance/active_probe_test.go`
- Create: `plugins/governance/active_probe.go`

- [ ] **Step 1: 写 failing tests**

覆盖：

- 有新鲜观测时不探测
- 超过静默窗口才探测
- 只探测有明确 `provider/model` 的 target
- 只探测支持的请求类型
- 探测结果能写回多个 rule-scoped bucket

- [ ] **Step 2: 运行单测确认失败**

Run: `go test ./plugins/governance -run 'TestActiveProbe_'`

- [ ] **Step 3: 实现最小主动探测器**

- [ ] **Step 4: 再跑单测确认通过**

Run: `go test ./plugins/governance -run 'TestActiveProbe_'`

## Task 4: 接通 governance 生命周期

**Files:**
- Modify: `plugins/governance/main.go`
- Modify: `transports/bifrost-http/server/server.go`

- [ ] **Step 1: 写 failing tests 或补现有测试**

覆盖：

- 被动结果写入观测元信息
- governance 拿到 client 后才启动主动探测
- cleanup 能停止后台任务

- [ ] **Step 2: 运行对应测试确认失败**

Run: `go test ./plugins/governance -run 'TestGovernance'`

- [ ] **Step 3: 实现生命周期与 client 注入**

- [ ] **Step 4: 再跑测试确认通过**

Run: `go test ./plugins/governance -run 'TestGovernance'`

## Task 5: 扩展健康状态接口与页面

**Files:**
- Modify: `transports/bifrost-http/handlers/governance.go`
- Modify: `ui/app/workspace/adaptive-routing/healthStatusView.tsx`

- [ ] **Step 1: 补前后端字段测试或最小验证点**

覆盖：

- 接口返回 `last_observed_at`
- 接口返回 `last_observation_source`
- 页面能展示来源与时间

- [ ] **Step 2: 实现最小展示改动**

- [ ] **Step 3: 跑最小验证**

Run: `go test ./transports/bifrost-http/...`

如果前端测试太重，至少确认类型和构建不报错。

## Task 6: 回归 grouped routing 行为

**Files:**
- Modify: `plugins/governance/routing_test.go`

- [ ] **Step 1: 补一个回归测试**

覆盖：

- 目标主动恢复后，rule 可以重新选中该 target

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./plugins/governance -run 'TestBuildGroupedRoutingDecision_'`

- [ ] **Step 3: 做最小修正**

- [ ] **Step 4: 重新运行测试**

Run: `go test ./plugins/governance -run 'TestBuildGroupedRoutingDecision_'`

## Task 7: 全量验证

**Files:**
- Modify: `plugins/governance/health_tracker.go`
- Modify: `plugins/governance/main.go`
- Create: `plugins/governance/active_probe.go`
- Modify: `transports/bifrost-http/server/server.go`
- Modify: `transports/bifrost-http/handlers/governance.go`
- Modify: `ui/app/workspace/adaptive-routing/healthStatusView.tsx`

- [ ] **Step 1: 跑 governance 相关测试**

Run: `go test ./plugins/governance/...`

- [ ] **Step 2: 跑 transport 相关测试**

Run: `go test ./transports/bifrost-http/...`

- [ ] **Step 3: 如有前端类型检查脚本，跑最小前端验证**

建议命令：

- `pnpm --dir ui lint`
- 或 `pnpm --dir ui typecheck`

- [ ] **Step 4: 对照需求清单逐项验收**

核对文档：`docs/superpowers/specs/2026-04-14-hybrid-health-probing-requirements.md`

- [ ] **Step 5: 做最终代码复审**

重点检查：

- 是否有递归探测风险
- 是否会误记治理成本
- 是否会在无明确 target 时误发探测
- 是否会把所有流量挡死
