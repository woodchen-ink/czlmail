package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestRunningMCP 只使用临时配置和本地假端点, 不依赖或启动桌面程序。
func TestRunningMCP(t *testing.T) {
	for _, name := range []string{"missing", "invalid", "stale", "unauthorized", "running"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			// os.UserConfigDir 在 macOS、Windows、Linux 分别使用这些环境变量。
			t.Setenv("HOME", dir)
			t.Setenv("AppData", dir)
			t.Setenv("XDG_CONFIG_HOME", dir)
			t.Setenv("CZLMAIL_INSTANCE", "mcpbridge-test")
			path, err := InfoPath()
			if err != nil {
				t.Fatal(err)
			}

			want := Info{Token: "test-token"}
			switch name {
			case "invalid":
				if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "stale", "unauthorized", "running":
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer "+want.Token {
						t.Error("missing bearer token")
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					if name == "unauthorized" {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					// MCP 探测可能返回 405, 连接是否可用仍由 SDK 握手确认。
					w.WriteHeader(http.StatusMethodNotAllowed)
				}))
				defer server.Close()
				want.URL = server.URL
				if name == "stale" {
					server.Close()
				}
				data, err := json.Marshal(want)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			got, err := runningMCP(ctx)
			if name == "running" {
				if err != nil || got != want {
					t.Fatalf("runningMCP() = %+v, %v; want %+v, nil", got, err, want)
				}
				return
			}
			if !errors.Is(err, errMCPUnavailable) || got != (Info{}) {
				t.Fatalf("runningMCP() = %+v, %v; want empty info and unavailable error", got, err)
			}
			if ctx.Err() != nil {
				t.Fatal("bridge waited instead of failing promptly")
			}
		})
	}
}
