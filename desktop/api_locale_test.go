package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/i18n"
)

func TestSetUILocaleRejectsUnknown(t *testing.T) {
	a := &App{}
	if got := a.t("新邮件"); got != "新邮件" {
		t.Fatalf("before the UI reports a locale: %q", got)
	}
	a.SetUILocale("en")
	if got := a.t("收到 {0} 封新邮件", 3); got == "收到 3 封新邮件" {
		t.Fatalf("en not applied: %q", got)
	}
	a.SetUILocale("fr")
	if got := a.t("新邮件"); got != "新邮件" {
		t.Fatalf("unknown locale not reset to the source language: %q", got)
	}
}

// 后端 a.t("…")、reminderText 里 t("…") 与 catalog.T 用到的原文, 每个语言文件都要有: 缺了就会在通知、托盘里漏出中文。
func TestBackendTextsTranslated(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	sources := map[string]string{}
	for _, file := range files {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			arg := sourceArg(call)
			if arg < 0 || arg >= len(call.Args) {
				return true
			}
			lit, ok := call.Args[arg].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(lit.Value); err == nil {
				sources[s] = fset.Position(lit.Pos()).String()
			}
			return true
		})
	}
	if len(sources) < 20 {
		t.Fatalf("scan found only %d backend texts", len(sources))
	}
	locales := catalog.Locales()
	sort.Strings(locales)
	for _, locale := range locales {
		if locale == i18n.Source {
			continue
		}
		for source, at := range sources {
			if _, ok := catalog.Lookup(locale, source); !ok {
				t.Errorf("%s: %q missing from %s.json", at, source, locale)
			}
		}
	}
}

// sourceArg 返回原文是第几个参数: a.t(原文) 与 translator 参数 t(原文) 是第 0 个, catalog.T(语言, 原文) 是第 1 个;
// 不是翻译调用时为 -1。
func sourceArg(call *ast.CallExpr) int {
	switch f := call.Fun.(type) {
	case *ast.SelectorExpr:
		if f.Sel.Name == "t" {
			return 0
		}
		if x, ok := f.X.(*ast.Ident); ok && x.Name == "catalog" && f.Sel.Name == "T" {
			return 1
		}
	case *ast.Ident:
		if f.Name == "t" {
			return 0
		}
	}
	return -1
}
