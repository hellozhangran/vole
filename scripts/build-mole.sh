#!/bin/bash
# 从 Mole 源码编译它的一个 Go 命令（analyze / status），只编译不修改源码（GPL-3 约束）。
# 用法：scripts/build-mole.sh <analyze|status> [Mole 源码目录] [输出路径]
# 默认：third_party/mole → build/dev-bin/<cmd>；交叉编译用环境变量 GOARCH（如 GOARCH=amd64）
set -euo pipefail

CMD="${1:?用法：build-mole.sh <analyze|status> [src] [out]}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="${2:-$ROOT/third_party/mole}"
OUT="${3:-$ROOT/build/dev-bin/$CMD}"

if [[ ! -d "$SRC/cmd/$CMD" ]]; then
    echo "找不到 Mole 源码：$SRC/cmd/$CMD" >&2
    exit 1
fi
SRC="$(cd "$SRC" && pwd)"

mkdir -p "$(dirname "$OUT")"
OUT="$(cd "$(dirname "$OUT")" && pwd)/$(basename "$OUT")"

cd "$SRC"
# -trimpath：不把本机路径（含用户名）编进二进制
GOTOOLCHAIN=auto CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$OUT" "./cmd/$CMD"

# 来源记录在 third_party/mole/VOLE_SOURCE.md，打包时随 GPL-3 LICENSE 一起附上
COMMIT="$(sed -n 's/^| commit | \([0-9a-f]\{40\}\) |$/\1/p' "$SRC/VOLE_SOURCE.md" 2>/dev/null | head -1)"
echo "已编译 ${CMD}：${OUT}"
echo "Mole commit：${COMMIT:-unknown}"
