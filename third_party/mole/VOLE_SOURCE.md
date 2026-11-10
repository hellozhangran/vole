# Mole 源码（未修改）

Vole 打包时从这里编译 Mole 的 `analyze`（磁盘分析）和 `status`（系统状态）两个命令行程序，作为独立进程随 Vole.app 一起分发。

| 项 | 值 |
|---|---|
| 上游 | https://github.com/tw93/Mole |
| 版本 | 1.69.0 |
| commit | 6d1af75f5066b3f0fc1e8951235cda5e0841df78 |
| 许可证 | GPL-3.0（见本目录 `LICENSE`） |
| 复制日期 | 2026-11-10 |

## 复制了哪些

- `cmd/analyze/`
- `cmd/status/`
- `internal/units/`（上面两个命令唯一引用的 Mole 内部包）
- `go.mod`、`go.sum`、`LICENSE`

以上文件和上游该 commit 逐字节相同，**未做任何修改**（包括 `_test.go`）。上游仓库的其他部分（shell 脚本、其他语言的 README 等）没有复制。Mole 依赖的第三方 Go 模块没有 vendor，构建时照常从 Go 模块代理下载，版本由本目录的 `go.mod` / `go.sum` 锁定。

## 构建

本目录是独立的 Go 模块（`github.com/tw93/mole`），不属于 Vole 的主模块。用 `scripts/build-mole.sh analyze|status` 编译，`scripts/package.sh` 打包时会自动调用。

## 更新

换上游版本时：整体替换上面列出的目录和文件，更新本文件的版本、commit 和日期，并同步 `THIRD_PARTY_NOTICES.md`；`scripts/package.sh` 会检查两处的 commit 一致。
