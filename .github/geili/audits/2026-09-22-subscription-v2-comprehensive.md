# 订阅 V2 全场景复验（2026-09-22）

目标候选 `0.2.7-geili.4`。本次在上一版完整验收之外，按产品规则、协议、账本、迁移、退款、竞态和UI重新建立矩阵；发现真实边界缺陷后先保留失败证据，再验证修复。

## 已执行结果

所有以下执行均在冻结源代码上完成；运行器记录前后源树哈希并确认一致。Go计数含子测试且不同构建标签间会重复，不相加为独立用例数。

| 验证 | 通过测试项 | 跳过 | 结果 |
|---|---:|---:|---|
| go_default | 11764 | 12 | exit 0 |
| go_unit | 20274 | 16 | exit 0 |
| go_integration | 12527 | 14 | exit 0 |
| subscription_payment_postgres | 10 | 0 | exit 0 |
| subscription_race | 477 | 0 | exit 0 |
| go_utc | 477 | 0 | exit 0 |

- 前端310个文件/2526测试通过，类型检查及生产构建通过，修改文件ESLint通过。
- 独立big.Int金额oracle覆盖320,300种购买组合，另有100种档位操作矩阵及350种费用守恒组合。
- 真实PostgreSQL19项迁移分类/重复执行/原始权益不变/事务回滚/时区补账，9项支付锁等待和回调竞态；48并发结算与重置、100并发扣费、缺失准入重试、实时逐轮准入及跨期对账。
- 最终全新库端到端HTTP **240项通过**。覆盖原生命周期及Chat/Responses/Messages/Gemini JSON/SSE、Embeddings、图片JSON/SSE、异步视频、媒体按量价、倍率0、失败不扣费、跨组回退、7类协议额度429、跨午夜和到期迟到结算。
- Redis中断/空缓存重启/双实例 **15项通过**。Redis不可用时请求明确拒绝且不扣费；恢复后扣费、额度重置、撤销恢复与另一实例一致。测试只停止专门创建的Redis。
- 浏览器验证45档到180档升级、同类型限制、原截止时间不变、确认金额一致，390px手机布局正常（clientWidth=scrollWidth=382）、无console error；微信等原生支付环境由组件模拟桥接覆盖，不冒充真实手机SDK。

## 本轮实际修复

1. 取得数据库锁之前取时导致跨到期/报价过期仍准入或创建、履约；改为锁后重新校验。
2. 并发修改两档销售价格造成报价快照不一致；排序加锁并在最终锁后核对有效期。
3. 未发权益退款的迟到失败/待定结果覆盖成功，以及中断REFUNDING无法恢复；终态幂等并支持恢复，退款失败仍阻止冲突购买。
4. 已撤销/过期暂停的历史父记录被新购复用导致付后冲突；保留历史身份，新购新有效记录。
5. active+未来权益、零价历史卡、销售额度/周期已经改变的历史权益被误迁成V2；采用明确订单周期证据与保守分类，新增254只矫正未发生V2操作的旧迁移合同。
6. 同日重新开通后转兼容模式，旧合同期过期消费被减到新日账本；过期抵扣限定当前合同期。
7. 部分旧权益到期后管理员重置掩盖后续扣费；保留原始账本抵扣偏移，审计有效用量和原始用量；统一锁后北京时间日期。
8. 超最大支持日期或未来才生效的合同可生成注定不能履约的报价；提前拒绝。
9. 页面在PAID/RECHARGING时过早显示订阅成功，或重叠资格请求恢复过时权限；订阅等待COMPLETED并持续轮询、资格响应按修订保护。
10. 支付跳转返回丢失待支付订阅恢复，及匿名查询将已付款待处理标为未付款；保留订单身份与准确生命周期字段。

## 覆盖和边界

- 明细：[完整矩阵](../subscription-v2-test-matrix.md)。
- 专项Go/HTTP/前端测试均没有功能性skip；全仓的既有skip详见下面列表，未把skip计作通过。
- 当前Stage只有已停用的历史验收支付渠道，没有确认可用的渠道沙箱；本次未进行真实扣款，也未启用历史实付渠道。
- 真实支付渠道、微信/支付宝真机SDK及真实文本/图片/视频供应商联调，仍属于外部验收待办。不能把240项合成HTTP结果解释为真实供应商全通过。
- 未授权生产切换；保持production_ready=false。已发布迁移253及其之前全部306个SQL字节未变，只新增254。

## 证据摘要

- backend: `deploy/.secrets/subscription-v2-comprehensive/20260921T161140Z-backend/report.json`，SHA-256 `51fc723d0bf3a51d9beee1fee69152f2970afecc9ca23102740bb779eb40840b`。
- frontend: `deploy/.secrets/subscription-v2-comprehensive/20260921T161140Z-frontend/report.json`，SHA-256 `a471f52aa9db314939c232caec3f9b980c705000fe2f9b3f5d5be27522d8566e`。
- http: `deploy/.secrets/subscription-v2-acceptance/acceptance_v2_comprehensive_final-8e220a1f/report.json`，SHA-256 `33ac5b3e2f70c6873923a782ce580fb4cf8c2bb20b252845a6d4d2770ee69254`。
- resilience: `deploy/.secrets/subscription-v2-acceptance/acceptance_v2_resilience_20260921161341-982a1d44/report.json`，SHA-256 `5984cbcad0a264b04edbb6d543f2774fbd6efebe3b3282785168240ddad571f5`。

## 全仓既有跳过清单

- `github.com/Wei-Shaw/sub2api/internal/handler/TestDingTalkOAuthStart_Disabled`
- `github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint/TestAllProfiles`
- `github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint/TestDialerAgainstCaptureServer`
- `github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint/TestDialerBasicConnection`
- `github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint/TestJA3Fingerprint`
- `github.com/Wei-Shaw/sub2api/internal/repository/TestConcurrencyCacheSuite/TestGetAccountsLoadBatch`
- `github.com/Wei-Shaw/sub2api/internal/securityaudit/TestPromptAuditConfigCASSecretRoundTripInvalidationAndTTL`
- `github.com/Wei-Shaw/sub2api/internal/securityaudit/TestPromptAuditDatabasePersistsFullPromptOnEventsOnly`
- `github.com/Wei-Shaw/sub2api/internal/securityaudit/TestPromptAuditMigrationSchemaAndLeakageGate`
- `github.com/Wei-Shaw/sub2api/internal/securityaudit/TestPromptAuditRepositoryAdmissionClaimFencingAndEventTransaction`
- `github.com/Wei-Shaw/sub2api/internal/securityaudit/TestPromptAuditRepositoryForeignKeysFiltersAndStableIdentitySnapshots`
- `github.com/Wei-Shaw/sub2api/internal/securityaudit/TestPromptAuditRepositoryHighWaterAndSafeDeletion`
- `github.com/Wei-Shaw/sub2api/internal/securityaudit/TestPromptAuditServiceConfirmationKeepsPostPreviewEventsAndConcurrentDeletesAreSafe`
- `github.com/Wei-Shaw/sub2api/internal/securityaudit/TestPromptRuntimeAggregatesConfigWorkersQueueRedisEndpointsAndGuardMetrics`
- `github.com/Wei-Shaw/sub2api/internal/securityaudit/TestRedisPayloadStoreRoundTripTTLNamespaceAndDelete`
- `github.com/Wei-Shaw/sub2api/internal/service/TestAuthPendingIdentityService_UpsertAdoptionDecision_ClearsLegacyNullSessionReference`
- `github.com/Wei-Shaw/sub2api/internal/service/TestEstimateOpenAIInputTokens_CompareWithOpenAIAPI`
- `github.com/Wei-Shaw/sub2api/internal/service/TestPluginRuntimeIntegration`
