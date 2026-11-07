package binloc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeExec(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestLocate(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "Vole.app", "Contents", "MacOS", "Vole")
	bundled := filepath.Join(dir, "Vole.app", "Contents", "Resources", "analyze")
	override := filepath.Join(dir, "dev", "analyze")
	writeExec(t, exe)
	writeExec(t, bundled)
	writeExec(t, override)

	// 环境变量优先
	if got, err := locate("analyze", AnalyzeEnv, override, exe); err != nil || got != override {
		t.Fatalf("env 覆盖：got %q, %v", got, err)
	}
	// 未设置环境变量 → bundle Resources
	got, err := locate("analyze", AnalyzeEnv, "", exe)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(bundled); got != want && got != bundled {
		t.Fatalf("bundle：got %q want %q", got, bundled)
	}
	// 环境变量指向不存在的文件 → 报错且不回落到 bundle
	_, err = locate("analyze", AnalyzeEnv, "/nope", exe)
	var nf *NotFoundError
	if !errors.As(err, &nf) || len(nf.Tried) != 1 {
		t.Fatalf("env 无效应报错：%v", err)
	}
	// 都没有 → 报错并列出已查找位置
	_, err = locate("analyze", AnalyzeEnv, "", filepath.Join(dir, "other", "bin", "x"))
	if !errors.As(err, &nf) || len(nf.Tried) != 2 {
		t.Fatalf("应列出两处查找位置：%v", err)
	}
}
