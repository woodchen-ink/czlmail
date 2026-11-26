package gmailtest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imaptest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/oauthtest"
)

func apiCall(t *testing.T, method, url, token string, body []byte, contentType string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

func TestAPISendAppearsInSentMail(t *testing.T) {
	provider := oauthtest.New(t)
	mail := New(t, Options{Account: "me@gmail.test", Tokens: provider})
	api := NewAPI(t, provider, mail)
	tok := provider.IssueAccessToken("me@gmail.test")

	raw := imaptest.SampleFrom("me@gmail.test", "you@example.test", "sent via API")
	body, _ := json.Marshal(map[string]string{"raw": base64.URLEncoding.EncodeToString(raw)})
	code, out := apiCall(t, "POST", api.URL+"/gmail/v1/users/me/messages/send", tok, body, "application/json")
	if code != 200 {
		t.Fatalf("send: %d %v", code, out)
	}
	id, _ := out["id"].(string)
	gmID, err := strconv.ParseUint(id, 16, 64)
	if err != nil {
		t.Fatalf("id %q is not hex: %v", id, err)
	}
	m := mail.ByGmMsgID(gmID)
	if m == nil || mail.UID(SentMail, m) == 0 {
		t.Fatal("sent message not in [Gmail]/Sent Mail under the returned id")
	}

	// Media upload form.
	code, _ = apiCall(t, "POST", api.URL+"/upload/gmail/v1/users/me/messages/send?uploadType=media", tok, raw, "message/rfc822")
	if code != 200 || len(api.SentMessages()) != 2 {
		t.Fatalf("upload send: %d, %d sent", code, len(api.SentMessages()))
	}

	api.FailNextSend(429, "rateLimitExceeded")
	code, out = apiCall(t, "POST", api.URL+"/gmail/v1/users/me/messages/send", tok, body, "application/json")
	if code != 429 {
		t.Fatalf("injected failure: %d %v", code, out)
	}
}

func TestAPISendAsAndAuth(t *testing.T) {
	provider := oauthtest.New(t)
	api := NewAPI(t, provider, New(t, Options{Account: "me@gmail.test"}))
	api.SetSendAs(
		SendAs{SendAsEmail: "me@gmail.test", IsPrimary: true, IsDefault: true},
		SendAs{SendAsEmail: "alias@example.test", DisplayName: "Alias", TreatAsAlias: true, VerificationStatus: "accepted"},
	)

	code, out := apiCall(t, "GET", api.URL+"/gmail/v1/users/me/settings/sendAs", provider.IssueAccessToken("me@gmail.test"), nil, "")
	list, _ := out["sendAs"].([]any)
	if code != 200 || len(list) != 2 {
		t.Fatalf("sendAs: %d %v", code, out)
	}

	code, out = apiCall(t, "GET", api.URL+"/gmail/v1/users/me/settings/sendAs", "bogus", nil, "")
	if code != 401 {
		t.Fatalf("bad token: %d %v", code, out)
	}
	code, _ = apiCall(t, "GET", api.URL+"/gmail/v1/users/me/settings/sendAs", provider.IssueAccessToken("other@gmail.test"), nil, "")
	if code != 401 {
		t.Fatalf("token for another account: %d", code)
	}
}
