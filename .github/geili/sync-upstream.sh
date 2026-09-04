#!/usr/bin/env bash
# 本地合并上游 tag 到 geili/main（在 sync/<tag> 分支上进行）。
#
# 用法：
#   .github/geili/sync-upstream.sh            # 合并上游最新正式 tag
#   .github/geili/sync-upstream.sh v0.2.3     # 合并指定 tag
#   .github/geili/sync-upstream.sh --verify   # 合并后顺带跑本地验证
#
# 流程：fetch upstream → 从 geili/main 切出 sync/<tag> → git merge --no-ff <tag>
#   * 无冲突：打印需要人工过目的上游改动文件，提示 push + 开 PR；
#   * 有冲突：停在冲突状态，列出冲突文件和两处挂钩的恢复方法，由你手工解决。
set -euo pipefail

cd "$(dirname "$0")/../.."
# shellcheck source=lib.sh
source .github/geili/lib.sh

VERIFY=0
TAG=""
for arg in "$@"; do
  case "$arg" in
    --verify) VERIFY=1 ;;
    -h|--help) sed -n '2,14p' "$0"; exit 0 ;;
    *) TAG="$arg" ;;
  esac
done

[ -z "$(git status --porcelain)" ] || geili_die "工作区有未提交改动，请先提交或 stash"

geili_fetch_upstream
[ -n "$TAG" ] || TAG="$(geili_latest_upstream_tag)"
git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null || geili_die "tag ${TAG} 不存在（已 fetch upstream）"

git fetch --quiet origin "${GEILI_BASE_BRANCH}"
BASE_REF="origin/${GEILI_BASE_BRANCH}"
if git merge-base --is-ancestor "${TAG}" "${BASE_REF}"; then
  echo "${GEILI_BASE_BRANCH} 已经包含 ${TAG}，无需同步。"
  exit 0
fi

PREV="$(geili_upstream_base_of "${BASE_REF}")"
BRANCH="sync/${TAG}"
if git show-ref --quiet "refs/heads/${BRANCH}"; then
  geili_die "本地已存在分支 ${BRANCH}，请先处理或删除"
fi

echo "==> 上游基线 ${PREV} → ${TAG}"
echo "==> 创建 ${BRANCH}（基于 ${BASE_REF}）"
git switch -c "${BRANCH}" "${BASE_REF}"

if git merge --no-ff --no-edit -m "sync: merge upstream ${TAG} into ${GEILI_BASE_BRANCH}" "${TAG}"; then
  echo
  echo "==> 自动合并成功。以下上游改动涉及 Geili 覆盖/挂钩文件，请人工过目："
  geili_touched_watch_paths "${PREV}" "${TAG}" | sed 's/^/    /' || true
  echo "    上游对比: https://github.com/${GEILI_UPSTREAM_REPO}/compare/${PREV}...${TAG}"
  if [ "$VERIFY" = 1 ]; then
    echo
    .github/geili/verify.sh
  fi
  echo
  echo "下一步："
  echo "  git push -u origin ${BRANCH}"
  echo "  gh pr create --base ${GEILI_BASE_BRANCH} --title 'sync: upstream ${TAG}' --fill"
else
  echo
  echo "==> 合并冲突，已停在 ${BRANCH} 的冲突状态。冲突文件："
  git diff --name-only --diff-filter=U | sed 's/^/    /'
  cat <<'EOF'

提示：
  * frontend/vite.config.ts   需保留：import { geiliOverrides } ... 与 plugins 首项 geiliOverrides(__dirname)
  * frontend/postcss.config.js 需保留：tailwindcss: { config: './src/geili/tailwind.config.js' }
  * backend/internal/service/update_service.go 需保留四处 selfUpdateDisabled() 守卫
  * frontend/src/geili/** 与 backend/**/update_service_geili*.go 是我们的文件，上游不会碰
解决后：
  git add -A && git commit
  .github/geili/verify.sh
  git push -u origin <branch> && gh pr create --base geili/main --title 'sync: upstream <tag>'
放弃：
  git merge --abort && git switch geili/main && git branch -D <branch>
EOF
  exit 2
fi
