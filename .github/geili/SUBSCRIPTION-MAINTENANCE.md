# Geili 双分组订阅维护约束

> 订阅 V2 的产品与迁移规则见 [subscription-v2.md](subscription-v2.md)。V2 采用统一合同与共享日额度；下文独立份额/周月窗口仅描述旧版兼容账本。

双分组订阅是 Geili 扩展，不得把业务规则散落到上游订阅、鉴权和计费实现中。

- 自定义规则优先放在 `backend/internal/geili/subscription/`；前端沿用官方组件和样式，不恢复已移除的主题覆盖。
- 官方文件只允许保留带 `geili hook` 注释的最小适配点；同步上游后必须人工复核这些点。
- `user_subscription_groups` 是幂等关联表；路由归属仍绑定原 subscription_id；额度、窗口和有效期以 user_subscription_entitlements 份额为准，主订阅仅保留兼容汇总。
- 没有 Geili 关联时必须走上游原有单分组路径。
- 每次上游同步必须运行 `.github/geili/verify.sh`、双分组订阅测试、迁移重复执行检查和 staging 冒烟。
- 发布版本使用 `<upstream-version>-geili.<n>`，并记录上游基线、冲突处理、测试结果和镜像摘要。

同步流程使用 `.github/geili/sync-upstream.sh`，不要直接在 `upstream/main` 开发。日常开发在 `geili/main`。
同步时不得用上游树覆盖现网 0.2.5 线；冲突按保留 Geili 计费/订阅行为处理。建议启用
`git config rerere.enabled true` 记录重复冲突的解决方案。

- 权益修复约定与验收见 `audits/2026-09-18-subscription-repair.md`；生产发布不能只验证最近一项UI改动，须绑定完整SHA/digest/迁移及全门禁结果。

- 所有订阅展示查询（含 `ListActiveByUserID`）必须预载额度份额；有份额时按份额自身窗口投影，不能对主订阅兼容窗口执行展示归零。顶部、列表、摘要与进度的用量、额度上限、下一次重置时间均使用权益聚合；无份额订阅才回退旧逻辑。跨日显示回归见 `audits/2026-09-19-subscription-display.md`。
