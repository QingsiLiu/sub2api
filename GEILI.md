# Geili fork 维护约定

Agent 和日常开发先读根目录 [AGENTS.md](AGENTS.md)，再看本文。运维上线看 `~/code/geili/subscription-lab-ops/docs/runbooks/geili-sub2api-release.md`。

## 当前基线

- GPT 持久异步图生图新隔离候选`.16` / `4a00d7a6c`直接继承生产`.7`与`.12`，参考图原字节上传到AUAPI后把HTTPS链接持久化，改用JSON任务提交/查询，规避同步上游125秒超时。原`.12` Stage229项证据保持，仅作为旧候选记录，不批准`.16`；新候选本地service/handler完整回归、定向竞态及build通过，CI38733396230全部9任务/13制品门禁通过，固定镜像`sha256:2508a6966a5895df2f9d8670b9dc3100f1078a7a2d52645de6b1e0665efd745c`；已部署Stage，新候选382项模拟/业务/缓存/迁移/持久恢复及生产保全检查通过，测试资源全部恢复。真实13次提交中三模型1K/2K生成与编辑12项全部通过精确尺寸/完整解码/单次结算，六张编辑视觉通过；真实夹具已恢复，AUAPI逐笔扣USD0.36/退USD0.07。Image2/4K/high上游原图仅2048²，已全退、未重提，完整18项未通过；网页CORS与1K/2K部分发布范围待确认。仅固定该隔离revision/digest，不发布合入后的全主干镜像；主干VERSION16已包含其他待验改动，不能只按版本号挑镜像。尚未发布生产。

- 当前主干等待预算候选为`0.2.16-geili.15`：统一扩展各文本思考模型族及映射别名的上游空闲/复合Key总预算，补齐Antigravity各入口与首内容期限；显式配置、客户端截止时间和媒体预算保持。继承`.14`手动容灾；本地完整回归与定向竞态、独立CI9任务/13门禁通过，未部署Stage/生产。详见RELEASES.md及`.github/geili/long-thinking-timeouts.md`。

- 独立手动容灾候选`.14`按用户澄清调整为手动欠费管理与故障恢复：取消普通请求新增的实时PG/Redis健康读取、成功重置、自动欠费推断与轮询，移除`.13`路由读取并行补偿；仅池不可用时探测同组非欠费停止账号。保留流式工具修复、原计费/订阅/配额/冷却及有界恢复规则。整合回归与CI9任务/13门禁通过，Stage继续由`.12`图片候选优先使用；未部署生产。`.11`完整性能未过证据保留，不能重标为当前候选通过。

- GPT 图生图隔离候选 `.12` / `2b3ea2b9c4ef1e283da2ef133ec481e9166f6046` 基于生产 `.7`，仅增加 AUAPI 显式兼容协议选择；CI9任务/13门禁通过，已部署Stage并回读核对，模拟验收中，未发布生产。上游路由和真实编辑验收待完成；主干 VERSION 以后续候选为准，发布本次只固定此revision/digest。下述 `.11` 为独立容灾候选，其证据不能用于批准本次 GPT 热修。

- GitHub 默认主干是 `geili/main`。现网跑的就是这条 0.2.5 线；以后只维护这一条主干。
- 一个人开发时默认直接在 `geili/main` 上改。需要隔离开的大改再拉 `codex/<主题>`，验收后合回并删除。不要留第二主干。
- 官方基线为 `v0.2.5`，当前源码已同步到上游`v0.2.16`。对外版本以`backend/cmd/server/VERSION`为准；当前生产为`0.2.16-geili.7` / `e668f16d952666686fd1db5abde9f77cb4986077` / `sha256:b502337d537244803f7106fa2a9e580b2c822d51ec09fb1cbfdec31c9c6574da`，从`.5`隔离仅发布高级批量Key表单、共享高级设置与逐Key独立代理，Stage329项、生产58项、门禁23项通过，345份生产SQL保持。历史独立候选`0.2.16-geili.11`保留新版高级批量Key表单，修复流式工具参数与账号容灾，限定自动探测文本入口，补全原生付费和插件欠费屏障，并减少简单Anthropic请求的冗余读取，`.10`Stage功能通过但性能未过；`.11`减少目录枚举和空队列规划开销，独立CI9任务/13门禁通过，Stage136功能通过、吞吐比.981750通过，但p95差107.209ms未过，资源全部恢复，尚不能发布生产，详见 RELEASES.md，旧版结果不得重标为当前候选。加密保护保持现有observe、保护名单与恢复关闭设置，不启用protect。候选构建注入该文件，不写`*.acceptance`。
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
