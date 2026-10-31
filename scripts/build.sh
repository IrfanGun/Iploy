#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FRONTEND="$ROOT/frontend/frontend"
WEB="$ROOT/backend/web/dist"
(cd "$FRONTEND" && npm ci && npm run build)
rm -rf "$WEB"
mkdir -p "$WEB"
cp -R "$FRONTEND/dist/." "$WEB/"
(cd "$ROOT/backend" && go test ./... && go build -o "$ROOT/panel" ./)
echo "Built $ROOT/panel"
