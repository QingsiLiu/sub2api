# 异步视频结算持久化（0.2.8-geili.8）

新增 `264_grok_video_tasks_geili.sql` 及 Geili 旁路适配，不改变已开放活动、供应商启停状态，不调用真实生成验证。

## 新任务

1. 在向供应商提交前，按当前已配置的计算器冻结用户/Key/订阅准入、供应商账号ID、模型、分辨率、按秒或按次单价、倍率与Key额度开关。
2. xAI使用既有视频计算器，不复制一套新价格表；Seedance只使用现有token计算器和供应商明确返回的 `usage.completion_tokens`。缺字段不是0，禁止用时长推测token。
3. 供应商返回任务ID后、向用户写创建响应前，先fsync安全任务快照到挂载数据目录，再保存SQL任务。没有提示词、图片/视频URL、Key原文或账号凭据。SQL暂不可用时文件保留，后台恢复后投递，不重新提交供应商任务。
4. SQL同时保留订阅media绑定；任务表没有热父行/用量日志外键。后台租约+SKIP LOCKED轮询精确账号下的已有任务。账号暂停/不可调度时保留重试，不恢复或调用该账号。
5. 首个可信成功结果与结算凭证Prepare在同一事务保存；后续结算worker负责唯一扣款。客户端重复状态/内容请求不依赖Redis已计费标记。状态冲突、单位变化、金额变化会报错，不覆盖已成功结果。
6. 确认failed/expired/cancelled产生零费用终态凭证，结束原准入；取消HTTP操作本身不假定供应商没有成功，需后续状态确认。成功0token（明确字段）或免费倍率0同样保留完整成功证据。

## 精确边界

- 目前支持既有**线性按秒、固定按次、平价输出token**。创建前使用原计算器多点验证；不支持的非线性或Seedance区间价卡明确拒绝创建，不偷偷改价。若需要复杂阶梯视频token价，必须另增完整版本化价卡快照。
- 这是“已取得供应商任务ID以后”的耐久保证。进程在供应商已接受、但响应尚未到达本进程之前崩溃，无法猜出未知上游任务ID；本实现不自动重发模糊创建，避免双任务/双成本。需要供应商支持的创建幂等或按客户请求ID查询接口才能关闭该外部不确定窗口。
- 老任务在发布前只存Redis，没有可靠新快照时不伪造恢复。兼容旧pending证据，但遗失原分辨率/价格证据时明确待核实，不再默认480p当作已验证价。新任务不走Redis财务claim。
- 生产文件目录必须在持久卷中；文件系统和SQL同时不可写时创建不能宣称成功。被硬件破坏/磁盘丢失不能声称可恢复。
- 日归属由冻结的原订阅准入决定；过期后完成不改为新日/新订阅。余额实际消费按真实结算日。无预算预留、无硬截断策略新增。

## 可观测性

`OpenAIGatewayService.GrokVideoTaskHealth` 返回SQL pending/prepared/failed/retrying、最老pending及文件积压数量/年龄。
文件扫描每次至多10000条，超过时 `ingress_scan_truncated=true`，数字是下界，不误报积压清空。
供应商pending数分钟可能正常；需要升级的是文件投递滞留30/120秒、失败重试及观测冲突。

## 本地验证

```sh
cd /Users/tedliu/code/sub2api/backend
go test -tags unit ./internal/service -run '^TestDurableVideo|TestSeedance|TestGrokVideo' -count=1
go test -tags integration ./internal/repository -run '^TestDurableVideoIntegration_' -count=1
```

覆盖SQL失败的已接受任务fsync及重启恢复、8并发状态观测只扣一次、单价冲突、陈旧租约、成功/失败/到期/取消/免费0、Seedance明确token与缺失token、固定账号轮询与暂停账号不访问。所有供应商调用为隔离mock，不消耗真实额度。
