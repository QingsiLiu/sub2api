# 财务累计统计规模修复（0.2.8-geili.8 后续候选）

## 问题与范围

管理员累计财务报表的凭证优先视图仍全量扫描历史日志。2026-09-26生产只读基准在约1,720万日志、约23.8GiB日志表上触发15秒保护超时。测试用空凭证CTE模拟首次上线，是扫描下界，不等价于真实新schema部署后的完整查询。

本修复只替换管理员无过滤累计统计，不改变价格、支付、扣款、权益或用户归属日。新代码需新的候选镜像与完整验收，不把旧Stage镜像当作已验证本修复。

## 设计

- 仅追加迁移265，旧迁移内容不动。独立财务日表保存NUMERIC金额、Token、请求数、耗时和非空计数、未知金额/不完整/待明细计数。
- 内部桶以日志created_at或凭证completed_at/settled_at的北京时间日期组织；用户accounting_date语义不变。
- 日志/凭证追加OLD与NEW身份事件，无父表外键或全局状态锁。消费者及查询在自己的RR快照解析对方日期，覆盖跨日映射和互不可见的并发提交。
- 分日回填、修复缺桶；精确确认选中的事件ID，不按最大ID清除。正常写入不等待统计作业状态锁。
- 报表在同一RR快照合并干净日桶、脏日原始数据、尚未回填尾段。先解析确切脏日，再传明确时间参数，避免不确定CTE基数引发大表扫描/JIT。
- 分区删除前同事务记录关联失效；维护不受可选官方聚合开关影响，CAS避免本实例任务重叠。
- 财务响应不经过TTL缓存，保留singleflight合并并发请求。迁移265等待热点DDL锁最多5秒。

## 已完成的局部验证

- 8项新增真实PostgreSQL/race测试：金额/NULL/加权平均、跨日提交顺序、低ID晚提交、回滚与RR、两个消费者、更新删除、较早恢复/缺桶、分区清理以及三连接锁链。
- 既有Financial+GroupRollup真实PG/race通过，14.142秒；Financial+Dashboard单元/race service3.292秒、repository4.807秒。
- 最终1,000,000条合成日志+10,000条凭证：8项PG/race通过，23.836秒。完整累计方法343ms；汇总SQL EXPLAIN ANALYZE0.228ms，尾段0.188ms；无JIT，尾段无临时文件溢写。这是本地样本，不是生产1,720万规模验收。
- 保留初版1.872秒规划开销证据，不通过延长超时或禁用JIT隐藏问题。
- 最终原始日志：`outputs/billing-reliability-0288/financial-rollup-million-race-final.log`，SHA256 `f1ac2e7b09c6a267e948426e88c182a86c96288e347ce6659967a8aa1a1dde73`。持久证据同步运维仓。

复验：`CI=true go test -race -tags=integration ./internal/repository -run 'TestFinancialRollup|TestFinancialProjection|TestGroupUsage' -count=1 -timeout=10m`；百万样本设置`GEILI_FINANCIAL_ROLLUP_PERF_ROWS=1000000`。CI已纳入新增repository/service竞态测试。

## 发布边界

新候选全量CI、Stage和生产规模性能仍待完成；首次回填未完成时如实回退原始数据，可能较慢。生产容量未落实、外部告警接收对象未明确、真实文本调用未成功，不能设置production_ready=true。已付款订单545/订阅558保留，用户明确要求不再重新支付；后续镜像的重放验证应单列证据，原回调证据仍注明50f735741。

## 1,720万数量级复验与CI修正

2026-09-26新增266，仅追加普通completed_at及completed为空时settled_at两个索引，查询改为等价的分支时间条件；265及之前322份SQL完全不变。原COALESCE表达式已ANALYZE仍估算1/3行，17,203,324日志+172,033凭证测试虽对账准确、方法1.399秒，但触发44函数JIT，按性能断言保留失败。修复后同数量级8项真实PG/race通过332.253秒，完整累计746.921ms、汇总SQL0.270ms、尾段0.201ms；无JIT、无临时溢出，对账精确。它仍是合成数据，并非生产真实库部署验收。

成功日志`outputs/billing-reliability-0288/financial-rollup-17m-split-time-race.log`，SHA256 `95e9ff5b281cd16074e52bfa6b4bfd1ff98a848a374d9824bf520668bdbbaa35`；失败日志SHA256 `cb0a931e449f2132597187e092cdadcfa449ac1597b5ea79e1a8a4dba61c6287`保持不变。所有隔离测试与探测容器已清理。

候选CI36230023143另发现两个旧分区清理mock遗漏财务失效写入预期，已补齐并增加失效写失败时禁止DROP的回滚断言；repository完整unit通过5.065秒，专项race通过2.961秒。不能把这次修复后的工作树当作原失败候选已通过，需新CI与Stage。

同一主干另已提交35ffb348f的加密推理上下文兼容修复；新候选包含该提交，需要完整后端与业务回归。原实付545/订阅558依用户指令保留。
