# V2 渠道监控：严格模型白名单

## 配置语义

`platforms[].models` 直接决定该平台参与 V2 监控的模型范围，不再只是展示名单。

- 平台启用且模型在名单内：正常计入成功、失败、用量、延迟和健康评分。
- 名单外模型：不进入总览、趋势、矩阵、模型明细、用户排行、错误分类及样本。
- 空名单或关闭的平台：不参与监控；所有名单为空时显示无数据。
- 匹配采用平台与请求模型名的精确组合，去除首尾空格，不扩展通配符或别名。
- 页面筛选只能缩小范围；旧 `__other__` 筛选不能重新引入名单外数据。
- 原始日志、计费、模型调用权限、V1 主动探测和底层全量采集不变。

**兼容性变化：** 升级后已有配置直接适用上述语义；空名单不再代表全部模型。不会自动补名单，也无需数据库迁移或重建历史汇总。增删名单立即影响下一次查询的历史统计口径。

## 实现边界

新增仓储适配集中在 `channel_monitor_v2_allowlist_geili.go`，统一使用参数化、成对的平台/模型 SQL 条件。分钟表与固定时间粒度汇总表共用过滤规则，错误样本在聚合和 `LIMIT` 前过滤；复合分组样本沿用汇总表的实际平台归属。

模型目录的内部平台组合键不再作为对外模型筛选值。前端成功率读取后端已有 `success_rate`，不将忽略错误当作成功，也不将无数据展示为 100%。错误率/健康分仍遵守原有忽略分类规则。

## 验证

- PostgreSQL 集成用例：名单内 90 成功 + 10 超时，混入名单外 700 成功 + 900 失败，仍只统计 100 次，成功率 90%。
- 覆盖 1 分钟、5 分钟、1 小时、12 小时、24 小时粒度，所有监控读取入口、延迟分布、忽略分类、分组权限及配置保存回读。
- 超过 400 条高频无效模型错误样本不会挤掉名单内错误；复合分组样本正确归属。
- 覆盖空名单、停用平台、跨平台同名模型、无流量模型、旧筛选链接及修改名单后的历史查询；确认原始错误日志不被删除。
- 前端覆盖中英文说明、清空/保存/回读名单、真实成功率与无数据展示。

复验命令：

```sh
(cd backend && go test ./...)
(cd backend && go test -tags=unit ./internal/repository ./internal/service ./internal/handler -run 'ChannelMonitorV2|ApplyIgnoredErrors' -count=1)
(cd backend && CI=true go test -tags=integration ./internal/repository -run TestGeiliChannelMonitorV2AllowlistPostgres -count=1)
(cd backend && go build ./...)
(cd frontend && pnpm test:run && pnpm typecheck && pnpm build)
```

本次只交付源码，不升版本、不部署 Stage 或生产。后续发布须在 `RELEASES.md` 明确记录空名单语义变化，并走候选镜像 → Stage 验证 → 当次授权后同 digest 发布生产的既有流程。
