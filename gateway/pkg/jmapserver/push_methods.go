package jmapserver

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// PushSubscription/get and /set (RFC 8620 §7.2.1, §7.2.2) with the emailpush
// extension properties. Subscriptions belong to the user, not to an account, so
// neither method takes an accountId or returns a state.

// SetError is an RFC 8620 §5.3 SetError.
type SetError struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Properties  []string `json:"properties,omitempty"`
}

func invalidProps(description string, props ...string) *SetError {
	return &SetError{Type: "invalidProperties", Description: description, Properties: props}
}

var errSetNotFound = &SetError{Type: "notFound"}

// pushProperties are the properties PushSubscription/get can return; url and
// keys are write-only, so another client of the same user cannot read them.
var pushProperties = []string{"id", "deviceClientId", "verificationCode", "expires", "types", "emailPush", "emailCount"}

func (p *pusher) get(ctx context.Context, call *Call) (any, error) {
	var args struct {
		IDs        *[]string `json:"ids"`
		Properties *[]string `json:"properties"`
	}
	if err := call.Decode(&args); err != nil {
		return nil, err
	}
	if args.IDs != nil && len(*args.IDs) > MaxObjectsInGet {
		return nil, ErrRequestTooLarge
	}
	props := pushProperties
	if args.Properties != nil {
		for _, name := range *args.Properties {
			if !slices.Contains(pushProperties, name) && name != "url" && name != "keys" {
				return nil, InvalidArguments("unknown property " + name)
			}
		}
		props = append([]string{"id"}, *args.Properties...)
	}
	subs, err := p.live(ctx, call.UserID)
	if err != nil {
		return nil, err
	}

	list := []map[string]any{}
	notFound := []string{}
	if args.IDs == nil {
		for _, sub := range subs {
			list = append(list, p.render(call, sub, props))
		}
	} else {
		for _, id := range *args.IDs {
			i := slices.IndexFunc(subs, func(s *PushSubscription) bool { return s.ID == id })
			if i < 0 {
				notFound = append(notFound, id)
				continue
			}
			list = append(list, p.render(call, subs[i], props))
		}
	}
	return map[string]any{"list": list, "notFound": notFound}, nil
}

// live returns the user's unexpired subscriptions; expired ones count as gone.
func (p *pusher) live(ctx context.Context, userID string) ([]*PushSubscription, error) {
	subs, err := p.store.ListPush(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := p.now()
	return slices.DeleteFunc(subs, func(s *PushSubscription) bool { return !s.Expires.After(now) }), nil
}

func (p *pusher) render(call *Call, sub *PushSubscription, props []string) map[string]any {
	obj := map[string]any{}
	for _, name := range props {
		switch name {
		case "id":
			obj["id"] = sub.ID
		case "deviceClientId":
			obj["deviceClientId"] = sub.DeviceClientID
		case "verificationCode":
			if sub.Verified {
				obj["verificationCode"] = sub.VerificationCode
			} else {
				obj["verificationCode"] = nil
			}
		case "expires":
			obj["expires"] = utcDate(sub.Expires)
		case "types":
			obj["types"] = sub.Types
		case "keys":
			obj["keys"] = nil
		case "emailPush":
			if call.Using(EmailPushCapability) {
				obj["emailPush"] = sub.EmailPush
			}
		case "emailCount":
			if call.Using(EmailPushCapability) {
				obj["emailCount"] = nil
			}
		}
	}
	return obj
}

func utcDate(t time.Time) string { return t.UTC().Truncate(time.Second).Format(time.RFC3339) }

type setArgs struct {
	Create  map[string]map[string]json.RawMessage `json:"create"`
	Update  map[string]map[string]json.RawMessage `json:"update"`
	Destroy []string                              `json:"destroy"`
}

type setResponse struct {
	Created      map[string]any       `json:"created"`
	Updated      map[string]any       `json:"updated"`
	Destroyed    []string             `json:"destroyed"`
	NotCreated   map[string]*SetError `json:"notCreated"`
	NotUpdated   map[string]*SetError `json:"notUpdated"`
	NotDestroyed map[string]*SetError `json:"notDestroyed"`
}

func (p *pusher) set(ctx context.Context, call *Call) (any, error) {
	var args setArgs
	if err := call.Decode(&args); err != nil {
		return nil, err
	}
	if len(args.Create)+len(args.Update)+len(args.Destroy) > MaxObjectsInSet {
		return nil, ErrRequestTooLarge
	}
	subs, err := p.live(ctx, call.UserID)
	if err != nil {
		return nil, err
	}
	resp := setResponse{}
	find := func(id string) *PushSubscription {
		if i := slices.IndexFunc(subs, func(s *PushSubscription) bool { return s.ID == id }); i >= 0 {
			return subs[i]
		}
		return nil
	}

	for _, cid := range sortedKeys(args.Create) {
		if len(subs) >= MaxPushSubscriptions {
			setErr(&resp.NotCreated, cid, &SetError{Type: "overQuota",
				Description: fmt.Sprintf("at most %d push subscriptions per user", MaxPushSubscriptions)})
			continue
		}
		sub, serr := p.newSubscription(call, args.Create[cid])
		if serr != nil {
			setErr(&resp.NotCreated, cid, serr)
			continue
		}
		if err := p.store.CreatePush(ctx, sub); err != nil {
			return nil, err
		}
		p.addUser(call.UserID)
		subs = append(subs, sub)
		call.CreatedIDs[cid] = sub.ID
		if resp.Created == nil {
			resp.Created = map[string]any{}
		}
		resp.Created[cid] = map[string]any{"id": sub.ID, "expires": utcDate(sub.Expires)}
		p.verify(sub)
	}

	for _, id := range sortedKeys(args.Update) {
		sub := find(id)
		if sub == nil {
			setErr(&resp.NotUpdated, id, errSetNotFound)
			continue
		}
		next, changed, serr := p.patch(call, sub, args.Update[id])
		if serr != nil {
			setErr(&resp.NotUpdated, id, serr)
			continue
		}
		if err := p.store.UpdatePush(ctx, next); err != nil {
			return nil, err
		}
		*sub = *next
		if resp.Updated == nil {
			resp.Updated = map[string]any{}
		}
		resp.Updated[id] = changed
	}

	for _, id := range args.Destroy {
		if find(id) == nil {
			setErr(&resp.NotDestroyed, id, errSetNotFound)
			continue
		}
		if err := p.store.DeletePush(ctx, call.UserID, id); err != nil {
			return nil, err
		}
		p.forget(id)
		subs = slices.DeleteFunc(subs, func(s *PushSubscription) bool { return s.ID == id })
		resp.Destroyed = append(resp.Destroyed, id)
	}
	return resp, nil
}

func setErr(m *map[string]*SetError, id string, e *SetError) {
	if *m == nil {
		*m = map[string]*SetError{}
	}
	(*m)[id] = e
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// newSubscription validates a create object.
func (p *pusher) newSubscription(call *Call, obj map[string]json.RawMessage) (*PushSubscription, *SetError) {
	now := p.now()
	sub := &PushSubscription{UserID: call.UserID, CredentialID: call.CredentialID, CreatedAt: now.UTC()}
	var keysGiven bool
	for name, raw := range obj {
		var serr *SetError
		switch name {
		case "deviceClientId":
			if json.Unmarshal(raw, &sub.DeviceClientID) != nil || sub.DeviceClientID == "" || len(sub.DeviceClientID) > 255 {
				serr = invalidProps("deviceClientId must be a string of 1 to 255 characters", name)
			}
		case "url":
			var u string
			if json.Unmarshal(raw, &u) != nil {
				serr = invalidProps("url must be a string", name)
			} else if err := checkPushURL(u, p.allowPrivate()); err != nil {
				serr = invalidProps(err.Error(), name)
			}
			sub.URL = u
		case "keys":
			sub.P256DH, sub.Auth, serr = parsePushKeys(raw)
			keysGiven = sub.P256DH != nil
		case "verificationCode":
			if !isNull(raw) {
				serr = invalidProps("verificationCode must be null on create", name)
			}
		case "expires":
			sub.Expires, serr = p.parseExpires(raw, now)
		case "types":
			sub.Types, serr = parseTypesProp(raw)
		case "emailPush", "emailCount":
			continue // checked below, once keys are known
		default:
			serr = invalidProps("unknown or server-set property", name)
		}
		if serr != nil {
			return nil, serr
		}
	}
	if sub.DeviceClientID == "" || sub.URL == "" {
		return nil, invalidProps("deviceClientId and url are required", "deviceClientId", "url")
	}
	if sub.Expires.IsZero() {
		sub.Expires = now.Add(MaxPushTTL).UTC()
	}
	if raw, ok := obj["emailCount"]; ok && !isNull(raw) {
		return nil, invalidProps("EmailCount pushes are not supported", "emailCount")
	}
	if raw, ok := obj["emailPush"]; ok {
		cfg, serr := p.parseEmailPush(call, raw, keysGiven)
		if serr != nil {
			return nil, serr
		}
		sub.EmailPush = cfg
	}

	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(now.UnixMilli())<<16)
	_, _ = rand.Read(b[6:])
	sub.ID = "P" + idEncoding.EncodeToString(b[:])
	code := make([]byte, 16)
	_, _ = rand.Read(code)
	sub.VerificationCode = base64.RawURLEncoding.EncodeToString(code)
	return sub, nil
}

// patch applies an update and returns the new value and the server-changed
// properties for the response (nil when the update was taken as given).
func (p *pusher) patch(call *Call, cur *PushSubscription, obj map[string]json.RawMessage) (*PushSubscription, map[string]any, *SetError) {
	next := *cur
	next.EmailPush = cloneEmailPush(cur.EmailPush)
	now := p.now()
	var changed map[string]any
	for _, name := range sortedKeys(obj) {
		raw := obj[name]
		switch {
		case name == "verificationCode":
			var code string
			if json.Unmarshal(raw, &code) != nil || code != cur.VerificationCode {
				return nil, nil, invalidProps("verificationCode does not match", name)
			}
			next.Verified = true
		case name == "expires":
			var serr *SetError
			if isNull(raw) {
				next.Expires = now.Add(MaxPushTTL).UTC()
			} else if next.Expires, serr = p.parseExpires(raw, now); serr != nil {
				return nil, nil, serr
			}
			var asked string
			if json.Unmarshal(raw, &asked) != nil || asked != utcDate(next.Expires) {
				changed = map[string]any{"expires": utcDate(next.Expires)}
			}
		case name == "types":
			t, serr := parseTypesProp(raw)
			if serr != nil {
				return nil, nil, serr
			}
			next.Types = t
		case name == "emailPush":
			cfg, serr := p.parseEmailPush(call, raw, cur.P256DH != nil)
			if serr != nil {
				return nil, nil, serr
			}
			next.EmailPush = cfg
		case strings.HasPrefix(name, "emailPush/"):
			accountID := strings.TrimPrefix(name, "emailPush/")
			if isNull(raw) {
				delete(next.EmailPush, accountID)
				if len(next.EmailPush) == 0 {
					next.EmailPush = nil
				}
				continue
			}
			one, serr := p.parseEmailPush(call, json.RawMessage(`{"`+jsonEscape(accountID)+`":`+string(raw)+`}`), cur.P256DH != nil)
			if serr != nil {
				return nil, nil, serr
			}
			if next.EmailPush == nil {
				next.EmailPush = map[string]EmailPushConfig{}
			}
			next.EmailPush[accountID] = one[accountID]
		case name == "emailCount":
			if !isNull(raw) {
				return nil, nil, invalidProps("EmailCount pushes are not supported", name)
			}
		case name == "deviceClientId" || name == "url" || name == "keys" || name == "id":
			return nil, nil, invalidProps(name+" cannot be changed", name)
		default:
			return nil, nil, invalidProps("unknown property", name)
		}
	}
	return &next, changed, nil
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func cloneEmailPush(m map[string]EmailPushConfig) map[string]EmailPushConfig {
	if m == nil {
		return nil
	}
	out := make(map[string]EmailPushConfig, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

// parseExpires reads a UTCDate and caps it at MaxPushTTL from now.
func (p *pusher) parseExpires(raw json.RawMessage, now time.Time) (time.Time, *SetError) {
	if isNull(raw) {
		return now.Add(MaxPushTTL).UTC(), nil
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return time.Time{}, invalidProps("expires must be a UTCDate", "expires")
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, invalidProps("expires must be a UTCDate", "expires")
	}
	if !t.After(now) {
		return time.Time{}, invalidProps("expires is in the past", "expires")
	}
	if limit := now.Add(MaxPushTTL); t.After(limit) {
		t = limit
	}
	return t.UTC(), nil
}

func parseTypesProp(raw json.RawMessage) ([]string, *SetError) {
	if isNull(raw) {
		return nil, nil
	}
	var types []string
	if json.Unmarshal(raw, &types) != nil || slices.Contains(types, "") {
		return nil, invalidProps("types must be null or an array of type names", "types")
	}
	if types == nil {
		types = []string{}
	}
	return types, nil
}

// parsePushKeys reads keys: null or {p256dh, auth}, both base64url.
func parsePushKeys(raw json.RawMessage) ([]byte, []byte, *SetError) {
	if isNull(raw) {
		return nil, nil, nil
	}
	var k struct {
		P256DH string `json:"p256dh"`
		Auth   string `json:"auth"`
	}
	bad := invalidProps("keys must have a P-256 p256dh and a 16-byte auth, base64url encoded", "keys")
	if json.Unmarshal(raw, &k) != nil {
		return nil, nil, bad
	}
	pub, err1 := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.P256DH, "="))
	auth, err2 := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.Auth, "="))
	if err1 != nil || err2 != nil || len(auth) != 16 {
		return nil, nil, bad
	}
	if _, err := ecdh.P256().NewPublicKey(pub); err != nil {
		return nil, nil, bad
	}
	return pub, auth, nil
}

// parseEmailPush validates an emailPush value (draft-ietf-jmap-emailpush §3).
func (p *pusher) parseEmailPush(call *Call, raw json.RawMessage, hasKeys bool) (map[string]EmailPushConfig, *SetError) {
	if isNull(raw) {
		return nil, nil
	}
	if !call.Using(EmailPushCapability) {
		return nil, invalidProps("emailPush needs "+EmailPushCapability+" in using", "emailPush")
	}
	if !hasKeys {
		return nil, invalidProps("emailPush needs encryption keys", "emailPush", "keys")
	}
	var byAccount map[string]json.RawMessage
	if json.Unmarshal(raw, &byAccount) != nil {
		return nil, invalidProps("emailPush must be an object keyed by accountId", "emailPush")
	}
	out := make(map[string]EmailPushConfig, len(byAccount))
	for accountID, rawCfg := range byAccount {
		path := "emailPush/" + accountID
		acc, ok := call.Session().Accounts[accountID]
		if !ok || !acc.HasCapability(EmailPushCapability) {
			return nil, invalidProps("unknown account or account without emailpush", "emailPush", path)
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(rawCfg, &fields) != nil || fields == nil {
			return nil, invalidProps("an EmailPushConfig must be an object", "emailPush", path)
		}
		cfg := EmailPushConfig{Properties: []string{}, Urgency: "normal"}
		for name, v := range fields {
			switch name {
			case "filter":
				if _, err := parseEmailFilter(v); err != nil {
					return nil, invalidProps(err.Error(), "emailPush", path+"/filter")
				}
				if !isNull(v) {
					cfg.Filter = append(json.RawMessage(nil), v...)
				}
			case "properties":
				if json.Unmarshal(v, &cfg.Properties) != nil || cfg.Properties == nil ||
					slices.ContainsFunc(cfg.Properties, func(s string) bool { return !slices.Contains(EmailPushProperties, s) }) {
					return nil, invalidProps("properties must list supported Email properties", "emailPush", path+"/properties")
				}
			case "urgency":
				if json.Unmarshal(v, &cfg.Urgency) != nil || !slices.Contains([]string{"very-low", "low", "normal", "high"}, cfg.Urgency) {
					return nil, invalidProps("urgency must be very-low, low, normal or high", "emailPush", path+"/urgency")
				}
			default:
				return nil, invalidProps("unknown EmailPushConfig property", "emailPush", path+"/"+name)
			}
		}
		out[accountID] = cfg
	}
	return out, nil
}
