#!/usr/bin/env bash
# Geili 本地预发环境：在本机 Docker 里用 fork 的正式镜像起一套独立、全新的 Sub2API，
# 验证通过后才允许交给 ops 仓部署到线上。
#
# 用法：
#   .github/geili/staging.sh up [tag]      # 启动（默认 tag 取 staging.env 里的 GEILI_IMAGE_TAG）
#   .github/geili/staging.sh smoke         # 冒烟：健康检查 / 版本号 / Geili 前端标记 / 在线更新被拒
#   .github/geili/staging.sh logs          # 跟随应用日志
#   .github/geili/staging.sh ps            # 容器状态
#   .github/geili/staging.sh down          # 停止（保留数据卷）
#   .github/geili/staging.sh reset         # 停止并删除数据卷（下次 up 是全新空库）
#   .github/geili/staging.sh creds         # 打印访问地址与管理员账号
#
# 依赖：docker（compose v2）、curl、jq。
set -euo pipefail

cd "$(dirname "$0")/../.."
STAGING_DIR="deploy/geili-staging"
ENV_FILE="${STAGING_DIR}/staging.env"
PROJECT="sub2api-staging"

compose() {
  docker compose -p "${PROJECT}" \
    -f deploy/docker-compose.yml -f "${STAGING_DIR}/compose.override.yml" \
    --env-file "${ENV_FILE}" "$@"
}

die() { echo "error: $*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "缺少命令 $1"; }

random_secret() {
  LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24
}

ensure_env() {
  if [ ! -f "${ENV_FILE}" ]; then
    sed -e "s/^ADMIN_PASSWORD=__GENERATED__/ADMIN_PASSWORD=$(random_secret)/" \
        -e "s/^POSTGRES_PASSWORD=__GENERATED__/POSTGRES_PASSWORD=$(random_secret)/" \
        "${STAGING_DIR}/staging.env.example" > "${ENV_FILE}"
    echo "==> 已生成 ${ENV_FILE}（含随机密码，已被 .gitignore 忽略）"
  fi
}

env_get() { sed -n "s/^$1=//p" "${ENV_FILE}" | tail -n1; }

set_tag() {
  local tag="$1"
  if grep -q '^GEILI_IMAGE_TAG=' "${ENV_FILE}"; then
    sed -i.bak "s#^GEILI_IMAGE_TAG=.*#GEILI_IMAGE_TAG=${tag}#" "${ENV_FILE}" && rm -f "${ENV_FILE}.bak"
  else
    echo "GEILI_IMAGE_TAG=${tag}" >> "${ENV_FILE}"
  fi
}

base_url() { echo "http://$(env_get BIND_HOST):$(env_get SERVER_PORT)"; }

ensure_docker() {
  need docker
  docker info >/dev/null 2>&1 || die "Docker 守护进程未运行，请先启动 Docker Desktop"
}

cmd_up() {
  ensure_docker; ensure_env
  [ -n "${1:-}" ] && set_tag "$1"
  local tag; tag="$(env_get GEILI_IMAGE_TAG)"
  echo "==> 拉取 ghcr.io/qingsiliu/sub2api:${tag}"
  compose pull sub2api
  echo "==> 启动 ${PROJECT}"
  compose up -d
  echo "==> 等待健康检查（最多 3 分钟）"
  wait_healthy 180
  cmd_creds
}

wait_healthy() {
  local deadline=$(( $(date +%s) + $1 )) url; url="$(base_url)/health"
  until curl -fs -m 3 "${url}" >/dev/null 2>&1; do
    [ "$(date +%s)" -lt "${deadline}" ] || { compose logs --tail=40 sub2api; die "${url} 在规定时间内未就绪"; }
    sleep 3
  done
}

cmd_creds() {
  cat <<EOF

预发地址 : $(base_url)
管理员   : $(env_get ADMIN_EMAIL)
密码     : $(env_get ADMIN_PASSWORD)
镜像 tag : $(env_get GEILI_IMAGE_TAG)
EOF
}

# 冒烟测试：验证"这确实是我们的构建，并且线上最关心的两个行为正确"
cmd_smoke() {
  need curl; need jq
  [ -f "${ENV_FILE}" ] || die "尚未 up"
  local url tag fails=0; url="$(base_url)"; tag="$(env_get GEILI_IMAGE_TAG)"

  check() { # <描述> <命令...>
    local desc="$1"; shift
    if "$@" >/dev/null 2>&1; then echo "  ok    ${desc}"; else echo "  FAIL  ${desc}"; fails=$((fails+1)); fi
  }

  echo "==> 冒烟 ${url}（期望版本 ${tag}）"
  check "健康检查 /health" curl -fs -m 5 "${url}/health"

  # 前端资源必须来自 Geili 覆盖层
  local html assets
  html="$(curl -fs -m 5 "${url}/")" || die "首页不可达"
  assets="$(echo "${html}" | grep -oE '/assets/[^"]+\.(js|css)' | sort -u)"
  local found_marker=0
  for a in ${assets}; do
    if curl -fs -m 10 "${url}${a}" | grep -q -e 'data-geili-ui' -e '--geili-primary-600'; then found_marker=1; break; fi
  done
  check "入口资源含 Geili 标记（data-geili-ui / --geili-primary）" test "${found_marker}" = 1

  # 管理员登录
  local token
  token="$(curl -fs -m 10 -H 'Content-Type: application/json' \
    -d "{\"email\":\"$(env_get ADMIN_EMAIL)\",\"password\":\"$(env_get ADMIN_PASSWORD)\"}" \
    "${url}/api/v1/auth/login" | jq -r '.data.access_token // .access_token // empty')"
  check "管理员登录" test -n "${token}"
  if [ -z "${token}" ]; then echo "登录失败，跳过需要鉴权的检查"; report_smoke "${fails}"; return; fi
  local auth=(-H "Authorization: Bearer ${token}")

  local version
  version="$(curl -fs -m 10 "${auth[@]}" "${url}/api/v1/admin/system/version" | jq -r '.data.version // empty')"
  check "运行版本 = ${tag}（实际: ${version:-<空>}）" test "${version}" = "${tag}"

  check "检查更新接口仍可用 (GET check-updates → 200)" \
    curl -fs -m 20 "${auth[@]}" "${url}/api/v1/admin/system/check-updates"

  local code out; out="$(mktemp)"
  code="$(curl -s -m 20 -o "${out}" -w '%{http_code}' "${auth[@]}" -X POST -H 'Content-Type: application/json' -d '{}' \
    "${url}/api/v1/admin/system/update")"
  check "执行在线更新被拒 (POST update → 403，实际 ${code})" test "${code}" = 403
  check "拒绝原因为 SELF_UPDATE_DISABLED" grep -q SELF_UPDATE_DISABLED "${out}"
  rm -f "${out}"

  local rollbacks
  rollbacks="$(curl -fs -m 20 "${auth[@]}" "${url}/api/v1/admin/system/rollback-versions" | jq -r '(.data // []) | length')"
  check "回滚候选为空（实际 ${rollbacks:-?} 个）" test "${rollbacks:-x}" = 0

  report_smoke "${fails}"
}

report_smoke() {
  echo
  if [ "$1" = 0 ]; then echo "==> 冒烟通过"; else echo "==> 冒烟失败：$1 项"; exit 1; fi
}

case "${1:-}" in
  up)    shift; cmd_up "$@" ;;
  smoke) cmd_smoke ;;
  logs)  ensure_docker; compose logs -f sub2api ;;
  ps)    ensure_docker; compose ps ;;
  down)  ensure_docker; compose down ;;
  reset) ensure_docker; compose down -v; echo "==> 数据卷已删除" ;;
  creds) [ -f "${ENV_FILE}" ] || die "尚未 up"; cmd_creds ;;
  -h|--help|"") sed -n '2,16p' "$0" ;;
  *) die "未知子命令 $1" ;;
esac
