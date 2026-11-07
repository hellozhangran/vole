#!/bin/bash
# 从 Mole 源码编译它的一个 Go 命令（analyze / status），只编译不修改源码（GPL-3 约束）。
# 用法：scripts/build-mole.sh <analyze|status> [Mole 源码目录] [输出路径]
# 默认：.local/refs/mole → .local/dev/bin/<cmd>；交叉编译用环境变量 GOARCH（如 GOARCH=amd64）
set -euo pipefail

CMD="${1:?用法：build-mole.sh <analyze|status> [src] [out]}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="${2:-$ROOT/.local/refs/mole}"
OUT="${3:-$ROOT/.local/dev/bin/$CMD}"

if [[ ! -d "$SRC/cmd/$CMD" ]]; then
    echo "找不到 Mole 源码：$SRC/cmd/$CMD" >&2
    exit 1
fi
SRC="$(cd "$SRC" && pwd)"

mkdir -p "$(dirname "$OUT")"
OUT="$(cd "$(dirname "$OUT")" && pwd)/$(basename "$OUT")"

cd "$SRC"
GOTOOLCHAIN=auto CGO_ENABLED=0 go build -ldflags="-s -w" -o "$OUT" "./cmd/$CMD"

# 记录来源，打包时随 GPL-3 LICENSE 一起附上
COMMIT="$(git -C "$SRC" rev-parse HEAD 2>/dev/null || echo unknown)"
VERSION="$(sed -n 's/^VERSION="\(.*\)"$/\1/p' "$SRC/mole" | head -1)"
echo "已编译 ${CMD}：${OUT}"
echo "Mole 版本：${VERSION:-unknown}  commit：$COMMIT"
