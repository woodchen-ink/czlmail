package imap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Gmail API limits.
const (
	gmailAPITimeout = 2 * time.Minute
	// gmailJSONMax is the largest message sent as JSON {raw, threadId}; larger
	// ones go through the media upload, which cannot carry a threadId.
	gmailJSONMax = 4 << 20
	// identityTTL is how long settings.sendAs.list is reused.
	identityTTL = 6 * time.Hour
)

// gmailAPI calls the Gmail REST API of one account with its OAuth token,
// through the upstream's dialer.
type gmailAPI struct {
	base     string
	hc       *http.Client
	token    func(ctx context.Context) (string, error)
	rejected func(token string)
}

func newGmailAPI(cfg upstream.Config, hc *http.Client) *gmailAPI {
	base := strings.TrimRight(cfg.APIBase, "/")
	if base == "" {
		base = "https://gmail.googleapis.com"
	}
	return &gmailAPI{base: base, hc: hc, token: cfg.AccessToken, rejected: cfg.TokenRejected}
}

// apiError is Google's error body.
type apiError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Errors  []struct {
			Reason string `json:"reason"`
		} `json:"errors"`
	} `json:"error"`
}

func (a *gmailAPI) do(ctx context.Context, method, path, contentType string, body []byte, out any) error {
	err := a.try(ctx, method, path, contentType, body, out)
	var rt *rejectedToken
	if a.rejected != nil && errors.As(err, &rt) {
		a.rejected(rt.token)
		err = a.try(ctx, method, path, contentType, body, out)
	}
	return err
}

// rejectedToken marks a 401: the access token was refused.
type rejectedToken struct {
	token string
	err   error
}

func (e *rejectedToken) Error() string { return e.err.Error() }
func (e *rejectedToken) Unwrap() error { return e.err }

func (a *gmailAPI) try(ctx context.Context, method, path, contentType string, body []byte, out any) error {
	if a.token == nil {
		return &upstream.Error{Kind: upstream.KindAuth, Op: "gmail api", Err: errors.New("no OAuth token")}
	}
	tok, err := a.token(ctx)
	if err != nil {
		return upstream.Classify("gmail api token", err)
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := a.hc.Do(req)
	if err != nil {
		return &upstream.Error{Kind: upstream.KindUnreachable, Op: "gmail api", Err: err}
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return &upstream.Error{Kind: upstream.KindUnreachable, Op: "gmail api", Err: err}
	}
	if res.StatusCode/100 != 2 {
		err := apiFailure(res.StatusCode, data)
		if res.StatusCode == http.StatusUnauthorized {
			ue := err.(*upstream.Error)
			ue.Err = &rejectedToken{token: tok, err: ue.Err}
		}
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// apiFailure classifies an API error: 401 means the grant is gone, rate limits
// are throttling, other 4xx reject this one request, 5xx are retried.
func apiFailure(status int, body []byte) error {
	var e apiError
	_ = json.Unmarshal(body, &e)
	reason := ""
	if len(e.Error.Errors) > 0 {
		reason = e.Error.Errors[0].Reason
	}
	err := fmt.Errorf("gmail api: %d %s %s: %s", status, e.Error.Status, reason, e.Error.Message)
	kind := upstream.KindUnreachable
	switch {
	case status == http.StatusUnauthorized:
		kind = upstream.KindAuth
	case status == http.StatusTooManyRequests, reason == "rateLimitExceeded", reason == "userRateLimitExceeded":
		kind = upstream.KindThrottled
	case status >= 400 && status < 500:
		kind = upstream.KindRejected
	}
	return &upstream.Error{Kind: kind, Op: "gmail api", Err: err}
}

// sendResponse is users.messages.send's answer. Ids are hexadecimal X-GM-MSGID
// / X-GM-THRID.
type sendResponse struct {
	ID       string `json:"id"`
	ThreadID string `json:"threadId"`
}

// send posts raw (with threadID, "" for none) to users.messages.send.
func (a *gmailAPI) send(ctx context.Context, raw []byte, threadID string) (*sendResponse, error) {
	var out sendResponse
	var err error
	if threadID != "" && len(raw) <= gmailJSONMax {
		body, _ := json.Marshal(map[string]string{"raw": base64.URLEncoding.EncodeToString(raw), "threadId": threadID})
		err = a.do(ctx, http.MethodPost, "/gmail/v1/users/me/messages/send", "application/json", body, &out)
	} else {
		err = a.do(ctx, http.MethodPost, "/upload/gmail/v1/users/me/messages/send?uploadType=media", "message/rfc822", raw, &out)
	}
	if err == nil && out.ID == "" {
		err = &upstream.Error{Kind: upstream.KindUnreachable, Op: "gmail send", Err: errors.New("no message id in the answer")}
	}
	return &out, err
}

type sendAs struct {
	SendAsEmail        string `json:"sendAsEmail"`
	DisplayName        string `json:"displayName"`
	ReplyToAddress     string `json:"replyToAddress"`
	Signature          string `json:"signature"`
	IsPrimary          bool   `json:"isPrimary"`
	VerificationStatus string `json:"verificationStatus"`
}

func (a *gmailAPI) sendAsList(ctx context.Context) ([]sendAs, error) {
	var out struct {
		SendAs []sendAs `json:"sendAs"`
	}
	err := a.do(ctx, http.MethodGet, "/gmail/v1/users/me/settings/sendAs", "", nil, &out)
	return out.SendAs, err
}

// IdentityID is the stable Identity id of an address: "i" + 20 base32
// characters of its SHA-256 (lower-cased address).
func IdentityID(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return "i" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))[:20]
}

// Identities lists the addresses the account may send as, from
// settings.sendAs.list, cached for six hours (the last list is kept when a
// refresh fails); see upstream.IdentitySource.
func (g *Gmail) Identities(ctx context.Context) ([]upstream.Identity, error) {
	gm := g.gm
	gm.idMu.Lock()
	defer gm.idMu.Unlock()
	now := g.opts.Now()
	if gm.identities != nil && now.Sub(gm.identAt) < identityTTL {
		return cloneIdentities(gm.identities), nil
	}
	list, err := gm.api.sendAsList(ctx)
	if err != nil {
		if gm.identities != nil {
			g.log.Warn("gmail sendAs refresh failed; using the last list", "err", err)
			return cloneIdentities(gm.identities), nil
		}
		return nil, err
	}
	out := make([]upstream.Identity, 0, len(list))
	for _, s := range list {
		if !s.IsPrimary && s.VerificationStatus != "accepted" {
			continue
		}
		out = append(out, upstream.Identity{
			ID: IdentityID(s.SendAsEmail), Name: s.DisplayName, Email: strings.ToLower(s.SendAsEmail),
			ReplyTo: s.ReplyToAddress, HTMLSignature: s.Signature,
		})
	}
	gm.identities, gm.identAt = out, now
	return cloneIdentities(out), nil
}

func cloneIdentities(l []upstream.Identity) []upstream.Identity {
	return append([]upstream.Identity{}, l...)
}
