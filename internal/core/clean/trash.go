// SPDX-License-Identifier: GPL-3.0-only

package clean

import (
	"context"

	"vole/internal/core/fsops"
)

// MoveToTrash 把路径移到废纸篓（实现在 core/fsops，移植自 Mole cmd/analyze/delete.go）。先走清理的路径校验。
func MoveToTrash(ctx context.Context, env *Env, p string) error {
	if err := validatePath(p, env, true); err != nil {
		return err
	}
	return fsops.MoveToTrash(ctx, fsops.TrashEnv{Home: env.Home, Now: env.Now, Finder: env.Finder}, p)
}
