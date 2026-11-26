package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 删掉的登录不归还前缀, 新登录永远拿到没用过的前缀。
func TestAddSignInNeverReusesPrefix(t *testing.T) {
	var f File
	if id := f.AddSignIn(Config{Username: "a"}); id != "s2" {
		t.Fatalf("first extra sign-in = %q, want s2", id)
	}
	if id := f.AddSignIn(Config{Username: "b"}); id != "s3" {
		t.Fatalf("second = %q, want s3", id)
	}
	if _, ok := f.RemoveSignIn("s3"); !ok {
		t.Fatal("remove s3")
	}
	if id := f.AddSignIn(Config{Username: "c"}); id != "s4" {
		t.Fatalf("after removing s3 got %q, want s4", id)
	}
	// 先分配前缀、连通后再登记; 没登记的前缀同样作废。
	reserved := f.ReserveSignIn()
	if reserved != "s5" || len(f.SignIns) != 2 {
		t.Fatalf("reserve = %q, sign-ins %d", reserved, len(f.SignIns))
	}
	f.PutSignIn(reserved, Config{Username: "d"})
	f.PutSignIn(reserved, Config{Username: "e"})
	if c, ok := f.SignIn(reserved); !ok || c.Username != "e" || len(f.SignIns) != 3 {
		t.Fatalf("put reserved sign-in: %+v %v (%d)", c, ok, len(f.SignIns))
	}
	if id := f.AddSignIn(Config{}); id != "s6" {
		t.Fatalf("after reserving s5 got %q, want s6", id)
	}
	// 序号落后于已有前缀 (手改过的文件) 时跳过它们。
	g := File{SignIns: []SignIn{{ID: "s7"}}}
	if id := g.AddSignIn(Config{}); id != "s8" {
		t.Fatalf("with stale counter got %q, want s8", id)
	}
}

func TestFindAndSetSignIn(t *testing.T) {
	f := File{Config: Config{SessionEndpoint: "https://a/jmap", AuthMethod: AuthPassword, Username: "Me@a"}}
	id := f.AddSignIn(Config{SessionEndpoint: "https://b/jmap", AuthMethod: AuthPassword, Username: "me@b"})
	if got, ok := f.Find("https://a/jmap", "me@a"); !ok || got != "" {
		t.Errorf("find primary = %q, %v", got, ok)
	}
	if got, ok := f.Find("https://b/jmap", "ME@B"); !ok || got != id {
		t.Errorf("find extra = %q, %v", got, ok)
	}
	if _, ok := f.Find("https://b/jmap", "other@b"); ok {
		t.Error("different user must not match")
	}
	if err := f.SetSignIn(id, Config{Username: "x"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := f.SignIn(id); c.Username != "x" {
		t.Errorf("set extra: %+v", c)
	}
	if err := f.SetSignIn("s9", Config{}); err == nil {
		t.Error("unknown sign-in should fail")
	}
}

// 主登录照旧写在顶层 (旧版本只认这些字段); 只改主登录时附加登录原样保留。
func TestFileKeepsPrimaryAtTopLevel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("CZLMAIL_INSTANCE", "config-test")

	if err := UpdateFile(func(f *File) error {
		f.Config = Config{ServerHost: "a", SessionEndpoint: "https://a/jmap", AuthMethod: AuthPassword, Username: "me@a"}
		f.AddSignIn(Config{ServerHost: "b", SessionEndpoint: "https://b/jmap", AuthMethod: AuthPassword, Username: "me@b"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := Save(Config{ServerHost: "a2", SessionEndpoint: "https://a/jmap", AuthMethod: AuthPassword, Username: "me@a"}); err != nil {
		t.Fatal(err)
	}

	path, _ := filePath()
	if !strings.HasPrefix(path, dir) {
		t.Fatalf("config path %q is outside the test dir", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var old Config // 旧版本的读法
	if err := json.Unmarshal(data, &old); err != nil || old.ServerHost != "a2" || old.Username != "me@a" {
		t.Fatalf("old reader sees %+v, %v", old, err)
	}
	f, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.SignIns) != 1 || f.SignIns[0].ID != "s2" || f.SignIns[0].ServerHost != "b" || f.LastSignIn != 2 {
		t.Fatalf("extra sign-ins lost: %+v", f)
	}
	// 数据目录在系统的用户配置目录下: Windows 是 %APPDATA%, Linux 是 $XDG_CONFIG_HOME, macOS 是 ~/Library/Application Support。
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, DirName())); err != nil {
		t.Fatal(err)
	}
}

func TestTokenKeyPerSignIn(t *testing.T) {
	if TokenKey("", "https://idp") != "oauth:https://idp" {
		t.Error("primary token key must stay unchanged")
	}
	if TokenKey("s2", "https://idp") == TokenKey("", "https://idp") {
		t.Error("extra sign-ins need their own token entry")
	}
}
