package jmapserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// emailFilter is a parsed RFC 8621 Email/query filter restricted to the
// conditions push supports (emailPushFilterConditions).
type emailFilter struct {
	operator   string // "AND", "OR", "NOT"; empty for a condition
	conditions []*emailFilter

	inMailbox          *string
	inMailboxOtherThan []string
	hasKeyword         *string
	notKeyword         *string
	from, to, cc, bcc  *string
	subject            *string
}

// parseEmailFilter parses and validates a filter; null matches every message.
func parseEmailFilter(raw json.RawMessage) (*emailFilter, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	return parseFilterNode(raw, 1)
}

func parseFilterNode(raw json.RawMessage, depth int) (*emailFilter, error) {
	if depth > maxFilterDepth {
		return nil, fmt.Errorf("the filter is nested deeper than %d", maxFilterDepth)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, errors.New("a filter must be an object")
	}
	if op, ok := obj["operator"]; ok {
		f := &emailFilter{}
		if err := json.Unmarshal(op, &f.operator); err != nil || !slices.Contains([]string{"AND", "OR", "NOT"}, f.operator) {
			return nil, errors.New("operator must be AND, OR or NOT")
		}
		var conds []json.RawMessage
		if err := json.Unmarshal(obj["conditions"], &conds); err != nil || len(conds) == 0 {
			return nil, errors.New("conditions must be a non-empty array")
		}
		for k := range obj {
			if k != "operator" && k != "conditions" {
				return nil, fmt.Errorf("unknown filter operator property %q", k)
			}
		}
		for _, c := range conds {
			child, err := parseFilterNode(c, depth+1)
			if err != nil {
				return nil, err
			}
			f.conditions = append(f.conditions, child)
		}
		return f, nil
	}

	f := &emailFilter{}
	str := func(dst **string, v json.RawMessage, name string) error {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return fmt.Errorf("%s must be a string", name)
		}
		*dst = &s
		return nil
	}
	for k, v := range obj {
		var err error
		switch k {
		case "inMailbox":
			err = str(&f.inMailbox, v, k)
		case "inMailboxOtherThan":
			if json.Unmarshal(v, &f.inMailboxOtherThan) != nil {
				err = errors.New("inMailboxOtherThan must be an array of ids")
			}
		case "hasKeyword":
			err = str(&f.hasKeyword, v, k)
		case "notKeyword":
			err = str(&f.notKeyword, v, k)
		case "from":
			err = str(&f.from, v, k)
		case "to":
			err = str(&f.to, v, k)
		case "cc":
			err = str(&f.cc, v, k)
		case "bcc":
			err = str(&f.bcc, v, k)
		case "subject":
			err = str(&f.subject, v, k)
		default:
			err = fmt.Errorf("unsupported filter condition %q", k)
		}
		if err != nil {
			return nil, err
		}
	}
	return f, nil
}

// match reports whether the message satisfies the filter; a nil filter matches all.
func (f *emailFilter) match(m *NewMail) bool {
	if f == nil {
		return true
	}
	switch f.operator {
	case "AND":
		for _, c := range f.conditions {
			if !c.match(m) {
				return false
			}
		}
		return true
	case "OR":
		return slices.ContainsFunc(f.conditions, func(c *emailFilter) bool { return c.match(m) })
	case "NOT":
		return !slices.ContainsFunc(f.conditions, func(c *emailFilter) bool { return c.match(m) })
	}
	// A condition: every given property must match (RFC 8621 §4.4.1).
	if f.inMailbox != nil && !slices.Contains(m.MailboxIDs, *f.inMailbox) {
		return false
	}
	if f.inMailboxOtherThan != nil && !slices.ContainsFunc(m.MailboxIDs, func(id string) bool {
		return !slices.Contains(f.inMailboxOtherThan, id)
	}) {
		return false
	}
	if f.hasKeyword != nil && !hasKeyword(m.Keywords, *f.hasKeyword) {
		return false
	}
	if f.notKeyword != nil && hasKeyword(m.Keywords, *f.notKeyword) {
		return false
	}
	for _, c := range []struct {
		want *string
		in   []EmailAddress
	}{{f.from, m.From}, {f.to, m.To}, {f.cc, m.Cc}, {f.bcc, m.Bcc}} {
		if c.want != nil && !addressesContain(c.in, *c.want) {
			return false
		}
	}
	if f.subject != nil && !containsFold(m.Subject, *f.subject) {
		return false
	}
	return true
}

// hasKeyword compares keywords case-insensitively, as RFC 8621 keywords are.
func hasKeyword(keywords []string, k string) bool {
	return slices.ContainsFunc(keywords, func(x string) bool { return strings.EqualFold(x, k) })
}

// addressesContain looks for text in the addresses' "name <email>" form, case-insensitively.
func addressesContain(list []EmailAddress, text string) bool {
	return slices.ContainsFunc(list, func(a EmailAddress) bool {
		return containsFold(a.Name+" <"+a.Email+">", text)
	})
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(strings.TrimSpace(sub)))
}
