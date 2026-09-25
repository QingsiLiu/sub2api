# 计费进程级故障验收

```sh
python3 .github/geili/billing-runtime-acceptance.py \
  --binary backend/bin/billing-0288-server-next \
  --report outputs/billing-reliability-0288/p17-runtime-report.json
```

只创建本轮唯一标签的 PostgreSQL、Redis 和两个真实后端进程；仅使用本地合成上游与合成支付。不会暂停/修改共用验收数据库，更不会访问生产供应商。退出时停止自身资源，保留私密证据供排查。

覆盖：
1. 锁住明细表，余额与订阅 HTTP 请求仍正常结算；实际等待一次15秒明细超时，再真实 `SIGKILL` 两副本，解锁后重启同一二进制和持久目录，120秒内恢复明细且不重复扣款。
2. 延迟 mock 在请求已发上游后停掉**本轮自有数据库**，放行最终用量；确认 fsync WAL 存在，再真实 `SIGKILL`，恢复数据库并重启，证据只结算一次。
3. 两副本128并发真实HTTP请求，最终131笔凭证、明细、精确token、余额及订阅分摊完全对账；旧usage队列限制为1并设置drop验证耐久路径不受影响。

报告绑定二进制SHA256、开始/结束源码摘要、恢复耗时及匿名化财务标识哈希。源码在执行期间变化会明确记录 `source_unchanged=false`，不能把该证据冒充最终冻结源码验收；功能结果单独报告。所有账号密码及原始日志只在忽略的 `deploy/.secrets/billing-runtime-acceptance/` 内。
