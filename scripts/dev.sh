#!/bin/bash
# 开发期启动 Vole：编译后直接运行。analyze、status 从 third_party/mole 编到 build/dev-bin/（没有就先编）。
set -euo pipefail
cd "$(dirname "$0")/.."
for cmd in analyze status; do
    [[ -x "build/dev-bin/$cmd" ]] || scripts/build-mole.sh "$cmd"
done
go build -o bin/vole .
VOLE_ANALYZE_BIN="$PWD/build/dev-bin/analyze" VOLE_STATUS_BIN="$PWD/build/dev-bin/status" exec ./bin/vole
