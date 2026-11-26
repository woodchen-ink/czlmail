package jmapserver

import (
	"encoding/json"
)

// Push message bodies (before encryption).

type pushVerification struct {
	Type               string `json:"@type"`
	PushSubscriptionID string `json:"pushSubscriptionId"`
	VerificationCode   string `json:"verificationCode"`
}

type emailPush struct {
	Type      string           `json:"@type"`
	AccountID string           `json:"accountId"`
	Emails    []map[string]any `json:"emails"`
	State     *string          `json:"state"`
}

// buildEmailPushes turns one account's new messages into EmailPush bodies of at
// most budget bytes (draft-ietf-jmap-emailpush §4). Messages are batched while
// they fit; a message that does not fit even alone keeps the longest prefix of
// props that fits, so the most important properties survive.
func buildEmailPushes(accountID, state string, mails []*NewMail, props []string, budget int) [][]byte {
	var st *string
	if state != "" {
		st = &state
	}
	encode := func(emails []map[string]any) []byte {
		b, _ := json.Marshal(emailPush{Type: "EmailPush", AccountID: accountID, Emails: emails, State: st})
		return b
	}

	var out [][]byte
	var batch []map[string]any
	for _, m := range mails {
		full := pickProperties(m, props, len(props))
		if b := encode(append(batch, full)); len(b) <= budget {
			batch = append(batch, full)
			continue
		}
		if len(batch) > 0 {
			out = append(out, encode(batch))
			batch = nil
		}
		obj := full
		if len(encode([]map[string]any{full})) > budget {
			// Alone and still too large: add properties in priority order until one overflows.
			n := 0
			for n < len(props) && len(encode([]map[string]any{pickProperties(m, props, n+1)})) <= budget {
				n++
			}
			obj = pickProperties(m, props, n)
		}
		batch = []map[string]any{obj}
	}
	if len(batch) > 0 {
		out = append(out, encode(batch))
	}
	return out
}

// pickProperties returns the first n requested properties the message has.
func pickProperties(m *NewMail, props []string, n int) map[string]any {
	obj := make(map[string]any, n)
	for _, p := range props[:n] {
		if v, ok := m.Email[p]; ok {
			obj[p] = v
		}
	}
	return obj
}
