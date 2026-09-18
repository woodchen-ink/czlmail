package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestAIEndpoint(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.openai.com":              "https://api.openai.com/v1/responses",
		"https://api.openai.com/v1":           "https://api.openai.com/v1/responses",
		"https://x.com/v1/responses":          "https://x.com/v1/responses",
		"https://proxy.example.com/openai/v1": "https://proxy.example.com/openai/v1/responses",
	} {
		if got := aiEndpoint(in); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}

func TestResponseText(t *testing.T) {
	got, err := responseText([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"你好"},{"type":"output_text","text":"世界"}]}]}`))
	if err != nil || got != "你好世界" {
		t.Fatalf("%q %v", got, err)
	}
}

// 流式、非流式、错误三种响应。
func TestCallResponses(t *testing.T) {
	var gotAuth, gotPath string
	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		for _, d := range []string{"Hel", "lo"} {
			fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", d)
		}
		fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	defer stream.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"output_text":"整段"}`)
	}))
	defer plain.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"bad key"}}`)
	}))
	defer failing.Close()

	ctx := context.Background()
	var out strings.Builder
	if err := callResponses(ctx, stream.URL, "m", "k", "i", "x", func(d string) { out.WriteString(d) }); err != nil || out.String() != "Hello" {
		t.Fatalf("stream: %q %v", out.String(), err)
	}
	if gotAuth != "Bearer k" || gotPath != "/v1/responses" {
		t.Errorf("auth=%q path=%q", gotAuth, gotPath)
	}

	out.Reset()
	if err := callResponses(ctx, plain.URL, "m", "k", "i", "x", func(d string) { out.WriteString(d) }); err != nil || out.String() != "整段" {
		t.Fatalf("plain: %q %v", out.String(), err)
	}

	err := callResponses(ctx, failing.URL, "m", "k", "i", "x", func(string) {})
	if err == nil || !strings.Contains(err.Error(), "bad key") {
		t.Errorf("error not surfaced: %v", err)
	}
}

// 关闭思考: 默认带 reasoning.effort=none; 上游嫌参数不对时逐级退回, 最后不带该参数。
func TestCallResponsesThinkingFallback(t *testing.T) {
	var efforts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got struct {
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		json.NewDecoder(r.Body).Decode(&got)
		efforts = append(efforts, got.Reasoning.Effort)
		if got.Reasoning.Effort != "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"Unsupported parameter: reasoning.effort"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"output_text":"好"}`)
	}))
	defer srv.Close()

	var out strings.Builder
	if err := callResponses(context.Background(), srv.URL, "m", "k", "i", "x", func(d string) { out.WriteString(d) }); err != nil || out.String() != "好" {
		t.Fatalf("%q %v", out.String(), err)
	}
	if want := []string{"none", "minimal", ""}; !slices.Equal(efforts, want) {
		t.Fatalf("efforts %v, want %v", efforts, want)
	}

	// 记住能用的那一档, 后面的调用直接不带参数。
	efforts = nil
	out.Reset()
	if err := callResponses(context.Background(), srv.URL, "m", "k", "i", "x", func(d string) { out.WriteString(d) }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(efforts, []string{""}) {
		t.Fatalf("efforts %v, want one request without the parameter", efforts)
	}
}

// 支持关闭思考的服务端只收到一次请求, 参数是 none。
func TestCallResponsesThinkingOff(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"output_text":"ok"}`)
	}))
	defer srv.Close()

	if err := callResponses(context.Background(), srv.URL, "m", "k", "i", "x", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || !strings.Contains(bodies[0], `"reasoning":{"effort":"none"}`) {
		t.Fatalf("bodies %v", bodies)
	}
}
