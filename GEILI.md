# Geili fork 维护约定

Agent 和日常开发先读根目录 [AGENTS.md](AGENTS.md)，再看本文。运维上线看 `~/code/geili/subscription-lab-ops/docs/runbooks/geili-sub2api-release.md`。

## 当前基线

- GPT持久异步图生图固定隔离候选`.16` / `4a00d7a6c9780d185f7b8a1861780b8f7867adaa` / `sha256:2508a6966a5895df2f9d8670b9dc3100f1078a7a2d52645de6b1e0665efd745c`直接继承生产`.7`及`.12`，不含主干待验`.14`/`.15`。CI9任务/13门禁、同镜像382项Stage、最终三模型1K/2K/4K生成/编辑18规格通过；实际19次含原4K/high失败退款1，原失败保持。网页保持只选分辨率/比例/张数，内部1K/low、2K/medium、4K/low，R2主站GET/HEAD与192项网页测试/build通过；9张编辑视觉通过，自有Stage资源恢复，实扣USD0.78/退0.07。v25正式生产证明通过，按用户10月16日当次授权已由canonical将同digest发布生产，恢复健康9.783秒、16项生产保全通过；AUAPI账号6410已启用并绑定34/63，静态工作台20261016-gpt-auapi已切换，公网三文件SHA/接口与鉴权复核通过。仅固定该隔离镜像，不能按VERSION选含其他改动的全主干镜像。

- 当前主干等待预算候选为`0.2.16-geili.15`：统一扩展各文本思考模型族及映射别名的上游空闲/复合Key总预算，补齐Antigravity各入口与首内容期限；显式配置、客户端截止时间和媒体预算保持。继承`.14`手动容灾；本地完整回归与定向竞态、独立CI9任务/13门禁通过，未部署Stage/生产。详见RELEASES.md及`.github/geili/long-thinking-timeouts.md`。

- 独立手动容灾候选`.14`按用户澄清调整为手动欠费管理与故障恢复：取消普通请求新增的实时PG/Redis健康读取、成功重置、自动欠费推断与轮询，移除`.13`路由读取并行补偿；仅池不可用时探测同组非欠费停止账号。保留流式工具修复、原计费/订阅/配额/冷却及有界恢复规则。整合回归与CI9任务/13门禁通过，Stage继续由`.12`图片候选优先使用；未部署生产。`.11`完整性能未过证据保留，不能重标为当前候选通过。

- GPT 图生图隔离候选 `.12` / `2b3ea2b9c4ef1e283da2ef133ec481e9166f6046` 基于生产 `.7`，仅增加 AUAPI 显式兼容协议选择；CI9任务/13门禁通过，已部署Stage并回读核对，模拟验收中，未发布生产。上游路由和真实编辑验收待完成；主干 VERSION 以后续候选为准，发布本次只固定此revision/digest。下述 `.11` 为独立容灾候选，其证据不能用于批准本次 GPT 热修。

- GitHub 默认主干是 `geili/main`。现网跑的就是这条 0.2.5 线；以后只维护这一条主干。
- 一个人开发时默认直接在 `geili/main` 上改。需要隔离开的大改再拉 `codex/<主题>`，验收后合回并删除。不要留第二主干。
- 官方基线为 `v0.2.5`，当前源码同步到上游 `v0.2.16`。当前生产为 `0.2.16-geili.17` / `c058cf8d277d3e9725eb3b1329e36eefdeb79ca7` / `sha256:2fb6369c592b2bb37856239eacf8c87c32eecf4741f99fe23346f8cedbc52547`，全主干供应商/显式严格透传及流式、手动欠费容灾、路由和等待预算已按当次授权经canonical Stage同digest发布；CI9任务/13门禁、249项Stage、15项契约测试、22项生产保全与10项公网检查通过，恢复健康9.883秒。生产347迁移含新增282/283，原345保持；原observe、保护名单与恢复关闭保持。真实第一方透传验收按用户要求excluded_by_owner，不启用严格账号；支付/真实供应商/媒体未执行。旧`.16`发布前6GiB OOM证据保留，不声称已修复。回退`.16`/`4a00d7a6c`/`2508a696`保留新表及业务数据；旧独立候选失败仍保留，不重标通过。完整记录见 RELEASES.md 和运维仓 `docs/reports/supplier-strict-v17-20261016/`。
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

## 供应商角色

- 角色 `supplier` 只管理自己上传的 API Key 账号并查看其使用记录，走独立 `/api/v1/supplier/*`，管理员接口不放行。方案、权限边界和验证见 `.github/geili/supplier-role.md`。
