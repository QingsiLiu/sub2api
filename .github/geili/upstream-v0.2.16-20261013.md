# 上游 v0.2.16 同步记录（2026-10-13）

## 范围与保全

- 上游正式版 v0.2.16 于北京时间 2026-10-13 15:20 发布；目标主干 `4d9ac3e48` 仅比 tag 多 VERSION 同步。
- 从 `5726928ad` / `origin/geili/main` 同步，上一上游基线 `3a6fd1c9d`；79 个提交（含 merge）、213 个上游变更文件。
- 在 `codex/sync-v0.2.16` 临时分支处理，验证后快进回唯一主干 `geili/main` 并删除临时分支。生产 revision `dd724c5732aaa7320f0f348d5da68da89695cd56` 必须仍为祖先。
- 生产与 Stage 保持 `0.2.15-geili.8`；本任务仅合并源码、生成候选，Stage 冒烟与新镜像业务验收尚待运维仓执行。本次没有发布生产授权。

## 合并适配

15 个冲突文件逐块解决，额外合并官方 VERSION 同步；没有采用上游树替换现有 Geili 源码。

- 合并分组多语言字段，同时保留订阅开关、余额 / 订阅倍率、usage_panel 及全部既有字段。Ent 与 Wire 按本仓 schema / providers 重新生成，保留结算 worker、响应审计和其他清理服务。
- 模型广场保留 Geili 可见性、价格与币种配置，加入名称 / 描述本地化以及追加 `Vary: Accept-Language`。订阅摘要 / 进度只本地化旧分组名称，不用分组译文替换套餐或权益来源名称、额度。
- 保留持久结算的数据库权威限速检查；接入新缓存固定费用预留时，以 Key 的结算来源和订阅 ID 校验独立套餐 / 共享额度池，叠加同一订阅未结算的费用，禁止按目标供应商分组额度替代额度所有者。订阅固定费用不预留用户余额。
- 存储费用报价和实际扣费共用明确结算来源的倍率选择；零倍率有效。显式缓存 POST 与收费 PATCH 接入持久订阅准入，出站标记真实 dispatch，避免异步费用对应的准入在 handler 返回时被取消。
- 保留官方 Select 组件与外部录入的限定 API 适配，新增 Vertex API Key 方式及上游错误文案。上游表单测试按 Select 组件事件验证选值；倍率编辑允许零并拒绝负数 / 非有限值。
- 上游结构化回退测试的“writer committed”夹具用实际响应体提交模拟，适配 Geili 在语义输出之前缓冲响应头的合同，仍验证输出交付后不重复请求。
- 修正两个 Geili 测试调用的新接口参数、Seedance 冻结定价调用的长上下文参数。同步脚本更新为 `codex/` 分支与中文 Conventional Commit，并移除恢复旧主题覆盖的过期提示。

## 迁移

旧 341 份 SQL 逐文件字节与 SHA256 保全；仅新增上游原名 242 / 243 / 244 / 245 四份迁移，共 345 份。迁移账本按完整文件名区分，同编号 Geili 迁移不重命名、不覆盖。

## 验证与候选

- 前端 405 文件 / 3294 测试、类型检查与生产构建通过，变更文件 ESLint 通过。
- 本机独立 PostgreSQL 18 / Redis 8、模拟上游的订阅 V2 HTTP 验收 104 项通过；没有真实支付、供应商或媒体调用。临时测试服务与容器任务结束后清理，凭据及原始报告保留于忽略的 `deploy/.secrets/`。
- 同一份最终代码的 `go test ./...`、`CI=true go test -tags=unit ./...`、`CI=true go test -tags=integration ./...` 全量通过；default / unit / integration 的 service 包分别 160.279 / 242.209 / 163.850 秒。仓储集成测试实际执行迁移并发锁及重复执行检查。
- `.github/geili/verify.sh`、相关 service / routes / middleware race、修正后 handler race、真实 PostgreSQL 的订阅 V2 / 旧订阅管理 / 退款 race，以及 Stage 恢复保护脚本全部通过。新增固定缓存费用回归覆盖独立套餐零余额、共享额度、待结算费用、错误归属和显式订阅 / 余额 / 零倍率。
- golangci-lint 2.14.0 default / unit / integration 修改行增量均 0 issues。全仓混用 unit,integration tags 会遇到已有 tlsfingerprint 重复测试函数，按官方各独立 suite 验证，不将混用失败计为通过。
- 前端首轮两项原生 select 操作不适配、Vertex 遗留未使用变量，后端测试接口参数、结构化回退夹具与旧 Git 空锁问题均保留失败记录；修正后结果单列，不把失败或中止运行计作通过。
- 源码 revision：`3e349871f70ff90f104afca42411cd3e389750f8`，已快进并推送 `origin/geili/main`；上游主干及生产 `.8` revision 均通过祖先核验。
- 主干[候选 CI 38396063756](https://github.com/QingsiLiu/sub2api/actions/runs/38396063756)全部 9 个任务通过，候选镜像为 `ghcr.io/qingsiliu/sub2api@sha256:0e38015fa8ca5974a74ef78601d485ae8a735c9d5362899c1bda8577b10f4df3`；制品 revision / image 一致、13 项门禁 passed，345 份迁移 SHA256 与最终源码完全匹配，`production_ready=false`；临时分支的重复候选 CI 主动取消，避免两次构建同一候选标签。临时分支已删除，Stage / 生产均未部署新版本。

- 普通 CI 的全量 lint 仍报告既有 52 项（errcheck 29 / ineffassign 2 / staticcheck 14 / unused 7）；主干[普通 CI 38396064267](https://github.com/QingsiLiu/sub2api/actions/runs/38396064267)的 lint job `115291462449` 日志已核对。与前次同步记录一致，本次三种独立配置修改行增量为 0，不宣称普通 CI 全绿。

- 主干 [Geili CI 38396063663](https://github.com/QingsiLiu/sub2api/actions/runs/38396063663)与[安全扫描 38396063962](https://github.com/QingsiLiu/sub2api/actions/runs/38396063962)均已通过。

发布记录的后续提交仅更新文档，使用 `[skip ci]` 避免为同一份已验证代码重建候选镜像；部署时始终绑定上述源码 revision 与不可变 digest。Stage 冒烟和业务验收仍待执行，生产版本未改变。

## 当次验证及发布后续

用户进一步授权“验证后发送到线上”，并限定“仅模拟与非支付，验证通过后发布生产（推荐）”。`.1`经正式Stage入口部署后，标准Gemini缓存资源名`models/模型名`在Geili显式路由误报MODEL_NOT_AVAILABLE；原始失败及夹具恢复保留，未发布生产。修复只在缓存创建授权时规范model ID，保持原请求、计费归属和权限；未知/冲突model仍拒绝，12场景回归与routes/middleware race通过。

新候选`.2` / `ad9713282c471debbbce28c20c3659521465bdb4` / `sha256:7be6851fffc3718d26184746c506980b465814dd4601ce4f2deb3afcef3f23b5`已通过候选CI38405572599全部9任务/13门禁。2026-10-13正式Stage入口已部署，健康200，生产仍`.8`；当前镜像完整验收正在执行，不把`.1`的结果重标使用。

`.2`最终828项Stage、4000性能样本及69项生产复核通过，已按本次模拟/非支付条件授权经正式prod入口发布同Stage digest。备份`/root/backups/subscription-prod-image-bump-20261013T151811Z`和`/root/backups/geili-upstream-v0216-preflight-20261013T142018Z`；加密设置保持observe及恢复false。两仓当次回收配置、证据和发布记录。
