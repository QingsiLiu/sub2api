# 计费可靠性真实 PostgreSQL 混合压力验收

本工具只使用仓库集成测试启动的临时 Docker PostgreSQL 18.1/Redis 8.4，加载当前全部迁移；不连接 Stage/生产，不发起供应商请求，不改变活动。

2026-09-25 生产去重结算的实测分钟峰值为 315（北京时间 17:05；相邻高值 310/308）。验收至少 630 次独立请求/分钟，默认计划 660 次，给调度保留余量。128 个 worker 每批同时提交，半数请求集中到各资金类别的热点用户，其余分布到其他用户。测试报告校验实际完成速率、128 同时活跃以及完整 30 分钟，不能以配置值代替实际证据。

在源码根目录执行短烟测：

```sh
GEILI_BILLING_STRESS=1 GEILI_BILLING_STRESS_DURATION=60s \
GEILI_BILLING_STRESS_REPORT="$PWD/outputs/billing-reliability-0288/stress-smoke.json" \
  sh -c 'cd backend && go test -tags integration ./internal/repository -run "^TestBillingReliabilityMixedStressGeili$" -count=1 -v -timeout 6m'
```

完整验收（保存 stdout 时须保留原始退出状态）：

```sh
GEILI_BILLING_STRESS=1 GEILI_BILLING_STRESS_DURATION=30m \
GEILI_BILLING_STRESS_REPORT="$PWD/outputs/billing-reliability-0288/stress-30m.json" \
  sh -c 'cd backend && go test -tags integration ./internal/repository -run "^TestBillingReliabilityMixedStressGeili$" -count=1 -v -timeout 38m'
```

覆盖余额、付费 V2 订阅、付费加赠礼订阅，真实准入、结算事务、Key 配额及权益分摊；财务 ID 与日志 ID 有意不同。五分之一任务由两个独立结算 worker 从持久化任务抢占；两个独立投递 worker 通过实际租约/`SKIP LOCKED` 写入日志。每七笔追加两次幂等重放，第二次必须未再扣费。

故障注入为汇总状态行锁 5 秒和日志表 SHARE 锁 30 秒，持续有真实结算进入。日志锁超过 15 秒投递超时，要求观察到失败、保留证据，并在释放后 120 秒内投递该批积压。持续查询同一 SQL 快照的凭证/财务投影，最后逐用户核对余额差额、Key 配额、订阅日账、权益分摊、结算凭证、日志及财务查询 API。未知金额、永久缺失和重复扣费均须为 0。

报告中的 worker 仓库实例重建只验证无进程内所有权依赖，不等同于操作系统重启；队列丢弃、数据库断开、历史恢复、异步取消等另由对应故障/业务测试覆盖。本压测无跨日时间加速、无供应商真实调用，不能单独替代全量发布验收。
