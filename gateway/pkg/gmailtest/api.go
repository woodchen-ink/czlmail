package gmailtest

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// SendAs is one entry of users.settings.sendAs.
type SendAs struct {
	SendAsEmail        string `json:"sendAsEmail"`
	DisplayName        string `json:"displayName,omitempty"`
	ReplyToAddress     string `json:"replyToAddress,omitempty"`
	Signature          string `json:"signature,omitempty"`
	IsPrimary          bool   `json:"isPrimary,omitempty"`
	IsDefault          bool   `json:"isDefault,omitempty"`
	TreatAsAlias       bool   `json:"treatAsAlias,omitempty"`
	VerificationStatus string `json:"verificationStatus,omitempty"`
}

// Sent is one message received by users.messages.send.
type Sent struct {
	ID       string
	ThreadID string
	Raw      []byte
}

// API is an httptest stand-in for the Gmail REST API of one account.
type API struct {
	*httptest.Server

	// Tokens checks the Bearer token; nil accepts none.
	Tokens TokenValidator
	// Mail, when set, receives every sent message under \Sent with the X-GM-MSGID
	// the API returned (Gmail API ids are X-GM-MSGID in hex), so IMAP sees it too.
	Mail *Server
	// Account is the address tokens must belong to. Defaults to Mail's account.
	Account string

	mu       sync.Mutex
	sendAs   []SendAs
	sent     []Sent
	nextID   uint64
	failSend []apiFailure
}

type apiFailure struct {
	status int
	reason string
}

// NewAPI starts the API; it is closed when the test ends. mail may be nil.
func NewAPI(t testing.TB, tokens TokenValidator, mail *Server) *API {
	a := &API{Tokens: tokens, Mail: mail, nextID: 0x18f0000000000001}
	if mail != nil {
		a.Account = mail.Account()
		a.sendAs = []SendAs{{SendAsEmail: mail.Account(), IsPrimary: true, IsDefault: true, VerificationStatus: "accepted"}}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /gmail/v1/users/{user}/messages/send", a.send)
	mux.HandleFunc("POST /upload/gmail/v1/users/{user}/messages/send", a.send)
	mux.HandleFunc("GET /gmail/v1/users/{user}/settings/sendAs", a.listSendAs)
	mux.HandleFunc("GET /gmail/v1/users/{user}/profile", a.profile)
	a.Server = httptest.NewServer(mux)
	t.Cleanup(a.Close)
	return a
}

// SetSendAs replaces the send-as list.
func (a *API) SetSendAs(list ...SendAs) {
	a.mu.Lock()
	a.sendAs = list
	a.mu.Unlock()
}

// SentMessages returns every message sent so far.
func (a *API) SentMessages() []Sent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Sent(nil), a.sent...)
}

// FailNextSend makes the next send fail with an HTTP status and Google error reason,
// e.g. (429, "rateLimitExceeded") or (400, "invalidArgument").
func (a *API) FailNextSend(status int, reason string) {
	a.mu.Lock()
	a.failSend = append(a.failSend, apiFailure{status, reason})
	a.mu.Unlock()
}

func (a *API) authorize(w http.ResponseWriter, r *http.Request) bool {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	user, valid := "", false
	if ok && a.Tokens != nil {
		user, valid = a.Tokens.Validate(tok)
	}
	if !valid || (a.Account != "" && !strings.EqualFold(user, a.Account)) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="https://accounts.google.com/", error="invalid_token"`)
		googleError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authError",
			"Request had invalid authentication credentials. Expected OAuth 2 access token.")
		return false
	}
	if u := r.PathValue("user"); u != "me" && !strings.EqualFold(u, user) {
		googleError(w, http.StatusForbidden, "PERMISSION_DENIED", "forbidden", "Delegation denied for "+user)
		return false
	}
	return true
}

func (a *API) send(w http.ResponseWriter, r *http.Request) {
	if !a.authorize(w, r) {
		return
	}
	var raw []byte
	var threadID string
	if strings.HasPrefix(r.URL.Path, "/upload/") {
		if t := r.URL.Query().Get("uploadType"); t != "media" {
			googleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "badRequest", "gmailtest supports uploadType=media only")
			return
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, 36<<20))
		if err != nil {
			googleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "badRequest", err.Error())
			return
		}
		raw = b
	} else {
		var body struct {
			Raw      string `json:"raw"`
			ThreadID string `json:"threadId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			googleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "badRequest", "Invalid JSON payload")
			return
		}
		b, err := base64.URLEncoding.DecodeString(body.Raw)
		if err != nil {
			b, err = base64.RawURLEncoding.DecodeString(body.Raw)
		}
		if err != nil {
			googleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalidArgument", "Invalid value for ByteString")
			return
		}
		raw, threadID = b, body.ThreadID
	}
	if len(raw) == 0 || !strings.Contains(strings.ToLower(string(raw)), "\nto:") && !strings.HasPrefix(strings.ToLower(string(raw)), "to:") {
		googleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalidArgument", "Recipient address required")
		return
	}

	a.mu.Lock()
	if len(a.failSend) > 0 {
		f := a.failSend[0]
		a.failSend = a.failSend[1:]
		a.mu.Unlock()
		googleError(w, f.status, http.StatusText(f.status), f.reason, "gmailtest: injected failure")
		return
	}
	var id, thr string
	if a.Mail != nil {
		m := a.Mail.insertSent(raw)
		id, thr = strconv.FormatUint(m.GmMsgID, 16), strconv.FormatUint(m.GmThrID, 16)
	} else {
		id = strconv.FormatUint(a.nextID, 16)
		a.nextID++
		thr = id
	}
	if threadID != "" {
		thr = threadID
	}
	a.sent = append(a.sent, Sent{ID: id, ThreadID: thr, Raw: raw})
	a.mu.Unlock()

	writeJSON(w, map[string]any{"id": id, "threadId": thr, "labelIds": []string{"SENT"}})
}

// insertSent files a message sent through the API under \Sent.
func (s *Server) insertSent(raw []byte) *Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.insertLocked(s.folderLocked(SentMail), raw, []string{`\Seen`}, time.Now())
	s.syncLocked()
	return m
}

func (a *API) listSendAs(w http.ResponseWriter, r *http.Request) {
	if !a.authorize(w, r) {
		return
	}
	a.mu.Lock()
	list := append([]SendAs{}, a.sendAs...)
	a.mu.Unlock()
	writeJSON(w, map[string]any{"sendAs": list})
}

func (a *API) profile(w http.ResponseWriter, r *http.Request) {
	if !a.authorize(w, r) {
		return
	}
	total := 0
	if a.Mail != nil {
		a.Mail.mu.Lock()
		total = len(a.Mail.st.msgs)
		a.Mail.mu.Unlock()
	}
	writeJSON(w, map[string]any{"emailAddress": a.Account, "messagesTotal": total, "threadsTotal": total, "historyId": "1"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	_ = json.NewEncoder(w).Encode(v)
}

// googleError writes Google's JSON error shape.
func googleError(w http.ResponseWriter, code int, status, reason, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"code":    code,
		"message": msg,
		"status":  status,
		"errors":  []map[string]string{{"message": msg, "domain": "global", "reason": reason}},
	}})
}
