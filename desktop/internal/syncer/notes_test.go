package syncer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"
	"github.com/woodchen-ink/czlmail/desktop/internal/notes"
)

// TestFileConditionProbe rejects ignored preconditions and never modifies real objects.
func TestFileConditionProbe(t *testing.T) {
	for _, enforced := range []bool{true, false} {
		t.Run(map[bool]string{true: "enforced", false: "ignored"}[enforced], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Calls []json.RawMessage `json:"methodCalls"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				responses := []any{}
				for _, raw := range request.Calls {
					var call []json.RawMessage
					json.Unmarshal(raw, &call)
					var name, id string
					json.Unmarshal(call[0], &name)
					json.Unmarshal(call[2], &id)
					if name == "FileNode/get" {
						responses = append(responses, []any{name, map[string]any{"accountId": "c", "state": "s3wmac", "list": []any{}, "notFound": []any{}}, id})
					} else {
						var args map[string]any
						json.Unmarshal(call[1], &args)
						if args["ifInState"] != "n" || args["create"] != nil || args["update"] != nil || args["destroy"] != nil {
							t.Errorf("unsafe probe: %v", args)
						}
						calls++
						if enforced {
							responses = append(responses, []any{"error", map[string]any{"type": "stateMismatch"}, id})
						} else {
							responses = append(responses, []any{"FileNode/set", map[string]any{"accountId": "c", "oldState": "s3wmac", "newState": "s3wmac"}, id})
						}
					}
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"methodResponses": responses, "sessionState": "session"})
			}))
			defer server.Close()
			s := &Syncer{client: &jmap.Client{Session: &jmap.Session{APIURL: server.URL, RawCapabilities: map[jmap.URI]json.RawMessage{jmap.CoreURI: json.RawMessage(`{}`), "urn:ietf:params:jmap:filenode": json.RawMessage(`{}`)}}, HttpClient: server.Client()}}
			err := s.verifyFileConditions(context.Background(), "c")
			if enforced && err != nil {
				t.Fatal(err)
			}
			if !enforced && err != notes.ErrUnavailable {
				t.Fatalf("ignored condition was allowed: %v", err)
			}
			if calls != 1 {
				t.Fatal("probe count", calls)
			}
		})
	}
}
