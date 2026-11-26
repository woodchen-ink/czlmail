package mailindex

import (
	"encoding/json"
	"slices"
	"strings"
)

// Keywords is a JMAP keyword set; keys are lower-case.
type Keywords map[string]bool

// JSON encodes the set with sorted keys (stable, so equal sets compare equal).
func (k Keywords) JSON() string {
	keys := make([]string, 0, len(k))
	for kw, on := range k {
		if on {
			keys = append(keys, kw)
		}
	}
	slices.Sort(keys)
	m := make(map[string]bool, len(keys))
	for _, kw := range keys {
		m[kw] = true
	}
	b, _ := json.Marshal(m) // map keys are marshalled sorted
	return string(b)
}

// ParseKeywords decodes the stored JSON.
func ParseKeywords(s string) Keywords {
	k := Keywords{}
	_ = json.Unmarshal([]byte(s), &k)
	return k
}

var systemFlags = map[string]string{
	`\seen`:     "$seen",
	`\flagged`:  "$flagged",
	`\answered`: "$answered",
	`\draft`:    "$draft",
}

// serverKeywords are set by servers on their own (Dovecot's attachment detection)
// and would show up as spurious keyword changes; they are not exposed.
var serverKeywords = map[string]bool{"$hasattachment": true, "$hasnoattachment": true}

// FlagsToKeywords maps IMAP flags to JMAP keywords (RFC 8621 §4.1.1): the four
// system flags become $seen, $flagged, $answered, $draft; \Deleted and \Recent
// are dropped; other keywords are lower-cased.
func FlagsToKeywords(flags []string) Keywords {
	k := Keywords{}
	for _, f := range flags {
		lf := strings.ToLower(f)
		if kw, ok := systemFlags[lf]; ok {
			k[kw] = true
			continue
		}
		if strings.HasPrefix(lf, `\`) || serverKeywords[lf] {
			continue
		}
		k[lf] = true
	}
	return k
}

// KeywordToFlag maps one JMAP keyword to its IMAP flag.
func KeywordToFlag(kw string) string {
	switch strings.ToLower(kw) {
	case "$seen":
		return `\Seen`
	case "$flagged":
		return `\Flagged`
	case "$answered":
		return `\Answered`
	case "$draft":
		return `\Draft`
	}
	return kw
}

// Address is one address of an email header, as JSON in the index.
type Address struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// EncodeJSON marshals v for a JSON column; nil slices become "[]".
func EncodeJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return "[]"
	}
	return string(b)
}

// DecodeAddresses reads an address column.
func DecodeAddresses(s string) []Address {
	var out []Address
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// DecodeStrings reads a JSON string-list column (in_reply_to, refs).
func DecodeStrings(s string) []string {
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}
