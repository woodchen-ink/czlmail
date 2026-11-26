package submission_test

import (
	"testing"

	"github.com/woodchen-ink/czlmail/gateway/pkg/smtptest"
)

func TestIdentityMethods(t *testing.T) {
	h := newHarness(t, harnessOptions{smtp: smtptest.NewFake(t)})
	r := h.api([]any{"Identity/get", map[string]any{"accountId": h.acct}, "0"})
	got := args(t, r[0], "Identity/get")
	list := got["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("identities %v", list)
	}
	primary := list[0].(map[string]any)
	if primary["email"] != h.email || primary["mayDelete"] != false {
		t.Fatalf("primary %v", primary)
	}
	state := got["state"].(string)

	// Signatures are stored by the gateway; the address is immutable.
	r = h.api([]any{"Identity/set", map[string]any{"accountId": h.acct, "update": map[string]any{
		primary["id"].(string): map[string]any{"textSignature": "-- \nme", "name": "Me"},
	}}, "0"})
	if u := args(t, r[0], "Identity/set")["updated"].(map[string]any); len(u) != 1 {
		t.Fatalf("update %v", r)
	}
	r = h.api([]any{"Identity/set", map[string]any{"accountId": h.acct, "update": map[string]any{
		primary["id"].(string): map[string]any{"email": "other@example.org"},
	}}, "0"})
	if nu := args(t, r[0], "Identity/set")["notUpdated"].(map[string]any); nu[primary["id"].(string)] == nil {
		t.Fatalf("email change accepted: %v", r)
	}

	// Creating an address no identity covers is refused through JMAP...
	r = h.api([]any{"Identity/set", map[string]any{"accountId": h.acct, "create": map[string]any{
		"n": map[string]any{"email": "x@example.org", "name": "X"},
	}}, "0"})
	if nc := args(t, r[0], "Identity/set")["notCreated"].(map[string]any); nc["n"].(map[string]any)["type"] != "forbiddenFrom" {
		t.Fatalf("uncovered alias: %v", nc)
	}
	// ...allowed in the admin, and then a second name for it through JMAP.
	admin := h.gw.UserAdmin(t, h.cred)
	var alias struct {
		ID        string `json:"id"`
		Source    string `json:"source"`
		MayDelete bool   `json:"mayDelete"`
	}
	admin.Do(t, "POST", "/api/admin/upstreams/"+h.acct+"/identities", map[string]any{"email": "*@example.org", "name": "Org"}, &alias)
	if alias.Source != "alias" || !alias.MayDelete || alias.ID == "" {
		t.Fatalf("admin alias %+v", alias)
	}
	r = h.api([]any{"Identity/set", map[string]any{"accountId": h.acct, "create": map[string]any{
		"n": map[string]any{"email": "x@example.org", "name": "X"},
	}}, "0"})
	created := args(t, r[0], "Identity/set")["created"].(map[string]any)["n"].(map[string]any)

	r = h.api([]any{"Identity/changes", map[string]any{"accountId": h.acct, "sinceState": state}, "0"})
	ch := args(t, r[0], "Identity/changes")
	if len(ch["created"].([]any)) != 2 || len(ch["updated"].([]any)) != 1 {
		t.Fatalf("changes %v", ch)
	}

	// The primary identity cannot be deleted; aliases can.
	r = h.api([]any{"Identity/set", map[string]any{"accountId": h.acct, "destroy": []string{primary["id"].(string), created["id"].(string)}}, "0"})
	set := args(t, r[0], "Identity/set")
	if d := set["destroyed"].([]any); len(d) != 1 || d[0] != created["id"] {
		t.Fatalf("destroyed %v", set)
	}
	if code, _ := admin.Call(t, "DELETE", "/api/admin/upstreams/"+h.acct+"/identities/"+primary["id"].(string), nil, nil); code != 40931 {
		t.Fatalf("admin delete primary: %d", code)
	}
	var views []struct{ ID string }
	admin.Do(t, "GET", "/api/admin/upstreams/"+h.acct+"/identities", nil, &views)
	if len(views) != 2 {
		t.Fatalf("admin list %v", views)
	}
}
