package jmapserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/core"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

func TestCoreEchoThroughGoJMAP(t *testing.T) {
	e := newEnv(t, 0)
	c := e.client("alice")
	req := &jmap.Request{}
	req.Invoke(&core.Echo{Hello: "world"})
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Responses) != 1 || resp.Responses[0].Name != "Core/echo" || resp.Responses[0].CallID != "0" {
		t.Fatalf("responses: %+v", resp.Responses)
	}
	if got := resp.Responses[0].Args.(*core.Echo).Hello; got != "world" {
		t.Fatalf("echo: %q", got)
	}
	if resp.SessionState == "" {
		t.Fatal("no sessionState")
	}
}

// registerList adds Test/list returning a fixed result and Test/echo returning its arguments.
func registerList(srv *jmapserver.Server) {
	srv.Register("Test/list", testCap, func(_ context.Context, call *jmapserver.Call) (any, error) {
		var args struct {
			AccountID string `json:"accountId"`
		}
		if err := call.Decode(&args); err != nil {
			return nil, err
		}
		if _, err := call.Account(args.AccountID); err != nil {
			return nil, err
		}
		return map[string]any{
			"accountId": args.AccountID,
			"ids":       []string{"m1", "m2"},
			"list": []any{
				map[string]any{"id": "m1", "threadId": "t1", "keywords": []string{"$seen", "$flagged"}},
				map[string]any{"id": "m2", "threadId": "t2", "keywords": []string{"$draft"}},
			},
		}, nil
	})
	srv.Register("Test/echo", testCap, func(_ context.Context, call *jmapserver.Call) (any, error) {
		return call.Args, nil
	})
}

func TestResultReferences(t *testing.T) {
	e := newEnv(t, 0)
	registerList(e.srv)
	status, _, body := e.post(`{"using":["urn:ietf:params:jmap:core","urn:test"],"methodCalls":[
		["Test/list",{"accountId":"a1"},"c0"],
		["Test/echo",{"#ids":{"resultOf":"c0","name":"Test/list","path":"/ids"},
		              "#threads":{"resultOf":"c0","name":"Test/list","path":"/list/*/threadId"},
		              "#keywords":{"resultOf":"c0","name":"Test/list","path":"/list/*/keywords"},
		              "#first":{"resultOf":"c0","name":"Test/list","path":"/list/0/id"}},"c1"],
		["Test/echo",{"#x":{"resultOf":"c0","name":"Test/get","path":"/ids"}},"c2"],
		["Test/echo",{"#x":{"resultOf":"c0","name":"Test/list","path":"/nope"}},"c3"],
		["Test/echo",{"x":1,"#x":{"resultOf":"c0","name":"Test/list","path":"/ids"}},"c4"],
		["Test/echo",{"#x":{"resultOf":"missing","name":"Test/list","path":"/ids"}},"c5"]
	]}`)
	if status != 200 {
		t.Fatalf("%d %s", status, body)
	}
	r := responses(t, body)
	got, _ := json.Marshal(r[1].Args)
	want := `{"first":"m1","ids":["m1","m2"],"keywords":["$seen","$flagged","$draft"],"threads":["t1","t2"]}`
	if string(got) != want {
		t.Fatalf("resolved args:\n got %s\nwant %s", got, want)
	}
	for i, typ := range map[int]string{2: "invalidResultReference", 3: "invalidResultReference", 4: "invalidArguments", 5: "invalidResultReference"} {
		if r[i].Name != "error" || r[i].Args["type"] != typ {
			t.Errorf("call %d: %s %v, want %s", i, r[i].Name, r[i].Args, typ)
		}
	}
}

func TestMethodErrors(t *testing.T) {
	e := newEnv(t, 0)
	registerList(e.srv)
	e.srv.Register("Test/upstream", testCap, func(context.Context, *jmapserver.Call) (any, error) {
		return nil, jmapserver.ServerUnavailable(&jmapserver.UpstreamError{Status: jmapserver.UpstreamUnreachable, Code: jmapserver.CodeUpstreamUnreachable})
	})
	e.srv.Register("Test/crash", testCap, func(context.Context, *jmapserver.Call) (any, error) {
		return nil, fmt.Errorf("upstream said: secret detail")
	})
	_, _, body := e.post(`{"using":["urn:ietf:params:jmap:core","urn:test"],"methodCalls":[
		["Nope/get",{},"0"],
		["Test/list",{"accountId":"zz"},"1"],
		["Test/upstream",{},"2"],
		["Test/crash",{},"3"],
		["Test/list",{"accountId":5},"4"]
	]}`)
	r := responses(t, body)
	if len(r) != 5 {
		t.Fatalf("responses: %s", body)
	}
	for i, typ := range []string{"unknownMethod", "accountNotFound", "serverUnavailable", "serverFail", "invalidArguments"} {
		if r[i].Name != "error" || r[i].Args["type"] != typ || r[i].CallID != fmt.Sprint(i) {
			t.Errorf("call %d: %s %v, want %s", i, r[i].Name, r[i].Args, typ)
		}
	}
	if up, _ := r[2].Args["czlUpstream"].(map[string]any); up["status"] != "unreachable" || up["code"] != 50210.0 {
		t.Errorf("czlUpstream: %v", r[2].Args)
	}
	if strings.Contains(string(body), "secret") {
		t.Error("internal error text leaked to the client")
	}

	// A registered method whose capability is not in `using` is unknown.
	_, _, body = e.post(`{"using":["urn:ietf:params:jmap:core"],"methodCalls":[["Test/echo",{},"0"]]}`)
	if r := responses(t, body); r[0].Args["type"] != "unknownMethod" {
		t.Fatalf("capability not used: %s", body)
	}
}

func TestRequestLevelErrors(t *testing.T) {
	e := newEnv(t, 0)
	calls := make([]string, 17)
	for i := range calls {
		calls[i] = fmt.Sprintf(`["Core/echo",{},"%d"]`, i)
	}
	cases := []struct {
		name, body, typ, limit string
		code                   float64
	}{
		{"unknown capability", `{"using":["urn:ietf:params:jmap:core","urn:nope"],"methodCalls":[]}`,
			"urn:ietf:params:jmap:error:unknownCapability", "", 40022},
		{"not json", `{"using":`, "urn:ietf:params:jmap:error:notJSON", "", 40020},
		{"not request", `{"using":["urn:ietf:params:jmap:core"]}`, "urn:ietf:params:jmap:error:notRequest", "", 40021},
		{"bad invocation", `{"using":[],"methodCalls":[["Core/echo",[],"0"]]}`, "urn:ietf:params:jmap:error:notRequest", "", 40021},
		{"too many calls", `{"using":["urn:ietf:params:jmap:core"],"methodCalls":[` + strings.Join(calls, ",") + `]}`,
			"urn:ietf:params:jmap:error:limit", "maxCallsInRequest", 40023},
		{"too large", `{"using":["urn:ietf:params:jmap:core"],"methodCalls":[],"pad":"` + strings.Repeat("x", jmapserver.MaxSizeRequest) + `"}`,
			"urn:ietf:params:jmap:error:limit", "maxSizeRequest", 40023},
	}
	for _, tc := range cases {
		status, ctype, body := e.post(tc.body)
		if status != http.StatusBadRequest || ctype != "application/problem+json" {
			t.Errorf("%s: %d %s", tc.name, status, ctype)
			continue
		}
		p := problemOf(t, body)
		if p["type"] != tc.typ || p["code"] != tc.code || (tc.limit != "" && p["limit"] != tc.limit) {
			t.Errorf("%s: %v", tc.name, p)
		}
	}
}

func TestConcurrentRequestLimit(t *testing.T) {
	e := newEnv(t, 0)
	entered, release := make(chan struct{}), make(chan struct{})
	e.srv.Register("Test/block", testCap, func(context.Context, *jmapserver.Call) (any, error) {
		entered <- struct{}{}
		<-release
		return map[string]any{}, nil
	})
	const block = `{"using":["urn:ietf:params:jmap:core","urn:test"],"methodCalls":[["Test/block",{},"0"]]}`
	var wg sync.WaitGroup
	for range jmapserver.MaxConcurrentRequests {
		wg.Go(func() { e.post(block) })
		<-entered
	}
	status, _, body := e.post(block)
	close(release)
	wg.Wait()
	if p := problemOf(t, body); status != http.StatusBadRequest || p["limit"] != "maxConcurrentRequests" {
		t.Fatalf("fifth request: %d %s", status, body)
	}
	// Slots are released afterwards.
	if status, _, body := e.post(`{"using":["urn:ietf:params:jmap:core"],"methodCalls":[["Core/echo",{},"0"]]}`); status != 200 {
		t.Fatalf("after release: %d %s", status, body)
	}
}

func TestCreatedIDsAndImplicitResponses(t *testing.T) {
	e := newEnv(t, 0)
	e.srv.Register("Test/set", testCap, func(_ context.Context, call *jmapserver.Call) (any, error) {
		call.CreatedIDs["k1"] = "m9"
		call.AddResponse("Test/implicit", map[string]any{"updated": []string{"m9"}})
		return map[string]any{"created": map[string]any{"k1": map[string]string{"id": "m9"}}}, nil
	})
	_, _, body := e.post(`{"using":["urn:ietf:params:jmap:core","urn:test"],"createdIds":{"k0":"m0"},"methodCalls":[["Test/set",{},"s"]]}`)
	r := responses(t, body)
	if len(r) != 2 || r[0].Name != "Test/set" || r[1].Name != "Test/implicit" || r[1].CallID != "s" {
		t.Fatalf("responses: %s", body)
	}
	var top struct {
		CreatedIDs map[string]string `json:"createdIds"`
	}
	_ = json.Unmarshal(body, &top)
	if top.CreatedIDs["k0"] != "m0" || top.CreatedIDs["k1"] != "m9" {
		t.Fatalf("createdIds: %v", top.CreatedIDs)
	}
}
