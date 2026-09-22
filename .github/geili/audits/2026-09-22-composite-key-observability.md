# 复合 Key 管理显示与路由错误记录修复

## 原因与边界

复合 Key 的有序配置存在 `group_ids`，单值 `group_id` 为 NULL。管理员用户 Key 弹窗原先只判断 `group_id/group`，因此误显示“无”。列表及单 Key 查询现在批量加载配置分组并返回可选 `groups`，保留 `group_ids` 原顺序。鉴权专用查询不增加预加载。管理员弹窗显示复合标识及配置分组，缺失分组回退显示 ID；不再使用单分组编辑入口修改复合 Key。

`explicitKeyRouting` 在 handler 运行前匹配模型与端点，失败时 handler 尚未写入 Ops 模型和请求类型。现在在路由解析前记录客户端模型、stream/request type；MODEL_NOT_AVAILABLE 标记为本地模型配置拒绝，不归因为上游错误。

日志的 `group_id` 仍代表实际选中的分组，不拿首个候选组伪造。迁移 `255_ops_error_requested_group_ids.sql` 新增可空 BIGINT[]，保存请求发生时的配置分组 ID 快照（列表/详情均返回 `requested_group_ids`）。未选中目标时展示“尚未选定目标分组”，另列候选 ID。旧记录维持 NULL，不以 Key 当前配置回填历史；历史缺失模型无法可靠恢复。

这次修复不改变模型准入、路由、计费或用户分组权限，不代表特定 MODEL_NOT_AVAILABLE 请求已经可路由。

## 本地验证

- 前端全量：311 文件、2530 测试通过；TypeScript、ESLint、build 通过。
- 后端：`go test ./...` 通过；repository/handler/routes/dto 的 unit 测试通过。
- `CI=true go test -tags=integration ./internal/repository` 通过（独立 PostgreSQL/Redis）。
- 新增覆盖：复合组顺序/DTO、单组兼容、旧 API 分组目录回退、路由早退元数据、异步队列快照隔离、无匹配目标不伪造 group_id、数据库日志列表/详情 round-trip。

候选版本 `0.2.7-geili.5`。本文只记录本地验证，不是 Stage 验收或生产批准证明；生产必须沿用规范发布入口和当次授权。
