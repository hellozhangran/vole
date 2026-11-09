// SPDX-License-Identifier: GPL-3.0-only

package clean

import "vole/internal/core/fsops"

// 「只读目录加写权限再删」搬到了 core/fsops，这里保留原来的名字。

func removeAllWritable(p string, extra ...string) error { return fsops.RemoveAllWritable(p, extra...) }

func makeWritable(dir string) error { return fsops.MakeWritable(dir) }
