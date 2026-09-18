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
	if err := callResponses(ctx, stream.URL, "m", "k", "none", "i", "x", func(d string) { out.WriteString(d) }); err != nil || out.String() != "Hello" {
		t.Fatalf("stream: %q %v", out.String(), err)
	}
	if gotAuth != "Bearer k" || gotPath != "/v1/responses" {
		t.Errorf("auth=%q path=%q", gotAuth, gotPath)
	}

	out.Reset()
	if err := callResponses(ctx, plain.URL, "m", "k", "none", "i", "x", func(d string) { out.WriteString(d) }); err != nil || out.String() != "整段" {
		t.Fatalf("plain: %q %v", out.String(), err)
	}

	err := callResponses(ctx, failing.URL, "m", "k", "none", "i", "x", func(string) {})
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
	if err := callResponses(context.Background(), srv.URL, "m", "k", "none", "i", "x", func(d string) { out.WriteString(d) }); err != nil || out.String() != "好" {
		t.Fatalf("%q %v", out.String(), err)
	}
	if want := []string{"none", ""}; !slices.Equal(efforts, want) {
		t.Fatalf("efforts %v, want %v", efforts, want)
	}

	// 记住能用的那一档, 后面的调用直接不带参数。
	efforts = nil
	out.Reset()
	if err := callResponses(context.Background(), srv.URL, "m", "k", "none", "i", "x", func(d string) { out.WriteString(d) }); err != nil {
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

	if err := callResponses(context.Background(), srv.URL, "m", "k", "none", "i", "x", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || !strings.Contains(bodies[0], `"reasoning":{"effort":"none"}`) {
		t.Fatalf("bodies %v", bodies)
	}
}

// 翻译与写作用各自的档位; auto 才自动退回, 点了名的档位照发不换。
func TestReasoningForAndChain(t *testing.T) {
	c := AIConfig{ReasoningTranslate: "none", ReasoningWrite: "high"}
	for kind, want := range map[string]string{
		"translate_email":    "none",
		"translate_segments": "none",
		"translate_text":     "none",
		"polish":             "high",
		"reply":              "high",
		"test":               "high",
	} {
		if got := c.reasoningFor(kind); got != want {
			t.Errorf("%s → %q, want %q", kind, got, want)
		}
	}
	for setting, want := range map[string][]string{
		"auto": {""},
		"":     {""},
		"off":  {""}, // 0.1.18 的老写法
		"low":  {"low"},
		"none": {"none", ""},
		"max":  {"max"},
	} {
		if got := aiEffortChain(setting); !slices.Equal(got, want) {
			t.Errorf("%q → %v, want %v", setting, got, want)
		}
	}
}

// 上游收下了 effort=none 却把输出全花在思考上、正文一个字没有: 当作这一档不可用, 不带参数重来。
func TestCallResponsesEmptyOutputFallsBack(t *testing.T) {
	var efforts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got struct {
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		json.NewDecoder(r.Body).Decode(&got)
		efforts = append(efforts, got.Reasoning.Effort)
		w.Header().Set("Content-Type", "text/event-stream")
		if got.Reasoning.Effort != "" {
			fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"想了半天\"}\n\n")
			fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"答案\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	defer srv.Close()

	var out strings.Builder
	if err := callResponses(context.Background(), srv.URL, "m", "k", "none", "i", "x", func(d string) { out.WriteString(d) }); err != nil || out.String() != "答案" {
		t.Fatalf("%q %v", out.String(), err)
	}
	if want := []string{"none", ""}; !slices.Equal(efforts, want) {
		t.Fatalf("efforts %v, want %v", efforts, want)
	}

	// 点名中间档时不退回, 空正文直接报错 —— 用户才看得出自己选的档位没生效。
	efforts = nil
	err := callResponses(context.Background(), srv.URL, "m", "k", "low", "i", "x", func(string) {})
	if err == nil || !strings.Contains(err.Error(), "2209") {
		t.Fatalf("err %v", err)
	}
	if !slices.Equal(efforts, []string{"low"}) {
		t.Fatalf("efforts %v, want one request", efforts)
	}
}
