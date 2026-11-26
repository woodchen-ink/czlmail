package upstream

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"
)

// TestMethodErrorKeepsUpstream go-jmap 解析响应时方法错误保留 czlUpstream, 也仍能当作 *jmap.MethodError 取类型。
func TestMethodErrorKeepsUpstream(t *testing.T) {
	var resp jmap.Response
	body := `{"methodResponses":[["error",{"type":"serverUnavailable","czlUpstream":{"status":"authRequired","code":40110}},"0"],
		["error",{"type":"accountNotFound"},"1"]],"sessionState":"7"}`
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	err, typ, ok := MethodErrorOf(resp.Responses[0].Args)
	if !ok || typ != "serverUnavailable" {
		t.Fatalf("first response: %T %q", resp.Responses[0].Args, typ)
	}
	if u := FromError(err); u == nil || u.Code != 40110 || u.Status != StatusAuthRequired {
		t.Fatalf("upstream = %+v", u)
	}
	if !strings.Contains(err.Error(), "40110 ") {
		t.Fatalf("error text %q lacks the gateway code", err)
	}
	var me *jmap.MethodError
	if !errors.As(err, &me) || me.Type != "serverUnavailable" {
		t.Fatal("not usable as *jmap.MethodError")
	}
	err, typ, ok = MethodErrorOf(resp.Responses[1].Args)
	if !ok || typ != "accountNotFound" || FromError(err) != nil {
		t.Fatalf("plain method error: %v %q %v", err, typ, ok)
	}
}

func TestErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		e    Error
		code string
	}{
		{Error{Status: StatusAuthRequired, Code: 40110}, "40110 "},
		{Error{Status: StatusUnreachable, Code: 50210}, "50210 "},
		{Error{Status: StatusUnreachable, Code: 42910}, "42910 "},
		{Error{Status: StatusSyncing, Code: 50310}, "50310 "},
		{Error{Status: StatusPaused, Code: 42310}, "42310 "},
		{Error{Status: StatusPaused}, "42310 "},
		{Error{Status: "somethingNew", Code: 50399}, "50399 "},
	} {
		if got := tc.e.Error(); !strings.HasPrefix(got, tc.code) {
			t.Errorf("%+v: %q", tc.e, got)
		}
	}
}

func TestOfAndDecode(t *testing.T) {
	caps := map[jmap.URI]json.RawMessage{
		"urn:ietf:params:jmap:mail": json.RawMessage(`{}`),
		URI:                         json.RawMessage(`{"kind":"gmail","status":"syncing","statusSince":"2026-11-26T09:00:00Z","code":50310,"reauthUrl":null,"initialSync":{"done":5,"total":10}}`),
	}
	i := Of(caps)
	if i == nil || i.Kind != "gmail" || i.OK() || i.InitialSync == nil || i.InitialSync.Total != 10 {
		t.Fatalf("Of = %+v", i)
	}
	b, _ := json.Marshal(caps)
	if d := Decode(string(b)); d == nil || d.Status != StatusSyncing {
		t.Fatalf("Decode = %+v", d)
	}
	if Of(map[jmap.URI]json.RawMessage{"urn:ietf:params:jmap:mail": json.RawMessage(`{}`)}) != nil {
		t.Fatal("non-gateway account has upstream info")
	}
	if Decode(`["urn:ietf:params:jmap:mail"]`) != nil || !(*Info)(nil).OK() {
		t.Fatal("legacy capability list")
	}
}
