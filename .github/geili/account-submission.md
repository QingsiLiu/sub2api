# 外部官方 API Key 录入

后台账号页的「邀请录入」预设平台、账号名、分组、代理、并发、优先级和账号倍率。Claude/OpenAI 仅使用两家官方地址，未选分组时保持未分组；零倍率有效。邀请默认 24 小时有效，只在生成时显示完整链接；关闭后无法恢复，丢失时撤销再生成。

对方打开 `/submit-key#token=…`，免登录输入 Key。账号从落库起就是 `inactive` 且 `schedulable=false`。管理员在原账号列表按名称找到账号，检查配置、主动测试，再启用状态和可调度开关。录入本身不联系上游，不探测 OpenAI 能力，也不验证 Key 有效性。更换 Key 时重新生成邀请并创建新账号，再停用旧账号。

## 保密与一致性

- 邀请使用 32 字节密码学随机数；数据库只保存完整令牌的 SHA256。令牌在 fragment 中，验证通过 POST 请求体进行；两个公开接口共用 Redis 每 IP 每分钟 30 次预算，Redis 故障拒绝请求。严格 JSON 解码，8KiB 请求上限，拒绝额外配置字段。
- 行锁和数据库时钟检查过期/撤销；账号、分组绑定、邀请消费、scheduler outbox 在一个 PostgreSQL 事务内提交。重复或丢失响应后的重试返回已提交状态，不替换 Key，不创建第二个账号。非法输入和事务失败不消费邀请。
- Key 沿用账号凭据存储与 DTO 脱敏；页面用密码框和独立 fetch 请求，错误仅保留状态码，成功清空 Key 与 fragment。Key 不进入 URL、持久存储或错误对象。
- 邀请表的 `account_id` 是永久来源记录，不依赖可编辑的 Extra。账号导出跳过这些账号并提示数量，复制被拒绝；来源查询异常时拒绝导出/复制。普通账号导出不变。
- 原始数据库备份包含账号凭据。存在已提交来源记录后，控制台禁止领取任何数据库备份下载 URL，包括旧备份；服务器备份任务不变。软删除账号不会清除这项保护。已经签发的存储链接不属于本次入口保护，备份存储仍由可信运维维护。
- 页面明确说明：Key 会交给服务器用于上游调用；后台不会展示或导出；服务器、数据库和备份存储权限持有人理论上仍可读取。该功能不承诺向服务器所有者隐藏密钥。

新增逻辑集中于 `account_submission_geili.go`，仅在 Wire、路由、导出、复制、备份入口加必要挂钩。迁移 `276_account_submission_invites_geili.sql` 仅新增邀请表及索引，不改旧迁移。

## 验证与发布

2026-10-10 本机隔离 PostgreSQL/Redis + 内嵌网关 + 模拟上游已验证两平台录入、管理员启用、转发与单次结算。真实 PostgreSQL race 测试覆盖每平台 12 个并发提交、撤销竞争、过期/失效引用及永久来源；sqlmock 覆盖凭据写入失败和 outbox 失败整体回滚。公开路由测试覆盖共享限流、窗口恢复和 Redis 故障拒绝。前端 API/页面/弹窗测试、完整 Vitest（365 文件、2983 用例）、构建、Go default/unit、相关 race、增量 lint 和 `.github/geili/verify.sh` 通过。桌面 OpenAI、手机 Claude 实际浏览器提交成功；后台邀请生成、零倍率与状态刷新已检查。

本机脚本 `.github/geili/account-submission-acceptance.py` 只允许带指定 label 的 `geili-key-intake-local` 独立测试库。候选 CI 额外执行新增 PostgreSQL/race 测试。Stage 使用 `.github/geili/account-submission-stage.py --ops-bin <运维仓/bin> …`，沿用正式运维身份/迁移/生产基线检查与排他租约，启动当前 digest 的第二实例，交错提交同一邀请；临时对象按快照里的精确 ID 恢复。同一镜像还需重做既有业务和双实例回归。

所有 Key、令牌、截图和运行快照仅放被忽略的私密目录。真实供应商 Key 不自动进行付费测试。Stage 证据必须绑定实际 revision、digest、脚本 SHA256、全部迁移校验和及恢复结果；旧版本的专用验收契约不能复用。未完成 Stage 或未取得具体候选的生产授权时，`production_ready=false`。回滚仅更换已记录镜像，保留新表。
