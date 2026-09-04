# Geili 二开说明（fork 维护手册）

本仓（`QingsiLiu/sub2api`）是 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 的 fork，
承载 sub.geiliapi.com 的二次开发。目标：**在完全重做 UI 的同时，仍能低成本、可控地跟进上游版本。**

三条原则：

1. **上游文件视为只读镜像。** 自有代码全部放在专属目录（`frontend/src/geili/`、`*_geili.go`、`.github/geili/`、
   `.github/workflows/geili-*.yml`），通过"覆盖"而不是"修改"接入。上游文件只允许存在少量带 `geili hook` 注释的挂钩行，
   清单见下文。
2. **版本号跟随上游。** 我们的版本形如 `v0.2.0-geili.3`：`0.2.0` 是所基于的上游版本，`geili.3` 是在该基线上的第三次发布。
   后台"检查更新"照常工作（只比较 `0.2.0` 部分），有新上游版本时仍会提示；但"执行更新/回滚"在本构建中被禁用。
3. **合并由人决定。** 自动化只负责发现新版本、尝试合并、开 PR 或 issue；`geili/main` 只能由人合并。

---

## 1. 分支与版本

| 名称 | 含义 |
| --- | --- |
| `upstream/main`、上游 tag `vX.Y.Z` | 上游只读 |
| `geili/main` | **默认分支**，= 某个上游 tag + 全部 Geili 提交。`git log v0.2.0..geili/main` 即全部二开内容 |
| `sync/vX.Y.Z` | 同步分支：`geili/main` + `merge vX.Y.Z`，经 PR 合入 `geili/main` |
| `feat/*`、`ui/*` | 日常开发分支，PR 到 `geili/main` |
| tag `vX.Y.Z-geili.N` | 发布 tag，触发 `release.yml` 产出镜像 `ghcr.io/qingsiliu/sub2api:X.Y.Z-geili.N` |
| `main` | fork 自带的上游镜像分支，不再使用，仅保留 |

版本比较规则（上游 `update_service.go` 的 `compareVersions`）：去掉 `v` 前缀和第一个 `-` 之后的所有内容，按 `major.minor.patch` 比较。
因此 `0.2.0-geili.3` 与上游 `0.2.0` 视为同一版本，不会误报"有更新"；上游发 `0.2.1` 后后台才会提示。

---

## 2. 挂钩点清单（上游文件中我们改动的全部位置）

| 文件 | 改动 | 标记 |
| --- | --- | --- |
| `frontend/vite.config.ts` | import 并把 `geiliOverrides(__dirname)` 放在 `plugins` 首位 | `geili hook` |
| `frontend/postcss.config.js` | `tailwindcss: { config: './src/geili/tailwind.config.js' }` | `geili hook` |
| `backend/internal/service/update_service.go` | `PerformUpdate` / `Rollback` / `ListRollbackVersions` / `RollbackToVersion` 开头各一处 `selfUpdateDisabled()` 守卫 | `// geili:` |

前两处由 `frontend/src/geili/__tests__/overrides.spec.ts` 校验标记仍在；第三处由
`backend/internal/service/update_service_geili_test.go` 校验行为。上游合并后如果这两组测试失败，就是挂钩被冲掉了。

其余全部是**新增文件**，上游永远不会碰：

```
frontend/src/geili/                         前端二开层（见其 README.md）
backend/internal/service/update_service_geili.go       在线更新守卫
backend/internal/service/update_service_geili_test.go
.github/geili/                              同步 / 验证 / 发布脚本
.github/workflows/geili-sync-upstream.yml   每日上游同步
.github/workflows/geili-ci.yml              Geili 层 CI
GEILI.md                                    本文
```

脚本放在 `.github/geili/` 而不是 `scripts/`、本文放在仓库根而不是 `docs/`，是因为上游 `.gitignore` 忽略了 `scripts` 与 `docs/*`，
而 git 不允许在被忽略的目录里再"反忽略"子目录——改 `.gitignore` 就得多一个挂钩点。

---

## 3. 前端覆盖机制（摘要）

详见 [`frontend/src/geili/README.md`](frontend/src/geili/README.md)。

- `frontend/src/geili/overrides.ts` 是唯一的覆盖清单：`'App.vue': 'App.vue'` 表示所有对上游 `src/App.vue` 的 import
  都会在 Vite 解析阶段被换成 `src/geili/App.vue`，不改上游任何 import 语句。
- 替换文件内 `import Upstream from '@/App.vue'` 拿到的是上游原文件（wrapper 模式），可以先包一层再逐步替换。
- 配色/字体只改 `styles/tokens.css`（CSS 变量）；Tailwind 的 `primary/accent/dark` 色板已接到这些变量上。
- 自有类名 `geili-` 前缀；**禁止**宽泛选择器覆盖上游样式（2026-06 的 Nginx 注入方案因此拖垮了图表页）。

要替换一个上游组件：在 `overrides.ts` 加一行 → 在 `src/geili/` 下写实现 → 单测会自动检查两个文件都存在。

---

## 4. 后端：在线更新策略

`update_service_geili.go`：`selfUpdatePolicy` 默认 `disabled`。

- `CheckUpdate` 不动 → 后台"系统更新"页仍会显示"发现新版本 vX.Y.Z"，这就是上游更新提示的来源。
- `PerformUpdate` / `Rollback` / `RollbackToVersion` 返回 `403 SELF_UPDATE_DISABLED`；`ListRollbackVersions` 返回空。
- 原因：官方更新器固定从上游 GitHub Release 下载官方二进制，会把镜像内的 Geili 前端整个冲掉。
- 恢复官方行为（不建议）：`go build -ldflags "-X github.com/Wei-Shaw/sub2api/internal/service.selfUpdatePolicy=enabled"`。

因为容器内不再需要写入二进制，部署侧应把 compose 的 `read_only: true` 加回来（见 §7）。

---

## 5. 跟进上游版本

### 5.1 自动（每日）

`geili-sync-upstream.yml` 每天 UTC 02:17 运行，也可在 Actions 页手动触发（可指定 tag）：

| 情况 | 动作 |
| --- | --- |
| `geili/main` 已包含最新上游 tag | 无 |
| 能干净合并 | 推 `sync/vX.Y.Z`，开 PR `sync: upstream vX.Y.Z`，正文列出上游改动触及的"需人工过目"文件（`.github/geili/watch-paths.txt`）与上游 release notes |
| 有冲突 | 开 issue `sync: upstream vX.Y.Z 合并冲突`，列出冲突文件与本地处理命令；issue 未关闭前不重复创建 |

> 注意：默认用 `GITHUB_TOKEN` 推送的分支和 PR **不会触发** CI。要让 sync PR 自动跑 CI，
> 在仓库 Secrets 添加 `GEILI_SYNC_TOKEN`（fine-grained PAT，仅本仓，权限 Contents / Pull requests / Issues 读写）。
> 没有的话，PR 上手动 `gh workflow run geili-ci.yml --ref sync/vX.Y.Z` 即可。

### 5.2 手动 / 解决冲突

```bash
.github/geili/sync-upstream.sh            # 最新上游 tag；或指定 .github/geili/sync-upstream.sh v0.2.3
# 无冲突：脚本会列出需过目的文件并给出 push / 开 PR 命令；加 --verify 顺带跑本地验证
# 有冲突：停在冲突状态，按提示解决 → git add -A && git commit → .github/geili/verify.sh → push → 开 PR
```

冲突解决要点：

- `backend/cmd/server/VERSION`：上游和我们的 `release.yml` 每次发布后都会把它提交回默认分支，所以**每次**合并都会冲突。
  同步工作流与脚本在冲突仅限该文件时会自动以我方为准解决；手工遇到时任取一方即可，发布时会按 tag 重写。
- `frontend/vite.config.ts`、`frontend/postcss.config.js`：以上游为准，再把那一两行挂钩加回去。
- `backend/internal/service/update_service.go`：以上游为准，再把四处 `if s.selfUpdateDisabled() {...}` 加回去。
- 上游改了被我们覆盖的文件（`App.vue`、`style.css`……）时 git 不会报冲突，但 wrapper 可能失去意义或漏掉新功能——
  这正是 PR 正文列出"需人工过目文件"的目的，逐个看一眼。
- 上游新增前端依赖：`pnpm install` 后 `pnpm-lock.yaml` 由上游带来，不需要手改。

### 5.3 合并后

PR 合进 `geili/main` 后立即发一个版本（§6），让线上基线与上游对齐；后台"检查更新"随即恢复"已是最新"。

---

## 6. 发布

```bash
git switch geili/main && git pull
.github/geili/release.sh           # 预览：显示基线、下一个 tag、自基线以来的提交
.github/geili/release.sh --push    # 创建并推送 vX.Y.Z-geili.N，触发 release.yml
```

`release.yml` 是上游原版：tag push → 写 `backend/cmd/server/VERSION=X.Y.Z-geili.N` → 构建前端（此时 Geili 覆盖生效）→
GoReleaser 构建二进制并推 `ghcr.io/qingsiliu/sub2api:X.Y.Z-geili.N`（附带 `latest`、`X`、`X.Y` 标签，部署时**不要**用它们，只用完整 tag）
→ 把 VERSION 文件以 `chore: sync VERSION to ... [skip ci]` 提交回 `geili/main`（发布后记得 `git pull`）。

首个版本 `v0.2.0-geili.1`（2026-09-04）已发布，镜像 `ghcr.io/qingsiliu/sub2api:0.2.0-geili.1`，视觉与官方 0.2.0 一致，
区别仅为在线更新被禁用；用于验证流水线，尚未部署。

可选：仓库 Variables 设 `SIMPLE_RELEASE=true` 只构建 x86_64 GHCR 镜像（跳过 arm64、Docker Hub、二进制归档），发布更快。

---

## 7. 部署（ops 仓，非本仓）

部署脚本与 compose 在 `~/code/geili/sub2api`（`QingsiLiu/geili-sub2api`），需要的改动：

1. `deploy/docker-compose.sub2api.yml`：`image: ghcr.io/qingsiliu/sub2api:X.Y.Z-geili.N`；加回 `read_only: true` + `/tmp` tmpfs
   （在线更新已禁用，容器无需可写 rootfs）。
2. `bin/deploy-sub2api.sh`：目标主机改为圣路易斯 BizGeili；镜像校验改为校验 fork 的 GHCR 摘要。
3. `docs/OPERATIONS.md`：把 2026-07-27 事故后"禁止自定义 Sub2API 镜像"的规则改写为"只允许 `geili/main` 经 `release.yml` 产出的
   `vX.Y.Z-geili.N` 镜像"，并注明在线更新已在构建期禁用。

---

## 8. 本地验证

```bash
.github/geili/verify.sh
# = frontend: pnpm install / vitest src/geili / typecheck+build / 产物含 data-geili-ui 与 --geili-* token
# + backend : go build / go test -tags=unit ./internal/service -run 'Geili|UpdateService'
```

与 `geili-ci.yml` 完全一致。上游自带的 `backend-ci.yml` 也会在 `geili/**`、`sync/**` 分支上跑，覆盖上游自身的完整测试。

---

## 9. 历史与决策记录

- 2026-06：尝试用 Nginx `sub_filter` 注入"Ceramic Ink"主题，宽泛选择器拖垮图表页，回滚。结论：外观改造必须走编译期组件替换。
- 2026-07-27：未经批准的自定义镜像 `0.1.165-group-priority.1` 上线并误降级，随后 `read_only` 与在线更新冲突。当时的对策是"禁止自定义镜像"。
- 2026-09-04：决定正式二开，建立本 fork 流程。上述禁令改为"只允许经本文流程产出的镜像"，并在构建期关闭在线更新以杜绝
  "官方二进制覆盖自有前端"的风险。此前 Aug 22 的 `group-priority` 实验改动已放弃。
