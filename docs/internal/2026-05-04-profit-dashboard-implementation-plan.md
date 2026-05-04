# 利润统计面板实施计划

目标：按设计文档落地一个中文“利润统计”页面，保存出售价格，并把每条 LLM 请求按当时售价沉淀为独立利润账本。

## 架构

- 后端在 `framework/logstore` 增加 `profit_settings` 和 `profit_events` 两张表，利润数据不依赖 `logs` 的生命周期。
- `plugins/logging` 在日志写入、deferred usage 更新、成本重算后同步 upsert 利润事件。
- `transports/bifrost-http/handlers/logging.go` 暴露 `/api/profit/*` 接口。
- UI 新增 `/workspace/profit` 页面和“利润统计”菜单项，全页面中文。
- 页面同时提供“节点利润明细”，按 `provider + model` 聚合收入、成本、利润、毛利率、请求量、token 和缺失成本次数。

## 执行步骤

1. 先写 `framework/logstore` 的利润公式、持久化、清空日志不清利润测试。
2. 实现利润数据结构、默认设置、upsert、summary、daily、missing-only backfill。
3. 写并实现 logging plugin 与 handler 的利润接口测试。
4. 接入日志写入后利润 upsert、deferred usage 更新后再次 upsert、成本重算后同步利润。
5. 增加后台定时完整性校对：启动后延迟 2 分钟，每 10 分钟只补缺失的利润事件，每批最多 1000 条。
6. 增加完整性状态接口和页面状态卡片：实时显示缺失利润事件数、上次校对、下次校对和上次结果。
7. 新增中文利润统计页面、菜单、标题和 API client。
8. 新增按中转站和模型聚合的节点利润明细接口与页面表格。
9. 运行 Go 测试、UI 单测/类型检查，启动本地服务给用户预览。

## 关键口径

- 收入只在 `status=success` 时按 token 和出售价格计算。
- 失败请求收入为 0；如果已有 `cost`，成本照算，利润为负。
- `processing` 不入账。
- 每条 `profit_events` 保存当时售价，后续修改售价不影响历史。
- `ClearAllLogs` 只清日志，不清利润事件和售价设置。
- 手动回填和定时校对都只补 `logs` 存在但 `profit_events` 缺失的记录，避免重复刷新已入账历史。
- 完整性状态中的缺失数量实时查数据库；上次/下次校对时间是运行态信息，服务重启后重新计算。
