#!/bin/bash
# 一键打包 Vole.app + dmg：编 analyze/status → ad-hoc 签名 → mygo build（拷 resources、签 app、出 dmg）→ 校验。
# 用法：scripts/package.sh            # 默认 darwin-arm64
#       ARCH=amd64 scripts/package.sh  # 预留：Intel
#       ARCH=universal scripts/package.sh  # 预留：arm64+amd64 合成一个 universal
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
MOLE_SRC="$ROOT/third_party/mole"            # Mole 源码（原样复制，见 VOLE_SOURCE.md）
STAGE="$ROOT/build/stage"           # 构建产物，/build/ 已在 .gitignore 里
ARCH="${ARCH:-arm64}"

case "$ARCH" in
    arm64)     PLATFORM=darwin/arm64;     GOARCHES=(arm64);       OUTDIR=build/darwin-arm64 ;;
    amd64)     PLATFORM=darwin/amd64;     GOARCHES=(amd64);       OUTDIR=build/darwin-amd64 ;;
    universal) PLATFORM=darwin/universal; GOARCHES=(arm64 amd64); OUTDIR=build/darwin-universal ;;
    *) echo "不支持的 ARCH=$ARCH（arm64 / amd64 / universal）" >&2; exit 1 ;;
esac

# 1. 许可证说明里的 commit 必须和实际编译的 Mole 源码一致（GPL-3：对应源码要指得准）：
#    以 third_party/mole/VOLE_SOURCE.md 记录的 commit 为准
COMMIT="$(sed -n 's/^| commit | \([0-9a-f]\{40\}\) |$/\1/p' "$MOLE_SRC/VOLE_SOURCE.md" | head -1)"
[[ -n "$COMMIT" ]] || { echo "third_party/mole/VOLE_SOURCE.md 里没有读到 Mole commit" >&2; exit 1; }
if ! grep -q "$COMMIT" THIRD_PARTY_NOTICES.md; then
    echo "THIRD_PARTY_NOTICES.md 里的 Mole commit 不是源码对应的 ${COMMIT}，先更新它" >&2
    exit 1
fi
cmp -s licenses/mole-GPL-3.0.txt "$MOLE_SRC/LICENSE" || { echo "licenses/mole-GPL-3.0.txt 与 Mole 的 LICENSE 不一致" >&2; exit 1; }

# 2. 按架构编 Mole 的 analyze、status（不改 Mole 源码），universal 用 lipo 合成；
# 3. 逐个 ad-hoc 签名（mygo 只签还没有签名的代码，Go 链接器自带的签名没有标识符，这里换成正式的 ad-hoc 签名）
rm -rf "$STAGE" && mkdir -p "$STAGE"
for cmd in analyze status; do
    for a in "${GOARCHES[@]}"; do
        GOARCH="$a" "$ROOT/scripts/build-mole.sh" "$cmd" "$MOLE_SRC" "$STAGE/$cmd-$a"
    done
    if [[ ${#GOARCHES[@]} -gt 1 ]]; then
        lipo -create "${GOARCHES[@]/#/$STAGE/$cmd-}" -output "$STAGE/$cmd"
    else
        cp "$STAGE/$cmd-${GOARCHES[0]}" "$STAGE/$cmd"
    fi
    codesign --force --sign - --identifier "dev.vole.$cmd" "$STAGE/$cmd"
done

# 4. mygo build：按 mygo.json 的 resources 拷进 Contents/Resources，ad-hoc 签 app，出 dmg
go tool mygo build -platform "$PLATFORM"

# 5. 校验
APP="$OUTDIR/Vole.app"
RES="$APP/Contents/Resources"
for f in analyze status THIRD_PARTY_NOTICES.md licenses/mole-GPL-3.0.txt; do
    [[ -e "$RES/$f" ]] || { echo "bundle 里缺 $f" >&2; exit 1; }
done
codesign --verify --deep --strict --verbose=2 "$APP"
codesign --verify --strict "$RES/analyze"
codesign --verify --strict "$RES/status"
"$RES/analyze" --help > /dev/null
"$RES/status" --help > /dev/null

echo
echo "完成：$APP"
ls "$OUTDIR"/*.dmg
