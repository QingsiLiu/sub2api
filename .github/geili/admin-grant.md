# 管理员发放订阅权益（admin grant）

后台「订阅管理 → 发放权益」。给任意用户加一份「每日额度 × 天数」的权益，绕过自助购买的档位和叠加限制。
典型用途：线下付款、补偿、大客户定制额度。

## 规则

- **叠加：** 新权益写进用户现有的订阅池，和赠礼、付费权益按日额度相加。
  例：赠礼 45/天还剩 5 天，发放 360/天 × 30 天 → 前 5 天 405/天，之后 360/天，30 天后自然到期。
- **回落：** 各份权益各自到期。当天到期的那份承担的用量会被扣出，用户不会因为额度下降被整天封停。
- **起算：** 从确认时刻起算 `days` 天。日额 0.01–10000 USD，天数 1–3650，原因必填（≤500 字）。
- **新建池：** 用户没有有效订阅时新建父订阅（group/plan 为空），由迁移 246 的触发器挂到全模型订阅分组。
- **兼容模式：** 发放后合同转为 `legacy_daily`（日额度 = 所有有效份额之和）。在管理员权益有效期间：
  - 用户不能自助续费、升级、叠加，报价返回 `SUBSCRIPTION_COMPATIBILITY_MODE`；
  - 赠礼叠加优惠不出现；
  - 已有 V2 订单的退款转人工。
  到期且池里没有其他付费份额后恢复自助购买。预览会给出这些提示。

## 会被拒绝的情况

| 错误码 | 含义 | 处理 |
|---|---|---|
| `ADMIN_GRANT_MULTIPLE_POOLS` | 用户有多个有效池 | 先人工合并/撤销多余的池 |
| `ADMIN_GRANT_POOL_SUSPENDED` | 池被暂停 | 先恢复 |
| `ADMIN_GRANT_REFUND_PENDING` | 有份额在退款中 | 等退款结束 |
| 在途订单 | 用户有待支付/履约中的订单 | 等订单结束，避免和付款履约竞争 |
| `ADMIN_GRANT_SNAPSHOT_MISMATCH` | 预览后用户权益变了 | 重新预览再确认 |
| `ADMIN_GRANT_IDEMPOTENCY_KEY_REUSED` | 同一个幂等键带了不同参数 | 关闭弹窗重新发起 |

## 流程与幂等

1. 填用户、日额、天数、原因 → **预览**（只读，零副作用）。预览给出池号、今日额度变化、每日额度时间线、份额列表、警告。
2. **确认发放**：带上预览的 `snapshot` 和 `Idempotency-Key`。
   - 键只绑定参数（用户、日额、天数、原因），存在 sessionStorage；结果不确定（超时、5xx）时重试复用同一个键。
   - 服务端按键在 `subscription_operations` 持久去重：同键同参数直接返回已发放的结果，不会发第二份。
3. 成功后列表刷新。

接口：

- `POST /api/v1/admin/subscriptions/grant`，`apply=false` 预览，`apply=true` 应用（需 `Idempotency-Key`）
- `GET /api/v1/admin/subscriptions/:id/entitlements`，权益明细
- `POST /api/v1/admin/subscriptions/:id/entitlements/:eid/terminate`，提前终止（`{reason}`，需 `Idempotency-Key`）

## 调整与提前终止

- **延期/缩短：** 用「调整订阅」，在兼容池里勾选那一份权益。
- **提前终止：** 行操作「权益明细」→ 管理员发放的有效份额旁「提前终止」，填原因。
  到期时间设为当前时刻，按当日到期规则扣出今日用量。只有 `admin_grant` 份额可以提前终止；付费份额走退款，赠礼不动。

## 审计

```sql
SELECT id, subscription_id, entitlement_id, operation, source_reference AS idempotency_key,
       actor_id, detail->>'reason' AS reason, created_at
FROM subscription_operations
WHERE operation IN ('admin_grant', 'admin_terminate')
ORDER BY id DESC;
```

份额本身：`user_subscription_entitlements.source_type = 'admin_grant'`，`source_reference` 是幂等键。
后台「权益明细」和用户侧「权益变更历史」都会显示这些记录；用户侧把这类份额标为「专属权益」。

## 顺带修复

赠礼领取进 `legacy_daily` 池时，同步把合同和合同期限的 `expires_at` 推到赠礼到期（`benefit_campaign_service.go` 的 geili hook）。
之前只更新了池的到期时间，合同看起来已过期，报价会给出 purchase，用户付款后履约失败，转人工审核。
