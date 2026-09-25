# 旧订阅份额管理（V3订单）

## 行为与边界

`legacy_daily` 保留旧日账本和独立到期时间，不转换成V2统一合同。有效付费份额按同类型/同档位选择部分续期、升级；`purchase`新份额从履约时起算整周期，`stack`选择同档有效付费参照份额对齐到期。续期按当前在售价格；升级/叠加剩余天数逐份ceil后汇总定点四舍五入。旧特殊/停售/不唯一价卡、活动赠礼、暂停/退款/到期份额不能被误改。新V2规则和旧订单仍保持原契约。

## API

- 用户 `GET /api/v1/payment/legacy-subscription-options`：`enabled,pools[{subscription_id,management_mode,lots[{id,status,plan_id,period_days,kind,upgrade_plan_ids,reason,expires_at,daily_limit_usd}]}]`。不可办理份额不提供plan_id；每池独立选择，不合并Key归属。
- 现有 `POST /payment/subscription-quote`增加`subscription_id,entitlement_ids,expiry_anchor_entitlement_id`；显式选择旧池才进入V3。返回`management_mode=legacy_lots,entitlement_changes`以及共享额度预览。
- 订单仍通过现有`quote_id`创建，签名快照`version=3,token_type=subscription_quote_legacy_v3`，选中份额商业状态不包括消费计数。创建订单重验商业状态/价卡/有效期，支付后按固定快照执行；过期续期/升级/参照到期转已付款待处理，不偷偷更改承诺。
- 用户/管理员订单读取包含`entitlement_changes`，已履约时为真实新增ID和开始/到期时间。不会输出签名或内部完整报价。
- 管理端 `GET/PUT /api/v1/admin/subscriptions/legacy-management`：`{"enabled":true,"user_ids":[...]}`灰度；`enabled=true,user_ids=[]`全量；`enabled=false`关闭新报价/新订单，不阻止已付款履约、退款。默认无setting即关闭。
- 管理端 `POST /api/v1/admin/subscriptions/:id/align-legacy`：`user_id,entitlement_ids,idempotency_key,reason,apply`；预览返回`snapshot,changes,expires_at`；应用必须提供`expected_snapshot`。服务端只能对齐选中有效同档付费权益的既有最晚到期，不能手填赠送日期。

## 定向免费延期

本次批准只针对已定位客户，不批量处理：实施时重新核对订阅277归属及权益288/295，4已过期不能恢复；若现场不同则停止。`.github/geili/align-legacy-customer.py`默认预览，落盘0600备份到`deploy/.secrets/legacy-alignment`；`--apply-preview`只接收该目录中的同源同订阅预览。原数据变化使CAS失效，重放同幂等键无额外延期。不要使用按整天extend接口近似到期秒数。

## 一致性与退款

用户锁→父订阅锁→目录锁。份额写入和审计/订单变更记录同事务，原subscription_id/term_id/账本不换。完整周期新购允许同一期自然到期后到账，已开新一期或有退款冲突则转人工。跨午夜/在途结算继续使用原准入快照。退款检查本单影响份额是否又被消费/修改或有在途请求，只冻结本单份额；保留其他份额，复杂退款人工审核。

没有新增迁移，使用已有setting、合同变更和权益操作审计。免费调整幂等由用户/父订阅行锁及操作记录实现，不改已有SQL文件。上线后不得直接回退到无法识别V3订单的旧镜像；应先关闭入口、完成已付款订单和退款，保留数据库。

## 验证与发布状态

本地前端全量2815测试、类型与构建通过；后端default/unit/integration通过，真实PostgreSQL竞态通过（8并发创建仅1订单、8回调只1次到账、4并发免费对齐幂等）。订阅race四包通过；候选源码本地双实例/Redis故障恢复15项通过（初次仅PG未ready导致启动失败，确认就绪后完整重跑）。Stage结果随后记录。

本次源码初始提交没有发布生产，没有调整客户权益，没有真实付款。必须候选CI→Stage同digest验收→真实支付/限额上游授权和证据齐全→canonical生产发布。功能开关默认关闭；不得因实现完成宣称客户已可办理。

## 本地真实HTTP复核

2026-09-25候选准备中运行隔离PostgreSQL、专用Redis及两个真实后端进程，模拟支付签名回调/退款与上游，旧份额交易28项通过：部分续期、精确退款、重复回调、部分升级、两种加购、开关关闭后已付款履约、现有Key调用、免费对齐预览及重放、过期已付款待处理及退款。
首轮发现实际履约明细保存触发订单updated_at自动更新，使付款完成CAS误判租约丢失；已在同一订单锁事务内保留原租约版本并新增服务回归，再完整重跑28项通过。没有把该失败算通过或发布失败候选。
