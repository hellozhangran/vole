#!/bin/bash
# 开发期启动 Vole：编译后直接运行，analyze 二进制用 .local/dev/bin/analyze、.local/dev/bin/status（缺了先跑 scripts/build-mole.sh analyze / status）
set -euo pipefail
cd "$(dirname "$0")/.."
go build -o bin/vole .
VOLE_ANALYZE_BIN="$PWD/.local/dev/bin/analyze" VOLE_STATUS_BIN="$PWD/.local/dev/bin/status" exec ./bin/vole
