# 第三方代码许可

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
