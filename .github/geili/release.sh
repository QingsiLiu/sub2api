#!/usr/bin/env bash
# 给 geili/main 打发布 tag：v<上游基线版本>-geili.<n>
#
# 用法：
#   .github/geili/release.sh            # 只打印将要创建的 tag
#   .github/geili/release.sh --push     # 创建并推送 tag，触发 release.yml 产出 GHCR 镜像
#
# 规则：
#   * 基线 = geili/main 最近的上游正式 tag 祖先（vX.Y.Z）；
#   * n     = 该基线下已有 -geili.<n> tag 的最大值 + 1；
#   * 只允许在 geili/main 且工作区干净、与 origin 同步时执行。
set -euo pipefail

cd "$(dirname "$0")/../.."
# shellcheck source=lib.sh
source .github/geili/lib.sh

PUSH=0
[ "${1:-}" = "--push" ] && PUSH=1

CURRENT="$(git branch --show-current)"
[ "$CURRENT" = "$GEILI_BASE_BRANCH" ] || geili_die "请在 ${GEILI_BASE_BRANCH} 分支上执行（当前 ${CURRENT}）"
[ -z "$(git status --porcelain)" ] || geili_die "工作区有未提交改动"

git fetch --quiet origin "${GEILI_BASE_BRANCH}" --tags
[ "$(git rev-parse HEAD)" = "$(git rev-parse "origin/${GEILI_BASE_BRANCH}")" ] \
  || geili_die "本地 ${GEILI_BASE_BRANCH} 与 origin 不一致，请先 push/pull"

BASE="$(geili_upstream_base_of HEAD)"
BASE_VERSION="${BASE#v}"
LAST_N="$(git tag --list "v${BASE_VERSION}-geili.*" | sed -E 's/.*-geili\.([0-9]+)$/\1/' | sort -n | tail -n1)"
NEXT_N=$(( ${LAST_N:-0} + 1 ))
TAG="v${BASE_VERSION}-geili.${NEXT_N}"

echo "上游基线: ${BASE}"
echo "本次 tag : ${TAG}"
echo "镜像     : ghcr.io/$(git remote get-url origin | sed -E 's#.*github.com[:/]([^/]+)/.*#\1#' | tr '[:upper:]' '[:lower:]')/sub2api:${BASE_VERSION}-geili.${NEXT_N}"
echo
echo "自 ${BASE} 以来的 Geili 提交："
git log --oneline "${BASE}..HEAD" | sed 's/^/    /'

if [ "$PUSH" = 1 ]; then
  git tag -a "${TAG}" -m "Geili release ${TAG} (upstream ${BASE})"
  git push origin "${TAG}"
  echo
  echo "已推送 ${TAG}，release.yml 将构建镜像；进度：gh run watch --repo $(git remote get-url origin | sed -E 's#.*github.com[:/]##; s#\.git$##')"
else
  echo
  echo "（预览模式，加 --push 实际创建并推送 tag）"
fi
