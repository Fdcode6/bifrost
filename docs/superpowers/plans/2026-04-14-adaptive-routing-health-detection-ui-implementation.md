# Adaptive Routing Health Detection UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a visual health detection settings experience to `Adaptive Routing`, backed by dedicated governance APIs that safely read and update active probe settings without overwriting unrelated governance configuration.

**Architecture:** Introduce a small adaptive-routing-focused handler for health status and health detection settings, expose a dedicated RTK Query API surface, and add a focused settings card above the existing health status table. Reuse current governance runtime state for reads, and reload governance through the existing plugin lifecycle after persisted updates.

**Tech Stack:** Go, fasthttp handlers, RTK Query, React, Next.js, TypeScript, Vitest, Go test, Docker

---

### Task 1: Plan And File Map

**Files:**
- Create: `docs/superpowers/plans/2026-04-14-adaptive-routing-health-detection-ui-implementation.md`
- Modify: `transports/bifrost-http/server/server.go`
- Modify: `transports/bifrost-http/handlers/governance.go`
- Create: `transports/bifrost-http/handlers/adaptive_routing.go`
- Create: `transports/bifrost-http/handlers/adaptive_routing_test.go`
- Modify: `plugins/governance/main.go`
- Modify: `plugins/governance/active_probe.go`
- Modify: `ui/lib/types/routingRules.ts`
- Modify: `ui/lib/store/apis/routingRulesApi.ts`
- Create: `ui/app/workspace/adaptive-routing/healthDetectionSettingsCard.tsx`
- Create: `ui/app/workspace/adaptive-routing/healthDetectionConfig.ts`
- Create: `ui/app/workspace/adaptive-routing/healthDetectionConfig.test.ts`
- Modify: `ui/app/workspace/adaptive-routing/healthStatusView.tsx`

- [ ] Confirm final file ownership and minimal change path
- [ ] Keep routing-rule editing untouched apart from linking from Adaptive Routing

### Task 2: Backend Read API Test First

**Files:**
- Create: `transports/bifrost-http/handlers/adaptive_routing_test.go`

- [ ] Write failing tests for `GET /api/governance/health-detection-config`
- [ ] Cover running-config reads, read-only mode, and response shape
- [ ] Run targeted Go test and confirm failure

### Task 3: Backend Read API Implementation

**Files:**
- Create: `transports/bifrost-http/handlers/adaptive_routing.go`
- Modify: `plugins/governance/main.go`
- Modify: `plugins/governance/active_probe.go`
- Modify: `transports/bifrost-http/server/server.go`

- [ ] Add governance runtime getter for active probe config
- [ ] Implement adaptive routing handler with `GET /api/governance/health-detection-config`
- [ ] Register adaptive routing routes in the server
- [ ] Run targeted Go test and confirm pass

### Task 4: Backend Write API Test First

**Files:**
- Modify: `transports/bifrost-http/handlers/adaptive_routing_test.go`

- [ ] Write failing tests for `PUT /api/governance/health-detection-config`
- [ ] Cover safe merge, plugin record creation, immutable read-only mode, and reload callback
- [ ] Run targeted Go test and confirm failure

### Task 5: Backend Write API Implementation

**Files:**
- Modify: `transports/bifrost-http/handlers/adaptive_routing.go`

- [ ] Implement request validation and mode mapping
- [ ] Merge active probe fields without losing unrelated governance config
- [ ] Create or update plugin config safely, then reload governance
- [ ] Return updated full response payload
- [ ] Run targeted Go tests and confirm pass

### Task 6: Frontend Config Logic Test First

**Files:**
- Create: `ui/app/workspace/adaptive-routing/healthDetectionConfig.ts`
- Create: `ui/app/workspace/adaptive-routing/healthDetectionConfig.test.ts`

- [ ] Write failing Vitest coverage for mode mapping, defaults, and payload shaping
- [ ] Run targeted Vitest command and confirm failure

### Task 7: Frontend Data Layer

**Files:**
- Modify: `ui/lib/types/routingRules.ts`
- Modify: `ui/lib/store/apis/routingRulesApi.ts`
- Modify: `ui/app/workspace/adaptive-routing/healthDetectionConfig.ts`

- [ ] Add health detection config types
- [ ] Add RTK Query endpoints for get/update config
- [ ] Add helper functions for display mode and payload generation
- [ ] Run targeted Vitest and type-aware UI build checks

### Task 8: Adaptive Routing UI

**Files:**
- Create: `ui/app/workspace/adaptive-routing/healthDetectionSettingsCard.tsx`
- Modify: `ui/app/workspace/adaptive-routing/healthStatusView.tsx`

- [ ] Add settings card with mode switch, read-only state, dirty state, save, and discard
- [ ] Add `Detection Mode` read-only badge above health summary
- [ ] Add `Open Routing Rules` action and empty-state CTA
- [ ] Keep existing health tables intact apart from small layout integration

### Task 9: Verification

**Files:**
- Modify only if verification uncovers issues

- [ ] Run targeted Go tests for adaptive routing and governance handlers
- [ ] Run targeted governance plugin tests
- [ ] Run UI Vitest for helper coverage
- [ ] Run UI production build
- [ ] Run transport Go build

### Task 10: Docker Image

**Files:**
- No source changes unless build reveals a packaging issue

- [ ] Build the UI bundle into `transports/bifrost-http/ui`
- [ ] Build the Docker image with repo-standard target
- [ ] Record the resulting local image tags for follow-up deployment
