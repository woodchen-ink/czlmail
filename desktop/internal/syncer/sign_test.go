package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/smime"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// fakeSignServer 模拟签名发送用到的几个方法, 记下调用顺序与上传的原文。
type fakeSignServer struct {
	mu         sync.Mutex
	calls      []string
	destroyed  []string
	uploaded   string
	submission map[string]any
}

func (f *fakeSignServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/download/"):
			f.calls = append(f.calls, "download")
			io.WriteString(w, "From: a@example.cz\r\nTo: b@example.cz\r\nBcc: Hidden\r\n <blind@example.test>\r\nSubject: x\r\nContent-Type: text/plain\r\n\r\nahoj\r\n")
		case strings.HasPrefix(r.URL.Path, "/upload/"):
			b, _ := io.ReadAll(r.Body)
			f.uploaded = string(b)
			f.calls = append(f.calls, "upload")
			json.NewEncoder(w).Encode(map[string]any{"accountId": "acc", "blobId": "SIGNED", "type": "message/rfc822", "size": len(b)})
		case r.URL.Path == "/api":
			var req struct {
				MethodCalls [][3]json.RawMessage `json:"methodCalls"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode: %v", err)
			}
			var responses []any
			for _, mc := range req.MethodCalls {
				var name, callID string
				var args map[string]any
				json.Unmarshal(mc[0], &name)
				json.Unmarshal(mc[1], &args)
				json.Unmarshal(mc[2], &callID)
				f.calls = append(f.calls, name)
				var out map[string]any
				switch name {
				case "Email/set":
					out = map[string]any{"accountId": "acc", "newState": "2"}
					if _, ok := args["create"]; ok {
						out["created"] = map[string]any{"draft": map[string]any{"id": "TMP", "blobId": "RAW", "threadId": "T", "size": 10}}
					}
					if d, ok := args["destroy"].([]any); ok {
						for _, id := range d {
							f.destroyed = append(f.destroyed, id.(string))
						}
						out["destroyed"] = d
					}
				case "Email/import":
					out = map[string]any{"accountId": "acc", "newState": "3", "created": map[string]any{"signed": map[string]any{"id": "SIG", "blobId": "SIGNED", "threadId": "T", "size": 10}}}
				case "EmailSubmission/set":
					f.submission = args
					out = map[string]any{"accountId": "acc", "newState": "1", "notCreated": map[string]any{"sub": map[string]any{"type": "forbiddenFrom"}}}
				default:
					t.Errorf("unexpected method %s", name)
					out = map[string]any{}
				}
				responses = append(responses, []any{name, out, callID})
			}
			json.NewEncoder(w).Encode(map[string]any{"methodResponses": responses, "sessionState": "s"})
		default:
			http.NotFound(w, r)
		}
	})
}

func signSyncer(server *httptest.Server) *Syncer {
	return &Syncer{
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		client: &jmap.Client{
			HttpClient: server.Client(),
			Session: &jmap.Session{
				APIURL:      server.URL + "/api",
				UploadURL:   server.URL + "/upload/{accountId}/",
				DownloadURL: server.URL + "/download/{accountId}/{blobId}/{name}?type={type}",
				RawCapabilities: map[jmap.URI]json.RawMessage{
					"urn:ietf:params:jmap:core":       json.RawMessage(`{}`),
					"urn:ietf:params:jmap:mail":       json.RawMessage(`{}`),
					"urn:ietf:params:jmap:submission": json.RawMessage(`{}`),
				},
			},
		},
	}
}

func signDraft(sign func([]byte) ([]byte, error)) Draft {
	return Draft{
		AccountID: "acc", IdentityID: "id", FromEmail: "a@example.cz",
		To: []store.Address{{Email: "b@example.cz"}}, Subject: "x", TextBody: "ahoj",
		ReplaceDraftID: "OLD", Sign: sign,
	}
}

// 签不了名时邮件不发出, 临时草稿被删, 用户原来的草稿留着。
func TestSendSignedFailsClosed(t *testing.T) {
	f := &fakeSignServer{}
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	boom := errors.New("2402 expired")
	err := signSyncer(server).Send(context.Background(), signDraft(func([]byte) ([]byte, error) { return nil, boom }), "drafts", "sent")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	for _, c := range f.calls {
		if c == "EmailSubmission/set" || c == "upload" {
			t.Fatalf("nothing may be submitted when signing fails: %v", f.calls)
		}
	}
	if strings.Join(f.destroyed, ",") != "TMP" {
		t.Fatalf("destroyed %v, want only the temporary draft", f.destroyed)
	}
}

// 服务器拒绝提交时: 签过名的那封和临时草稿都删掉, 原草稿不动; 上传的是签名函数的输出。
func TestSendSignedSubmissionRejected(t *testing.T) {
	f := &fakeSignServer{}
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	var got string
	err := signSyncer(server).Send(context.Background(), signDraft(func(raw []byte) ([]byte, error) {
		got = string(raw)
		return []byte("SIGNED " + string(raw)), nil
	}), "drafts", "sent")
	if err == nil || !strings.Contains(err.Error(), "forbiddenFrom") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(got, "Subject: x") || f.uploaded != "SIGNED "+got {
		t.Fatalf("signed %q, uploaded %q", got, f.uploaded)
	}
	want := []string{"Email/set", "download", "upload", "Email/import", "EmailSubmission/set", "Email/set"}
	if strings.Join(f.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls %v, want %v", f.calls, want)
	}
	if strings.Join(f.destroyed, ",") != "TMP,SIG" {
		t.Fatalf("destroyed %v", f.destroyed)
	}
}

func TestSignedBlindRecipientOnlyInEnvelope(t *testing.T) {
	f := &fakeSignServer{}
	server := httptest.NewServer(f.handler(t))
	defer server.Close()
	d := signDraft(func(raw []byte) ([]byte, error) {
		return smime.SignMessage(raw, func([]byte) ([]byte, error) { return []byte("unit signature"), nil })
	})
	d.To = nil
	d.BCC = []store.Address{{Email: "blind@example.test"}}
	err := signSyncer(server).Send(context.Background(), d, "drafts", "sent")
	if err == nil || !strings.Contains(err.Error(), "forbiddenFrom") {
		t.Fatalf("mock submission: %v", err)
	}
	if f.uploaded == "" || strings.Contains(f.uploaded, "blind@example.test") || strings.Contains(strings.ToLower(f.uploaded), "bcc:") {
		t.Fatal("unexpected message headers")
	}
	var submission struct {
		Create map[string]struct {
			Envelope struct{ RcptTo []struct{ Email string } }
		}
	}
	b, _ := json.Marshal(f.submission)
	if err := json.Unmarshal(b, &submission); err != nil {
		t.Fatal(err)
	}
	recipients := submission.Create["sub"].Envelope.RcptTo
	if len(recipients) != 1 || recipients[0].Email != "blind@example.test" {
		t.Fatalf("wrong envelope %v", recipients)
	}
}
