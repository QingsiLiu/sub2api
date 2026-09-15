# Geili 双分组订阅维护约束

双分组订阅是 Geili 扩展，不得把业务规则散落到上游订阅、鉴权和计费实现中。

- 自定义规则优先放在 `backend/internal/geili/subscription/`；前端沿用官方组件和样式，不恢复已移除的主题覆盖。
- 官方文件只允许保留带 `geili hook` 注释的最小适配点；同步上游后必须人工复核这些点。
- `user_subscription_groups` 是幂等关联表；额度、窗口和到期时间仍以主订阅记录为准。
- 没有 Geili 关联时必须走上游原有单分组路径。
- 每次上游同步必须运行 `.github/geili/verify.sh`、双分组订阅测试、迁移重复执行检查和 staging 冒烟。
- 发布版本使用 `<upstream-version>-geili.<n>`，并记录上游基线、冲突处理、测试结果和镜像摘要。

同步流程使用 `.github/geili/sync-upstream.sh`，不要直接在 `upstream/main` 开发；建议启用
`git config rerere.enabled true` 记录重复冲突的解决方案。
