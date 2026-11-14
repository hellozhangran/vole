// SPDX-License-Identifier: GPL-3.0-only

package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Vole", "settings.json")
	// 还没有文件：默认设置，不算错
	if s, err := Load(path); err != nil || s != (Settings{}) {
		t.Fatalf("%+v %v", s, err)
	}
	if err := Save(path, Settings{Lang: "ko"}); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || s.Lang != "ko" {
		t.Fatalf("%+v %v", s, err)
	}
	// 只留下 settings.json，没有临时文件
	if es, _ := os.ReadDir(filepath.Dir(path)); len(es) != 1 {
		t.Fatalf("目录里应只有 settings.json：%v", es)
	}
	// 改回跟随系统：字段省略
	if err := Save(path, Settings{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "{}\n" {
		t.Fatalf("%q", b)
	}
	// 内容坏了：返回默认设置和错误
	_ = os.WriteFile(path, []byte("{"), 0o600)
	if s, err := Load(path); err == nil || s != (Settings{}) {
		t.Fatalf("%+v %v", s, err)
	}
	// 不认识的字段忽略
	_ = os.WriteFile(path, []byte(`{"lang":"en","future":1}`), 0o600)
	if s, err := Load(path); err != nil || s.Lang != "en" {
		t.Fatalf("%+v %v", s, err)
	}
	if Load(""); Save("", Settings{}) == nil {
		t.Fatal("没有路径时保存应报错")
	}
}
