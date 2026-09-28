# 财务汇总消费者稳健性（0.2.8-geili.19）

> **已于 0.2.8-geili.20 停用。** 仪表盘和使用记录已换回官方的`usage_logs` + `usage_dashboard_hourly/daily`统计，财务汇总消费者已删除。迁移274删除了入队触发器，并清空事实表和事件队列。本文仅作历史记录；其中的后续计划不再推进。

## 背景：`.18` 生产实测

- 00:50 保留期清理按 5k–10k 行一批删除 `usage_logs`，每行 DELETE 触发器各入队一条财务事件和一条分组失效，一晚约 3.4 万条。队列超过 2000 条期间，读端退回原视图（慢但正确），约 5 分钟后消化完。
- 消费者整轮共用一个 2 分钟 context。发布后回填期间 4 次「canceling statement due to user request」：步骤本身没问题，是整轮预算到了。每步单独提交，所以进度不丢。但如果某一步单独就要超过剩余预算（比如繁忙日首灌、跨日），就会每轮被取消、原地打转。00:00 跨日单步实测已到 1 分 39 秒。
- 积压、事实覆盖落后、日桶未关闭都没有告警，只能靠人盯日志。

## 设计

### 1. 批量删除按天入队（迁移 273，只增）

`deleteUsageLogsCompactGeili` 用于保留期清理 `cleanupUsageLogsBatchWithRollupInvalidation` 和管理员批量清理 `deleteUsageLogsBatchWithRollupInvalidation`。它设置事务内 `geili.usage_bulk_delete=on`，两个触发器（`enqueue_usage_financial_rollup_event` 的 log DELETE 分支、`enqueue_group_usage_rollup_invalidation` 的 DELETE 分支）见到后直接返回。然后在同一条 `WITH ... DELETE ... RETURNING` 语句里自己入队：

- **精确 log 事件**：只给可能碰到 kind=1（凭证）事实的行。条件是日志日期，或任一关联凭证的 `GREATEST(accounting_date, 完成/结算日)`，不早于 `今天 - GEILI_FINANCIAL_FACT_DAYS`（覆盖起点再往前留一天余量）。关联条件是 `usage_log_id=id` 或 `(usage_request_id, api_key_id)`，任意状态，和 `usage_financial_fact_keys` 一致或更宽。
- **'day' 事件**：其余行按「日志北京日」∪「关联已结算凭证的完成/结算北京日」去重，每天一条。'day' 事件会删除并重建当天的 kind=2 事实和日汇总桶，所以 kind=2 精确无损。关联条件与 `usage_financial_event_days` 的已结算条件一致。
- **分组失效**：每个受影响的北京日一条（`group_id NULL` = 当天所有分组），这个语义已被分组汇总支持。

其他写入（插入、更新、单行删除、凭证）仍走 260/271 的逐行事件。GUC 是 `set_config(..., true)` 事务级，语句结束后立刻置回 off。

读端门槛：`financialFactSnapshot` 原先把所有 'day' 事件都算进待解析数。现在只统计「当天确有 kind=2 事实」的 'day' 事件。窗口外的旧日事件不影响近 8 天读取，正确性由测试 `TestFinancialFactSnapshotWaitsForDayWithMaterializedLegacyRows` 覆盖。

效果：一晚 3.4 万条降到约「删除天数 + 少量精确行」，通常一天一条。

### 2. 消费者单步限时

- 服务层整轮上限 15 分钟，只兜底单步。
- 仓储循环 2 分钟后不再开启新步骤，返回 nil，下一轮（10 秒后）继续。
- 每一步有独立的 10 分钟 context。
- 进入步骤前在事务里 `SELECT ... FROM usage_financial_rollup_state FOR UPDATE NOWAIT`。另一实例持锁（55P03）时本轮直接让出，不排队等一整步。
- 超过 30 秒的步骤记录耗时日志，便于发现接近上限的日期。

### 3. 积压告警

每轮结束后每 5 分钟做一次只读健康检查：队列条数、最老事件年龄、`closed_before` 与今天、事实覆盖起点与目标。

| 条件 | 日志 |
|---|---|
| 最老事件 ≥ 15 分钟，或日桶/事实覆盖落后持续 ≥ 30 分钟 | `WARN ALERT financial rollup delayed` |
| 最老事件 ≥ 60 分钟，或落后持续 ≥ 60 分钟 | `ERROR ALERT financial rollup stalled` |

发布后回填约 10 分钟、跨日首灌约 2 分钟，都在阈值以内。运维仓 `bin/billing-alert-monitor.py` 同步识别这两条消息。

## 需要业主决定：「累计消费」实际只有 90 天

保留期清理删除旧日志后，会给对应日期入 'day' 事件。消费者按源数据重建那一天，日志已删，桶就变小。所以「累计消费」「管理员累计」实际等于保留期（默认 90 天）内的合计，不是开站以来。这是既有行为，官方同样如此，`.19` 不改。

可选方案：

- **A. 维持现状**：累计 = 保留期内合计，界面文案改成「近 90 天累计」。零风险。
- **B. 保留期删除不回滚已关闭日桶**（推荐，放进 P1）：保留期清理只删明细，不再给日桶入 'day' 事件，已关闭的日汇总桶作为归档永久保留。累计 = 开站以来。需要区分「保留期删除」和「管理员纠错删除」（后者仍要回滚），并补对拍：明细已删日期以归档桶为准。

## P1：窄表

目标是把事实表从「宽行副本」换成统计专用窄表，降低磁盘和回填时间。当前约 170MB/天，8 天连索引约 1.5–2GB，生产根盘余量有限。实施前先只做设计评审，不上代码。

1. **现状测量**（只读）：事实表各列实际被哪些读路径使用（总计/趋势/模型/分组/用户/分页身份），每列平均宽度，索引占比。
2. **窄表草案**：主键 `(fact_kind, source_id)`；北京日、user/key/group/account/model/billing_type/subscription 等维度 id；金额 NUMERIC、四类 token、请求数、耗时、完整性标志。不存 JSON detail、request_id 文本和展示字段；分页身份回原表按主键取。
3. **迁移方式**：新增表与消费者双写一段时间，读端按开关切换，对拍一致后删除旧事实表的只增迁移。窗口仍由 `GEILI_FINANCIAL_FACT_DAYS` 控制。
4. **验收**：沿用 `financialFactsAssertOracle` 全口径对拍，外加生产只读体积和 EXPLAIN 对比。
5. **与方案 B 的关系**：若选 B，归档日桶沿用现有 `usage_financial_daily_rollups`，窄表只管近 N 天明细统计，两者独立。

## 验证

- 集成（真实 PostgreSQL）：
  - `TestFinancialRollupBulkDeleteQueuesCompactDayEvents`：保留期 + 管理员删除，事件形状、分组失效，脏态和消费后都通过对拍。
  - `TestFinancialFactSnapshotWaitsForDayWithMaterializedLegacyRows`。
  - `TestFinancialRollupConsumerSkipsWhileAnotherInstanceHoldsState`。
  - `TestFinancialRollupHealthReportsBacklogAndLag`。
  - 既有 Financial/Dashboard/UsageCleanup/GroupUsage 全部通过。
- 单元：sqlmock 断言清理语句包含 DELETE…RETURNING、'day' 事件和分组失效，GUC 开关成对；告警阈值表驱动测试。
