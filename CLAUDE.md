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

- 官方文件尽量只读。二开收拢到 `*_geili.go`、`.github/geili/`、带 `geili hook` 注释的最小挂钩。
- 前端用官方组件和样式，不要恢复已删除的 `frontend/src/geili/` 主题覆盖。
- 对外版本只改 `backend/cmd/server/VERSION`，形如 `0.2.5-geili.1`。候选 CI 必须注入这个文件，不要再写 `*.acceptance`。下次热修是 `0.2.5-geili.2`。
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
