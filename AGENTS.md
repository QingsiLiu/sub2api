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

- 2026-10-19 当前候选 `0.2.17-geili.1`：`681c9385e56492cfd5de6a4a04ecc664f0979918` / `sha256:7c3ff7ade6a97b697bdd85fa2ce2c2562e0fb2ecd68f4da60baf416e9a24fb74`，合并官方稳定v0.2.17并保留完整Geili及蓝绿能力；CI39254335608全部9任务/13门禁通过，准备Stage，生产仍`.23`。用户已当次授权合并、验证和不停机试发，仍先同镜像完整Stage再canonical生产。固定本digest，不使用后续文档提交新生成镜像；本次状态见RELEASES.md顶部及运维仓`docs/reports/upstream-v0217-bluegreen-20261019/`。

- 2026-10-19最新状态以本条及RELEASES.md顶部为准：生产仍`.23` / `94f3b743d03e8775ab0b54efaf7249cad166a183` / `sha256:ee14e1c23e74398b6a0cd648c4cd8146febb3f9fc5c30cd83a888dd1c1a6ff4c`。蓝绿候选`.24`固定`d0107479ed89969d8415ca0c29b013ad443a255b` / `sha256:bf08ef0db00dc9037430e2c990fea611f498e5d9753938e47aa739c41be54993`，已切Stage，生产未启用。旧`.23`无排空能力，不能证明时保留且关闭自动重启、阻止下一次发版；不在新版有请求时重启旧程序。候选代码后续的文档提交不能替换这枚固定镜像，验收看运维仓`docs/reports/bluegreen-v24-20261019/`。下述旧候选段落为历史，下一未占用热修编号为`.25`。

- GPT持久异步图生图固定隔离候选`.16` / `4a00d7a6c9780d185f7b8a1861780b8f7867adaa` / `sha256:2508a6966a5895df2f9d8670b9dc3100f1078a7a2d52645de6b1e0665efd745c`直接继承生产`.7`及`.12`，不含主干待验`.14`/`.15`。CI9任务/13门禁、同镜像382项Stage、最终三模型1K/2K/4K生成/编辑18规格通过；实际19次含原4K/high失败退款1，原失败保持。网页保持只选分辨率/比例/张数，内部1K/low、2K/medium、4K/low，R2主站GET/HEAD与192项网页测试/build通过；9张编辑视觉通过，自有Stage资源恢复，实扣USD0.78/退0.07。v25正式生产证明通过，按用户10月16日当次授权已由canonical将同digest发布生产，恢复健康9.783秒、16项生产保全通过；AUAPI账号6410已启用并绑定34/63，静态工作台20261016-gpt-auapi已切换，公网三文件SHA/接口与鉴权复核通过。仅固定该隔离镜像，不能按VERSION选含其他改动的全主干镜像。

- 2026-10-15 GPT 图生图隔离候选为 `0.2.16-geili.12` / `2b3ea2b9c4ef1e283da2ef133ec481e9166f6046`，基于生产 `.7` 仅加入 AUAPI 显式 OpenAI Images 协议选择，不包含下述 `.11` 待发容灾改动。主干已收拢该候选，当前 VERSION 以后续候选为准；本固定候选CI9任务/13门禁通过，已部署Stage并回读核对，模拟验收中。上游三模型路由与真实编辑验收尚待完成，未发布生产。`.11` 独立验收状态保持，不得混用证据。

- 当前主干等待预算候选为`0.2.16-geili.15`：统一扩展各文本思考模型族及映射别名的上游空闲/复合Key总预算，补齐Antigravity各入口与首内容期限；显式配置、客户端截止时间和媒体预算保持。继承`.14`手动容灾；本地完整回归与定向竞态、独立CI9任务/13门禁通过，未部署Stage/生产。详见RELEASES.md及`.github/geili/long-thinking-timeouts.md`。

- 独立手动容灾候选为 `0.2.16-geili.14`：按用户澄清改为手动欠费标记/解除、仅故障时恢复同组停止账号；正常请求不新增实时PG/Redis健康检查或成功重置，取消自动欠费识别、启动推断、余额轮询及`.13`读取并行补偿，保留流式修复与原客户计费/订阅/调度约束。本地整合回归及CI9任务/13门禁通过，未部署Stage/生产；共享Stage继续优先`.12`图片验收。`.11`失败证据保留，不能沿用旧候选结果。

- 官方文件尽量只读。二开收拢到 `*_geili.go`、`.github/geili/`、带 `geili hook` 注释的最小挂钩。
- 前端用官方组件和样式，不要恢复已删除的 `frontend/src/geili/` 主题覆盖。
- 对外版本只改 `backend/cmd/server/VERSION`，候选CI注入该文件，不写 `*.acceptance`。当前生产是 `0.2.16-geili.17` / `c058cf8d277d3e9725eb3b1329e36eefdeb79ca7` / `sha256:2fb6369c592b2bb37856239eacf8c87c32eecf4741f99fe23346f8cedbc52547`：供应商角色、Anthropic显式严格透传及`.10`～`.15`待发流式/手动欠费容灾/路由读取/思考等待预算全主干。CI9任务/13门禁、249项Stage、15项契约测试、22项生产保全与10项公网检查通过；按用户当次授权及知悉旧`.16` OOM后继续授权经canonical同digest发布，恢复健康9.883秒。原345迁移保持，新增282/283后347份；Stage历史275不引入生产。真实第一方严格透传按用户要求excluded_by_owner，不记passed，本任务没有开启任何账号严格模式。原observe/名单/恢复关闭保持；`.16`/`4a00d7a6c`/`2508a696`保留回退，回退保留新增表和业务记录，不整库回灌。旧`.10`/`.11`性能失败与`.6`中止证据保留。旧`.16`于14:53:48触发6GiB OOM，不声称本版修复，证据在运维仓`docs/reports/supplier-strict-v17-20261016/`。当前源码已形成`.18`供应商容灾与`.20`Gemini原生文本两批候选（原`.19`发现可空参数兼容回归后撤回），`.18`与`.20`各自CI9任务/13门禁通过，`.19`撤回且未部署；`.18`业务/上游/缓存通过，但生产自然OOM中断性能，未满足发布门槛，`.20`尚未Stage（见RELEASES.md），均未上生产；不得按最新VERSION选镜像替换固定digest。独立内存修复任务已预留`.21`（生产`.17`隔离基线，尚待独立验收）；Stage优先完成OOM止血验证，任何后续候选必须保留届时生产功能。当前源码下次未占用热修编号为 `0.2.16-geili.22`。
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
