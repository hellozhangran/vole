# 第三方代码许可

Vole 以 GNU General Public License v3.0 发布（全文见仓库根目录 `LICENSE`），因为「清理」功能移植了 Mole 的部分 GPL-3 源码（见下文「移植的源码」）。

## Mole（analyze、status 二进制）

Vole.app 内的 `Contents/Resources/analyze` 和 `Contents/Resources/status` 分别是从
[tw93/Mole](https://github.com/tw93/Mole) 的 `cmd/analyze`、`cmd/status` **原样编译**得到的独立程序，
**未做任何修改**。Vole 只以子进程方式调用它们（`analyze --json [路径]`、`status --watch --interval <间隔>`），
不链接、不拷贝它们的源码。

- 上游项目：tw93/Mole
- 源码地址：https://github.com/tw93/Mole
- 对应版本：1.69.0
- 对应 commit：6d1af75f5066b3f0fc1e8951235cda5e0841df78
- 对应源码：
  - https://github.com/tw93/Mole/tree/6d1af75f5066b3f0fc1e8951235cda5e0841df78/cmd/analyze
  - https://github.com/tw93/Mole/tree/6d1af75f5066b3f0fc1e8951235cda5e0841df78/cmd/status
- 构建方式：在上述 commit 的源码根目录执行
  `CGO_ENABLED=0 go build -ldflags="-s -w" -o analyze ./cmd/analyze`
  `CGO_ENABLED=0 go build -ldflags="-s -w" -o status ./cmd/status`
- 许可：GNU General Public License v3.0，全文见 `licenses/mole-GPL-3.0.txt`

依照 GPL-3，你可以从上面的源码地址获取这两个二进制的完整对应源码，并在 GPL-3 条款下复制、修改和再分发它们。

## Mole（移植的源码）

「清理」后端 `internal/core/clean` 移植或参考了 tw93/Mole 的源码（同一 commit：6d1af75f5066b3f0fc1e8951235cda5e0841df78，GPL-3.0）。
每个文件头都写明了出处和改动：

| Vole 文件 | Mole 来源 | 方式 |
|---|---|---|
| `internal/core/clean/protect_data.go` | `lib/core/app_protection_data.sh`（SYSTEM_CRITICAL_BUNDLES、DATA_PROTECTED_BUNDLES） | 机械转换为 Go 数据 |
| `internal/core/clean/protect.go` | `lib/core/file_ops.sh`（_mole_is_critical_deletion_path）、`lib/core/app_protection.sh`（should_protect_data、should_protect_path 部分）、`lib/core/base.sh`（默认与安全白名单） | 移植，改写为 Go |
| `internal/core/clean/trash.go` | `cmd/analyze/delete.go`（移到废纸篓） | 移植，去掉 bubbletea 包装 |
| `internal/core/clean/oplog.go` | `cmd/analyze/oplog.go`（日志转义与追加写） | 移植，改日志位置和格式 |
| `internal/core/clean/validate.go`、`remove.go` | `lib/core/file_ops.sh`（validate_path_for_deletion、safe_remove）、`bin/clean.sh`（safe_clean_guarded） | 参考删前校验语义，用 Go 重写 |
| `internal/core/clean/running.go`、`scan.go` | `lib/core/base.sh`（mole_pgrep_any）、`lib/core/file_ops.sh`（_mole_user_cache_scope、_mole_user_cache_owner_process_state） | 参考缓存归属判断，用 Go 重写 |
| `internal/core/clean/rules.go` | `lib/clean/user.sh`、`lib/clean/app_caches.sh`、`lib/clean/dev.sh`（每条规则的注释里写了对应行号，含 Codex 过期更新包 clean_codex_desktop_staging、Chrome DevTools MCP、编辑器过时扩展等） | 参考规则语义，用 Go 数据重写 |
| `internal/core/clean/uv.go` | `lib/clean/dev.sh`（clean_uv_cache） | 参考调用方式；`uv cache prune` 的估算和占用进程描述是 Vole 自写 |

以下是 Vole 自己写的，不含 Mole 代码：`internal/core/clean/force.go`（只读目录加写权限再删）、`env.go`、`size.go`、清理界面 `internal/features/clean`（只参照 Mole app 截图的布局，没有用 Mole app 的代码和素材）。

## Vole 自有素材

`internal/kit/art/` 下的田鼠插画和 `internal/kit/running/` 下的奔跑帧是 Vole 自己的素材，随 Vole 以 GPL-3.0 发布。
