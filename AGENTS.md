# Geili Sub2API 源码仓 — Agent 约定

本仓是 `QingsiLiu/sub2api`（fork 自 Wei-Shaw/sub2api），日常目录 `~/code/sub2api`。
它是 **sub.geiliapi.com 的程序源码**，不是运维仓。运维、测试环境、生产回收在 `~/code/geili/subscription-lab-ops`。
不要把运维仓 checkout 到 `~/code/geili/sub2api`。

更细的二开约定看 [GEILI.md](GEILI.md)。上线步骤看运维仓 `docs/runbooks/geili-sub2api-release.md`。

## 铁律

1. **唯一长期主干是 `geili/main`。** GitHub 默认分支也是它。现网跑的就是这条 0.2.5 线。
2. **不要把 `main` 当主干。** fork 上的 `main` 已归档，那是更早的 0.1.x 实验，合进去会盖掉现网代码。
3. **不准丢掉、不准覆盖现网这条线。** 合入、同步上游、整理分支时，先确认现网 revision 仍是祖先；树必须仍是当前 Geili 0.2.5，不能用旧 `0.2.4-geili.*` 或官方 `main` 盖过来。
4. **一个人开发，默认直接在 `geili/main` 上改。** 只有要隔离开的大改才拉 `codex/<主题>`，验收后合回并当天删除。禁止把功能分支留过夜当第二主干。
5. **密钥和设计稿不入库。** 密钥只放被忽略的 `deploy/.secrets/`。`.stitch/` 是用户设计图，保留但不要提交。
6. **生产必须用户当次授权。** 本仓只出候选镜像，不在服务器编译，不点控制台左上角官方更新。上线只走运维仓 `bin/release-geili-sub2api.sh`。
7. **任务结束 = 已提交 + 已推送到 `geili/main`。** Conventional Commits，中文（`feat:` `fix:` `docs:` `ci:` `chore:`），逻辑分批。

## 项目怎么组织

- GPT 持久异步图生图新隔离候选`.16` / `4a00d7a6c`直接继承生产`.7`与`.12`，参考图原字节上传到AUAPI后把HTTPS链接持久化，改用JSON任务提交/查询，规避同步上游125秒超时。原`.12` Stage229项证据保持，仅作为旧候选记录，不批准`.16`；新候选本地service/handler完整回归、定向竞态及build通过，CI38733396230全部9任务/13制品门禁通过，固定镜像`sha256:2508a6966a5895df2f9d8670b9dc3100f1078a7a2d52645de6b1e0665efd745c`；新Stage/真实图片待验收。仅固定该隔离revision/digest，不发布合入后的全主干镜像；主干VERSION16已包含其他待验改动，不能只按版本号挑镜像。未部署新Stage或生产。

- 2026-10-15 GPT 图生图隔离候选为 `0.2.16-geili.12` / `2b3ea2b9c4ef1e283da2ef133ec481e9166f6046`，基于生产 `.7` 仅加入 AUAPI 显式 OpenAI Images 协议选择，不包含下述 `.11` 待发容灾改动。主干已收拢该候选，当前 VERSION 以后续候选为准；本固定候选CI9任务/13门禁通过，已部署Stage并回读核对，模拟验收中。上游三模型路由与真实编辑验收尚待完成，未发布生产。`.11` 独立验收状态保持，不得混用证据。

- 当前主干等待预算候选为`0.2.16-geili.15`：统一扩展各文本思考模型族及映射别名的上游空闲/复合Key总预算，补齐Antigravity各入口与首内容期限；显式配置、客户端截止时间和媒体预算保持。继承`.14`手动容灾；本地完整回归与定向竞态、独立CI9任务/13门禁通过，未部署Stage/生产。详见RELEASES.md及`.github/geili/long-thinking-timeouts.md`。

- 独立手动容灾候选为 `0.2.16-geili.14`：按用户澄清改为手动欠费标记/解除、仅故障时恢复同组停止账号；正常请求不新增实时PG/Redis健康检查或成功重置，取消自动欠费识别、启动推断、余额轮询及`.13`读取并行补偿，保留流式修复与原客户计费/订阅/调度约束。本地整合回归及CI9任务/13门禁通过，未部署Stage/生产；共享Stage继续优先`.12`图片验收。`.11`失败证据保留，不能沿用旧候选结果。

- 官方文件尽量只读。二开收拢到 `*_geili.go`、`.github/geili/`、带 `geili hook` 注释的最小挂钩。
- 前端用官方组件和样式，不要恢复已删除的 `frontend/src/geili/` 主题覆盖。
- 对外版本只改 `backend/cmd/server/VERSION`，形如 `0.2.7-geili.8`。候选 CI 必须注入这个文件，不要再写 `*.acceptance`。本任务前一性能候选版本是 `0.2.16-geili.11`（继承流式修复与账号容灾，精确模型声明跳过完整目录枚举，结算领取复用执行计划；本地unit/竞态/真实PG及CI9任务/13门禁通过，Stage136项功能通过、4000计时请求全成功，吞吐比.981750达标但p95差107.209ms未过；资源全部恢复，尚不能发布生产）；生产是 `0.2.16-geili.7` / `e668f16d952666686fd1db5abde9f77cb4986077` / `sha256:b502337d537244803f7106fa2a9e580b2c822d51ec09fb1cbfdec31c9c6574da`（基于已发布`.5`，只上线高级表单多Key共享设置、逐Key独立代理；329项Stage/58项生产复核/23项门禁测试通过）。345份生产迁移保持，Stage历史275/282不引入生产。加密会话保护保持原observe阶段，恢复关闭；`.3`/`.4`失败及`.6`功能通过但性能中止证据保留，不重标为`.8`。本次`.8`/`.9`/`.10`/`.11`未获生产授权。当前源码下次热修是 `0.2.16-geili.17`。
- **每次升版本准备发布，必须把这个版本做了什么写进 [RELEASES.md](RELEASES.md) 顶部。** 至少包括版本号、相对上一版的用户可见变化、revision、镜像 digest、部署到了测试环境还是生产。没写这条说明，不算完成发版。
- `rate_multiplier` 是余额倍率，`subscription_rate_multiplier` 是订阅倍率；零是有效值。新 Key 必须有明确 `billing_source`。
- 分组图/视频单价（`image_price_*` / `video_price_*`）也是有效定价，余额 Key 不能只认 token 价卡。
- 保留官方迁移文件名和校验和；二开只加新迁移。
- 订阅方案见 `.github/geili/subscription-decoupling.md`，维护边界见 `.github/geili/SUBSCRIPTION-MAINTENANCE.md`。

## 上线（源码侧只做前半段）

1. 在 `geili/main` 推送，等 `Geili staging candidate` 产出不可变 digest。
2. 运维仓先 `bin/release-geili-sub2api.sh stage <digest>`，测试环境健康、版本号正确。
3. 确认该 revision 已在 `origin/geili/main`。
4. 用户授权后，运维仓 `CONFIRM=BUMP_PRODUCTION bin/release-geili-sub2api.sh prod`，必须和 Stage 同一 digest。

跳过 Stage、生产/Stage 各用一枚镜像、在 OVH 上 `docker build`，都不允许。
