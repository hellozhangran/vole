// SPDX-License-Identifier: GPL-3.0-only

// Package settings 是 Vole 的用户设置，存在 ~/Library/Application Support/Vole/settings.json。
// 只用标准库、不 import i18n：i18n 启动时要读它定界面语言，反过来依赖会成环。
package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Settings 是用户设置。零值就是默认设置。
type Settings struct {
	// Lang 是界面语言：zh / ko / en；空 = 跟随系统。
	Lang string `json:"lang,omitempty"`
}

// DefaultPath 是设置文件的位置；取不到家目录时为空。
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "Vole", "settings.json")
}

// Load 读设置。文件不存在时返回默认设置、不算错误；内容坏了返回默认设置和错误。
func Load(path string) (Settings, error) {
	var s Settings
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// Save 写设置：先写同目录下的临时文件再改名，写到一半退出也不会留下坏文件。
func Save(path string, s Settings) error {
	if path == "" {
		return errors.New("settings: no path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // 改名成功后它已经不在了，删除只是兜底
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
