# Geili 财务明细证据恢复

> `billing-reconcile-geili` **默认只读**。它不是重新计费工具，也不是补偿工具。
> 本轮只恢复可交叉证明的订阅结算凭证；余额/未知操作没有可靠金额与历史归属证据时，只列待核实清单。

## 不变量

- 不调用 `UsageBillingRepository.Apply`，不修改 `users`、`api_keys`、订阅日/周/月/总额度、冻结款、权益、分摊或旧扣费幂等表。
- 只新增 `usage_settlement_receipts` 的 `settled / historical_recovery / partial` 行；`command` 留空，后台结算/投递 worker 均排除这些行。
- 不向 `usage_logs` 编造账号、模型或零 token。计费金额用 PostgreSQL NUMERIC 原始十进制字符串，禁止经 float64 后再恢复。
- 保留既有最后一笔超额规则。恢复的是已结算金额，不截断为套餐上限；例如 `$46.78302384` 不能修剪成 `$45`。
- 每份清单绑定明确 `[from,cutoff)` **结算时间**、只读一致性快照、数据库名称、所有候选及未解项、金额来源、未知字段、摘要和 SHA256。报表与此前按准入日统计的事故数量可能不同，不能混淆。
- `accounting_date` 优先取准入合约的 `usage_date`，没有合约时取北京时间准入日。无法证明的请求完成时间保持 `NULL`，不能冒充结算时间。

## 证据与限制

可自动恢复的订阅行必须同时满足：

1. `subscription_requests.status='settled'`，财务ID/Key组合仅对应一笔订阅结算；有精确金额与结算时间。
2. 热/冷 `usage_billing_dedup` 的指纹一致、唯一存在。
3. 分摊金额之和与结算金额**精确相等**；每个分摊权益属于同一订阅，且其权益ID与订阅ID在当时的 admission lots 快照中存在。
4. 订阅账本用户与合约用户一致；准入合约 term 属于该订阅且归属日期等于北京时间准入日。不读取当前 Key 的用户、模型、路由或倍率来补猜历史。
5. 财务ID和日志ID的映射属于已验证的普通网关共享ID规则：`local:`、`client:`、`generated:`、`web_search:`、`grok-video:`、`grok_audio:`、`grok_realtime:`。
6. 不存在相同稳定日志身份的完整明细。已有零金额或错误归属的日志列为冲突，不能默认当作已恢复。

**AUAPI/批量异步映射、裸上游ID不能靠字符串相等假设恢复**；本工具保守列为 unresolved，待专用任务证据证明映射。生产事故基线当时 AUAPI/批量任务为0，但工具不依赖这个假设。

仅有非订阅 dedup 指纹时，无法证明该操作是扣款、冻结、释放或免费计数，也不知道金额与当时所有者。它们在清单中显示 `amount_usd: null`、`user_id: null` 和 `unattributed_billing_operation`，**不**写入任何用户账单，不显示金额0。这里的未知数是全局待核实操作数，不能误称为用户已扣费金额或全部订阅异常数。本版不接受手工填数字的外部“金额证明”；后续恢复余额金额必须另行审核原始请求/供应商等证据链。

正常零费用不会被认定漏扣：已有日志的免费调用不列为缺失；确有 settled 的零费用订阅请求只有分摊也为零且其它证据一致才可恢复。

## 安全运行

先完成新版本 Stage 验收、备份、生产当次授权部署及上线新凭证一致性观察，**再**在运维窗口审核历史清单。源码仓不保存DSN或清单中的个人账务信息，清单写入被忽略的 `deploy/.secrets/` 或服务器受控目录。

```sh
cd /Users/tedliu/code/sub2api/backend
go build -o /tmp/billing-reconcile-geili ./cmd/billing-reconcile-geili
# DSN 由安全凭据渠道注入环境，不放命令行、不写入版本库。
# 扫描使用只读数据库角色更佳。
/tmp/billing-reconcile-geili \
  -from '2026-09-24T16:00:00Z' \
  -cutoff '2026-09-25T16:00:00Z' \
  -manifest /secure/recovery-20260925.json
```

默认 `-mode scan` 只读 Repeatable Read；限时120秒/SQL，整体10分钟，可用 `-timeout` 调整整体截止时间。扫描同时覆盖订阅结算时间与其他 dedup 操作创建时间，并保留实际准入归属日。

清单文件以0600临时文件写入后原子发布，**拒绝覆盖**已有路径。输出提供 canonical JSON SHA256（不是缩进版文件的 `sha256sum`）。审批必须保存工具输出的 hash，审核候选和金额总计，特别核验用户498/订阅407、用户4077/订阅288的对应截止时间样例；不能只比较订阅最大额度。

确认精确清单后：

```sh
/tmp/billing-reconcile-geili -mode apply \
  -cutoff '2026-09-25T16:00:00Z' \
  -manifest /secure/recovery-20260925.json \
  -sha256 '<审核时保存的canonical SHA256>' \
  -batch-size 100
```

扫描可使用纯 SELECT 角色。写入阶段使用受控维护角色：证据源表 SELECT、目标 receipt INSERT/SELECT 与 sequence USAGE；PostgreSQL 的 `SELECT FOR SHARE` 还要求被锁父表的 UPDATE 权限（可限制到非资金列，或使用审核过的锁定函数）。不要误以为纯 SELECT 角色能够执行写入阶段的行锁。工具源码没有任何余额/额度/权益 UPDATE/DELETE，维护角色不得用于任意手写账务变更。执行过程中每批只锁定待恢复的父订阅（短 SHARE）以与身份迁移/删除保持一致；`lock_timeout=3s`、`statement_timeout=30s`。不会持有全局汇总锁。遇到热点锁超时可稍后**用同一份清单/hash重试**，不要循环强推或放宽为长锁。

## 并发、重试与审计

- 每批1–500个候选，独立事务提交；失败返回已提交批次进度，未提交批次回滚。
- 写入前重新读已批准的证据。金额、分摊、归属、指纹或身份改变即拒绝，不自动刷新为新清单。
- 相同清单重复执行只认可身份、精确金额、日期、来源及批准hash一致的恢复凭证，返回 `already_recovered`，永不重复扣款。
- 扫描后完整日志到达时，必须同时验证用户、Key、订阅与精确金额，返回 `detail_arrived`，不新增恢复行。
- 两个进程竞争插入时由唯一键及 Repeatable Read 冲突拒绝/回滚，重新执行同一清单可安全完成。
- 凭证保留批准hash和候选证据hash；完整清单受控保管，不保留提示词、密钥或认证头。
- `historical_recovery` 明细不会写回原用量表；首页/用量/导出经 `usage_financial_records` 展示精确金额、历史恢复/部分缺失标签，标准价、tokens、耗时保持未知。
- 出现同一身份的不同清单或 live receipt 冲突时停止审核，不覆盖成功凭证。

## 验证

```sh
cd /Users/tedliu/code/sub2api/backend
go test ./internal/repository ./cmd/billing-reconcile-geili -run 'TestUsageRecovery|TestRecovery'
go test -tags integration ./internal/repository -run '^TestUsageRecoveryIntegration_' -count=1
```

真实 PostgreSQL 验证包含：证据恢复两次不改变任何用户/Key/额度/权益/分摊/日账本/旧幂等数据；未知金额不伪造；NULL模型/token经统一查询可见；清单篡改、证据变化、补到日志与旧零费用冲突；并发重复执行。

“工具测试通过”不是“历史已恢复”，更不是“生产无问题”。实际恢复数量、金额、尚未核实操作、两位用户对账与恢复后残差须逐批记录于运维报告。补偿另列，本工具绝不执行补偿。
