// SPDX-License-Identifier: GPL-3.0-only

package clean

import (
	"context"
	"os"

	"vole/internal/core/fsops"
)

// 大小和身份的实现搬到了 core/fsops（卸载也要用），这里保留原来的名字。

func diskUsage(ctx context.Context, root string) (int64, error) { return fsops.DiskUsage(ctx, root) }

func identityOf(p string) (Identity, os.FileInfo, error) { return fsops.IdentityOf(p) }
