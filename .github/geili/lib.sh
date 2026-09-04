#!/usr/bin/env bash
# Geili 脚本公用函数。由 sync-upstream.sh / release.sh source，不直接执行。

GEILI_UPSTREAM_REPO="${GEILI_UPSTREAM_REPO:-Wei-Shaw/sub2api}"
GEILI_BASE_BRANCH="${GEILI_BASE_BRANCH:-geili/main}"

geili_repo_root() {
  git rev-parse --show-toplevel
}

geili_die() {
  echo "error: $*" >&2
  exit 1
}

# 确保 upstream remote 存在并拉取 tag
geili_fetch_upstream() {
  if ! git remote get-url upstream >/dev/null 2>&1; then
    git remote add upstream "https://github.com/${GEILI_UPSTREAM_REPO}.git"
  fi
  git fetch --quiet --tags upstream
}

# 上游最新正式 tag（形如 vX.Y.Z，排除预发布与 -geili 后缀）
geili_latest_upstream_tag() {
  git ls-remote --tags --refs "https://github.com/${GEILI_UPSTREAM_REPO}.git" 'v*' \
    | awk -F/ '{print $NF}' \
    | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' \
    | sort -V | tail -n1
}

# 某个提交所基于的上游正式 tag（最近的、不带 -geili 后缀的 vX.Y.Z 祖先）
geili_upstream_base_of() {
  git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude '*-geili.*' "${1:-HEAD}"
}

# 列出 <from>..<to> 之间上游改动过的“需人工过目”文件
geili_touched_watch_paths() {
  local from="$1" to="$2" root
  root="$(geili_repo_root)"
  grep -vE '^\s*(#|$)' "${root}/.github/geili/watch-paths.txt" \
    | xargs git diff --name-only "${from}" "${to}" -- 2>/dev/null
}
