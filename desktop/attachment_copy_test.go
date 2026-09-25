package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.pdf")
	os.WriteFile(src, []byte("new"), 0o644)
	dst := filepath.Join(dir, "out")
	if _, err := mcpTargetDir("relative/path"); err == nil {
		t.Fatal("relative directory accepted")
	}
	if _, err := mcpTargetDir(dst); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dst, "a.pdf"), []byte("mine"), 0o644)

	p, err := copyNoOverwrite(src, dst, "a.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "a (2).pdf" {
		t.Errorf("got %s, want a (2).pdf", filepath.Base(p))
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "a.pdf")); string(b) != "mine" {
		t.Error("existing file overwritten")
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Error("copy content wrong")
	}
}
