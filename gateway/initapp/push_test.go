package initapp_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"
)

// Push subscriptions are tied to the app password that created them: revoking
// one device's password stops pushes to that device only.
func TestPushSubscriptionsFollowAppPasswords(t *testing.T) {
	app, base := newGateway(t)
	token, _ := app.Auth.Accounts.IssueSetupToken()
	jar, _ := cookiejar.New(nil)
	adm := &admin{t: t, base: base, http: &http.Client{Jar: jar}}
	adm.do("POST", "/api/admin/setup", map[string]string{
		"token": token, "email": "owner@example.com", "name": "Owner", "password": "correct horse battery",
	})
	var phone, tablet struct{ ID, Password string }
	_ = json.Unmarshal(adm.do("POST", "/api/admin/app-passwords", map[string]string{"name": "Phone"}), &phone)
	_ = json.Unmarshal(adm.do("POST", "/api/admin/app-passwords", map[string]string{"name": "Tablet"}), &tablet)

	call := func(password, method string, args any) map[string]any {
		t.Helper()
		a, _ := json.Marshal(args)
		body := `{"using":["urn:ietf:params:jmap:core"],"methodCalls":[["` + method + `",` + string(a) + `,"c0"]]}`
		req, _ := http.NewRequest("POST", base+"/jmap/api", bytes.NewBufferString(body))
		req.SetBasicAuth("owner@example.com", password)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var r struct {
			MethodResponses [][3]json.RawMessage `json:"methodResponses"`
		}
		if res.StatusCode != 200 || json.Unmarshal(raw, &r) != nil || len(r.MethodResponses) != 1 {
			t.Fatalf("%s: %d %s", method, res.StatusCode, raw)
		}
		var out map[string]any
		_ = json.Unmarshal(r.MethodResponses[0][1], &out)
		return out
	}
	create := func(password, device string) {
		t.Helper()
		// .invalid never resolves, so the verification goes nowhere.
		res := call(password, "PushSubscription/set", map[string]any{"create": map[string]any{
			"s": map[string]any{"deviceClientId": device, "url": "https://push.invalid/p/" + device},
		}})
		if res["created"] == nil {
			t.Fatalf("create: %v", res)
		}
	}
	create(phone.Password, "phone")
	create(tablet.Password, "tablet")
	if l := call(tablet.Password, "PushSubscription/get", map[string]any{})["list"].([]any); len(l) != 2 {
		t.Fatalf("list %v", l)
	}

	adm.do("DELETE", "/api/admin/app-passwords/"+phone.ID, nil)
	deadline := time.Now().Add(3 * time.Second)
	for {
		l := call(tablet.Password, "PushSubscription/get", map[string]any{})["list"].([]any)
		if len(l) == 1 && l[0].(map[string]any)["deviceClientId"] == "tablet" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after revoking the phone: %v", l)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
