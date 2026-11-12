// SPDX-License-Identifier: GPL-3.0-only

package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// viaValue 是不以字面量传给 T 的原文：状态、分类常量在日志里保持中文，显示时才 i18n.T(string(x))。
var viaValue = []string{
	"已清理", "将清理", "跳过", "失败", "已取消", "已移到废纸篓", "将卸载", // clean.Status、apps.Status
	"缓存和日志", "个人数据", "系统级（不处理）", // apps.Kind.String()
}

// sourceKeys 收集仓库里（不含 third_party 和测试）所有 i18n.T / Tf / TC 的字面量参数。
func sourceKeys(t *testing.T) map[string]string {
	t.Helper()
	keys := map[string]string{}
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "third_party", ".git", "build", "i18n":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "i18n" {
				return true
			}
			args := call.Args
			prefix := ""
			switch sel.Sel.Name {
			case "T", "Tf":
			case "TC":
				if len(args) < 2 {
					return true
				}
				if ctx, ok := literal(args[0]); ok {
					prefix = ctx + "\x04"
				}
				args = args[1:]
			default:
				return true
			}
			if len(args) == 0 {
				return true
			}
			// 不是字面量的（如 i18n.T(string(o.Status))）由 viaValue 覆盖
			if s, ok := literal(args[0]); ok {
				keys[prefix+s] = fset.Position(args[0].Pos()).String()
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func literal(e ast.Expr) (string, bool) {
	b, ok := e.(*ast.BasicLit)
	if !ok || b.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(b.Value)
	return s, err == nil
}

func TestKoreanCoversSource(t *testing.T) {
	keys := sourceKeys(t)
	if len(keys) == 0 {
		t.Fatal("没有找到任何 i18n.T 调用")
	}
	for k, pos := range keys {
		if _, ok := ko[k]; !ok {
			t.Errorf("%s: 缺韩语译文 %q", pos, k)
		}
	}
	for k := range ko {
		if _, ok := keys[k]; !ok && !slices.Contains(viaValue, k) {
			t.Errorf("韩语译文表里的 %q 在源码里已经没有用到", k)
		}
	}
	for _, k := range viaValue {
		if _, ok := ko[k]; !ok {
			t.Errorf("缺韩语译文 %q（状态 / 分类常量）", k)
		}
	}
}

var verb = regexp.MustCompile(`%[-+# 0]*(\[\d+\])?\d*(\.\d+)?[a-zA-Z%]`)

// 译文的格式占位符要和原文一样多、顺序一样，否则 Sprintf 会错位。
func TestKoreanFormatVerbs(t *testing.T) {
	for k, v := range ko {
		if a, b := verb.FindAllString(k, -1), verb.FindAllString(v, -1); !slices.Equal(a, b) {
			t.Errorf("%q → %q：占位符 %v 和 %v 不一致", k, v, a, b)
		}
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]Lang{"ko": KO, "ko-KR": KO, "ko-US": KO, "KO_kr": KO, "zh": ZH, "zh-Hans-CN": ZH, "zh_CN": ZH} {
		if got, ok := parse(in); !ok || got != want {
			t.Errorf("parse(%q) = %q, %v；want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "en-US", "ja-JP", "kok"} {
		if _, ok := parse(in); ok {
			t.Errorf("parse(%q) 不该认", in)
		}
	}
}

func TestDefaultsToChineseInTests(t *testing.T) {
	if t.Setenv("VOLE_LANG", ""); detect() != ZH {
		t.Fatal("测试里默认语言应该是简体中文")
	}
	if T("扫描") != "扫描" {
		t.Fatal("中文下 T 应该原样返回")
	}
}

func TestKoreanLookup(t *testing.T) {
	old := current
	t.Cleanup(func() { current = old })
	current = KO
	if got := T("扫描"); got != "스캔" {
		t.Errorf("T(扫描) = %q", got)
	}
	if got := T("没有这条原文"); got != "没有这条原文" {
		t.Errorf("没有译文时应返回原文，得到 %q", got)
	}
	if got := TC("battery", "可用 "); got != "남은 시간 " {
		t.Errorf("TC(battery) = %q", got)
	}
	if got := TC("nope", "可用 "); got != T("可用 ") {
		t.Errorf("TC 没有上下文译文时应退回 T，得到 %q", got)
	}
	if got := Tf("%d 项", 3); got != "3개 항목" {
		t.Errorf("Tf = %q", got)
	}
}
