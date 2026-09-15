#!/usr/bin/env bash
# Geili 本地验证：与 .github/workflows/geili-ci.yml 保持同一套检查。
set -euo pipefail
cd "$(dirname "$0")/../.."

echo "==> frontend: install / geili 单测 / typecheck + build"
(cd frontend && pnpm install --frozen-lockfile && pnpm test:run src/views/user/__tests__/KeysView.spec.ts src/i18n/__tests__/localeKeyCompleteness.spec.ts && pnpm build)

echo "==> frontend: verify upstream visual assets"
if grep -rlq -e 'data-geili-ui' -e '\-\-geili-primary-600' backend/internal/web/dist/assets; then
  echo "Removed visual overrides were bundled" >&2
  exit 1
fi

echo "==> backend: build / 在线更新守卫单测"
(cd backend && go build ./... && go test -tags=unit ./internal/service/ -run 'Geili|UpdateService' -count=1)

echo "==> OK"
