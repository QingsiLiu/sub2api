#!/usr/bin/env bash
# Geili 本地验证：与 .github/workflows/geili-ci.yml 保持同一套检查。
set -euo pipefail
cd "$(dirname "$0")/../.."

echo "==> frontend: install / geili 单测 / typecheck + build"
(cd frontend && pnpm install --frozen-lockfile && pnpm test:run src/geili && pnpm build)

echo "==> frontend: 构建产物确认来自 Geili 覆盖层"
grep -rlq 'data-geili-ui' backend/internal/web/dist/assets || { echo "dist 中未找到 data-geili-ui，App.vue 覆盖未生效" >&2; exit 1; }
grep -rlq -- '--geili-primary-600' backend/internal/web/dist/assets || { echo "dist 中未找到 --geili-primary-600，style.css 覆盖未生效" >&2; exit 1; }

echo "==> backend: build / 在线更新守卫单测"
(cd backend && go build ./... && go test -tags=unit ./internal/service/ -run 'Geili|UpdateService' -count=1)

echo "==> OK"
