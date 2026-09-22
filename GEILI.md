# Geili fork 维护约定

Agent 和日常开发先读根目录 [AGENTS.md](AGENTS.md)，再看本文。运维上线看 `~/code/geili/subscription-lab-ops/docs/runbooks/geili-sub2api-release.md`。

## 当前基线

- GitHub 默认主干是 `geili/main`。现网跑的就是这条 0.2.5 线；以后只维护这一条主干。
- 一个人开发时默认直接在 `geili/main` 上改。需要隔离开的大改再拉 `codex/<主题>`，验收后合回并删除。不要留第二主干。
- 官方基线为 `v0.2.5`，当前源码已同步到上游 `v0.2.7` 线。对外版本号以 `backend/cmd/server/VERSION` 为准，当前是 `0.2.7-geili.6`；上线后控制台左上角显示 `v0.2.7-geili.6`。候选构建必须注入该文件，不要再写 `*.acceptance`。下次热修递增为 `0.2.7-geili.7`。
- 每次改版本号准备发布，把该版本做了什么追加到 [RELEASES.md](RELEASES.md) 顶部。缺这条说明不算完成发版。
- 2026-09-15 用户决定恢复官方视觉；`frontend/src/geili/`、覆盖插件、Geist 字体及主题 token 已删除。
- 前端功能扩展使用现有官方组件与样式；以后统一设计须另行提出。
- 后端保留在线更新禁用守卫，避免官方自更新覆盖二开计费功能。

## 开发和验证

- 保留官方迁移文件名和校验和；二开修复添加新迁移。
- `rate_multiplier` 为余额倍率，`subscription_rate_multiplier` 为订阅倍率；零是有效值。
- 订阅归属固定到 `subscription_id`；调度目标分组、原绑定分组与额度所有者是不同概念。
- 二开适配尽量收拢到 `*_geili.go`，修改官方调用点时保留清晰的适配说明。
- `.github/geili/verify.sh` 验证前端构建、官方视觉及自更新守卫。
- `.github/geili/acceptance.py` 在本机独立 PostgreSQL/Redis 和模拟上游上执行 HTTP 验收；私密状态只放被忽略的 `deploy/.secrets/`。

## 候选构建与部署

- `.github/workflows/geili-candidate.yml` 在 `geili/main` 和 `codex/**` 上发布 `acceptance-<commit>` 镜像及固定摘要，不修改 `latest`。
- 上线固定顺序：候选 CI → 先钉测试环境 → 合入 `geili/main` → 当次授权后用同一 digest 热修生产。运维入口是 `~/code/geili/subscription-lab-ops` 的 `bin/release-geili-sub2api.sh`，细则见该仓 `docs/runbooks/geili-sub2api-release.md`。
- 测试连接正式 API 时只用专用、限额、有期限的下游 Key，绝不复制供应商主凭据或生产数据库。
- 任何生产核心切换均需另行明确授权。

## 独立结算与多分组 Key

- 新流程的套餐额度与供应商分组解耦，方案及兼容边界见 `.github/geili/subscription-decoupling.md`。
- 新 Key 必须明确 `billing_source`；复合路由使用有序 `group_ids`，不依赖全模型订阅分组。
- `.github/geili/decoupled-acceptance.py` 验证独立额度池、付款履约、兑换、跨组切换和迁移幂等性。
