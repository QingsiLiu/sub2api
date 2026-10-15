# 供应商角色（2026-10-16，源码开发完成，未发布）

新增用户角色 `supplier`。供应商保留普通用户的全部功能，侧边栏多一个「供应商后台」，只有账号管理和使用记录两页，范围限定为自己上传的账号。管理员在用户管理里把用户角色改为「供应商」即可开通，角色从数据库实时读取，改回普通用户立即失效。

## 权限边界

- 独立接口 `/api/v1/supplier/*`：普通用户 JWT + `SupplierOnlyGeili` 中间件 + 面板限流 + 审计。管理员 `/api/v1/admin/*` 不对供应商放行，任何管理员接口都碰不到。
- 归属表 `account_supplier_owners_geili(account_id, owner_user_id)`（迁移283），不改官方 accounts 表结构。按 ID 的操作先查归属，别人的账号一律按“不存在”返回。列表通过上下文钩子过滤，服务层再按归属表复核一遍，出现不属于本人的行直接报错，不会返回。
- 第一版只允许上传 API Key 类账号，平台见 `SupplierPlatformsGeili`（不含 Antigravity、OpenCode）。

## 供应商能做什么

- 上传：名称、API Key、可选 Base URL、并发（1～1000）、备注。复用官方 CreateAccount 校验，经 `WithSubmissionAccountCreate` 钩子在一个事务里写账号、归属和调度 outbox。落库就是 `inactive`、不可调度、无分组、无代理，等管理员审核。
- 编辑：名称、备注、并发、Key、Base URL。Key/Base URL 有变化时先暂停调度，再写入，等管理员重新确认；管理员配置的其他凭据键（模型映射等）保留。非 API Key 账号（管理员指派的 OAuth 等）凭据只读。
- 测试：复用管理员同一套 SSE 测试，但成功后不会清除异常/限流状态。
- 暂停调度、删除（软删除，历史记录保留）。不能启用调度，不能改分组、账号倍率、优先级、状态、代理。

## 出站保护

供应商账号带外部录入同一个 `external_submission_form_geili` 标记。上游策略查询从“有邀请来源”扩展为“有邀请来源或有供应商归属”，所以 Base URL 和代理都只允许连到经过校验的公网地址，每次拨号重新校验，防止指向服务器内网（SSRF）。保存时也先解析 Base URL 的 DNS，内网、回环、保留地址直接拒绝。

## 使用记录

只返回时间、账号、模型、Token、缓存、耗时、账号成本（`COALESCE(account_stats_cost,total_cost) × account_rate_multiplier`，与官方账号成本口径一致）。不返回用户、Key、IP、UA 和客户实付金额。查询强制 `account_ids = 本人全部账号`（含已删除），清空用户/Key/分组/订阅过滤；没有账号时不查库，直接返回空。日期范围默认近7天，最多93天。

## 管理员侧

- 用户管理：角色可选「供应商」，筛选和徽章同步；供应商行有「供应商账号」入口，跳到 `/admin/accounts?supplier_owner_id=<id>`。
- 账号列表：管理员展示上下文里附带 `supplier_owner{id,email}`，名称下显示供应商徽章；支持 `supplier_owner_id` 过滤。插件可读快照剥离该字段。
- 行菜单「设置供应商」→ `PUT /api/v1/admin/accounts/:id/supplier-owner {owner_user_id}`，只能指派给角色为供应商的用户，0 表示取消。
- 管理员不能把自己从管理员改成其他角色；最后一个管理员降级保护覆盖改成供应商。

## 验证

本机：Go `go build ./...`、供应商 service/middleware 单测、default/unit 全量通过（沙箱禁止 unix socket 与本机 127.0.0.1 出站导致的插件/验证码/OAuth 用例除外，与本改动无关）。前端 vue-tsc、vite build、vitest 全量通过，新增供应商账号页用例。PostgreSQL 集成测试 `TestAccountSupplierGeiliIntegration*` 已写好（归属过滤、事务回滚、软删除、指派/取消、出站策略），本机沙箱无法访问 Docker，交候选 CI 的 integration 套件执行。尚未部署 Stage/生产，未改 VERSION。
