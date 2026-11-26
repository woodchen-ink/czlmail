package submission

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

type getArgs struct {
	AccountID  string    `json:"accountId"`
	IDs        *[]string `json:"ids"`
	Properties *[]string `json:"properties"`
}

type changesArgs struct {
	AccountID  string `json:"accountId"`
	SinceState string `json:"sinceState"`
	MaxChanges *int   `json:"maxChanges"`
}

type setArgs struct {
	AccountID string                                `json:"accountId"`
	IfInState *string                               `json:"ifInState"`
	Create    map[string]json.RawMessage            `json:"create"`
	Update    map[string]map[string]json.RawMessage `json:"update"`
	Destroy   []string                              `json:"destroy"`
	// EmailSubmission/set only.
	OnSuccessUpdateEmail  map[string]map[string]json.RawMessage `json:"onSuccessUpdateEmail"`
	OnSuccessDestroyEmail []string                              `json:"onSuccessDestroyEmail"`
}

// setResponse is a /set response; empty maps are sent as null (RFC 8620 §5.3).
type setResponse struct {
	AccountID    string              `json:"accountId"`
	OldState     *string             `json:"oldState"`
	NewState     string              `json:"newState"`
	Created      map[string]any      `json:"created"`
	Updated      map[string]any      `json:"updated"`
	Destroyed    []string            `json:"destroyed"`
	NotCreated   map[string]setError `json:"notCreated"`
	NotUpdated   map[string]setError `json:"notUpdated"`
	NotDestroyed map[string]setError `json:"notDestroyed"`
}

func (r *setResponse) create(id string, v any) {
	if r.Created == nil {
		r.Created = map[string]any{}
	}
	r.Created[id] = v
}

func (r *setResponse) update(id string, v any) {
	if r.Updated == nil {
		r.Updated = map[string]any{}
	}
	r.Updated[id] = v
}

func (r *setResponse) failCreate(id string, e setError) {
	if r.NotCreated == nil {
		r.NotCreated = map[string]setError{}
	}
	r.NotCreated[id] = e
}

func (r *setResponse) failUpdate(id string, e setError) {
	if r.NotUpdated == nil {
		r.NotUpdated = map[string]setError{}
	}
	r.NotUpdated[id] = e
}

func (r *setResponse) failDestroy(id string, e setError) {
	if r.NotDestroyed == nil {
		r.NotDestroyed = map[string]setError{}
	}
	r.NotDestroyed[id] = e
}

func checkLimit(n int) error {
	if n > jmapserver.MaxObjectsInSet {
		return jmapserver.ErrRequestTooLarge
	}
	return nil
}

// project keeps the requested properties (id always).
func project(obj map[string]any, props *[]string) map[string]any {
	if props == nil {
		return obj
	}
	out := map[string]any{"id": obj["id"]}
	for _, p := range *props {
		if v, ok := obj[p]; ok {
			out[p] = v
		}
	}
	return out
}

func validProperties(props *[]string, known map[string]bool) error {
	if props == nil {
		return nil
	}
	for _, p := range *props {
		if !known[p] {
			return jmapserver.InvalidArguments("unknown property " + p)
		}
	}
	return nil
}

func (s *Service) changesOf(call *jmapserver.Call, typ string) (any, error) {
	var a changesArgs
	if err := call.Decode(&a); err != nil {
		return nil, err
	}
	if _, err := call.Account(a.AccountID); err != nil {
		return nil, err
	}
	max := 0
	if a.MaxChanges != nil {
		if *a.MaxChanges <= 0 {
			return nil, jmapserver.InvalidArguments("maxChanges must be positive")
		}
		max = *a.MaxChanges
	}
	cs, err := s.changes.changes(a.AccountID, typ, a.SinceState, max)
	if errors.Is(err, errCannotCalculate) {
		return nil, jmapserver.ErrCannotCalculateChanges
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"accountId": a.AccountID, "oldState": a.SinceState, "newState": cs.NewState, "hasMoreChanges": cs.HasMore,
		"created": cs.Created, "updated": cs.Updated, "destroyed": cs.Destroyed,
	}, nil
}

var identityProperties = map[string]bool{
	"id": true, "name": true, "email": true, "replyTo": true, "bcc": true,
	"textSignature": true, "htmlSignature": true, "mayDelete": true,
}

func (s *Service) identityGet(_ context.Context, call *jmapserver.Call) (any, error) {
	var a getArgs
	if err := call.Decode(&a); err != nil {
		return nil, err
	}
	if _, err := call.Account(a.AccountID); err != nil {
		return nil, err
	}
	if err := validProperties(a.Properties, identityProperties); err != nil {
		return nil, err
	}
	if a.IDs != nil && len(*a.IDs) > jmapserver.MaxObjectsInGet {
		return nil, jmapserver.ErrRequestTooLarge
	}
	rows, err := s.Identities.List(a.AccountID)
	if err != nil {
		return nil, err
	}
	state, err := s.Identities.State(a.AccountID)
	if err != nil {
		return nil, err
	}
	byID := map[string]map[string]any{}
	list := []map[string]any{}
	for i := range rows {
		v := identityView(&rows[i])
		byID[rows[i].PublicID] = v
		if a.IDs == nil {
			list = append(list, project(v, a.Properties))
		}
	}
	notFound := []string{}
	if a.IDs != nil {
		for _, id := range *a.IDs {
			if v, ok := byID[id]; ok {
				list = append(list, project(v, a.Properties))
			} else {
				notFound = append(notFound, id)
			}
		}
	}
	return map[string]any{"accountId": a.AccountID, "state": state, "list": list, "notFound": notFound}, nil
}

func (s *Service) identityChanges(_ context.Context, call *jmapserver.Call) (any, error) {
	// Make sure the primary identity exists before answering.
	var a changesArgs
	if json.Unmarshal(call.Args, &a) == nil && a.AccountID != "" {
		if _, err := call.Account(a.AccountID); err == nil {
			_, _ = s.Identities.List(a.AccountID)
		}
	}
	return s.changesOf(call, TypeIdentity)
}

// identityPatch is the Identity/set create object or update patch.
type identityPatch struct {
	ID            *string    `json:"id"`
	MayDelete     *bool      `json:"mayDelete"`
	Name          *string    `json:"name"`
	Email         *string    `json:"email"`
	ReplyTo       *[]Address `json:"replyTo"`
	Bcc           *[]Address `json:"bcc"`
	TextSignature *string    `json:"textSignature"`
	HTMLSignature *string    `json:"htmlSignature"`
}

func decodeIdentityPatch(raw map[string]json.RawMessage) (*identityPatch, setError) {
	var p identityPatch
	for k, v := range raw {
		if !identityProperties[k] {
			return nil, invalidProperties("", k)
		}
		if string(v) == "null" && (k == "replyTo" || k == "bcc") {
			empty := []Address{}
			if k == "replyTo" {
				p.ReplyTo = &empty
			} else {
				p.Bcc = &empty
			}
			continue
		}
		b, _ := json.Marshal(map[string]json.RawMessage{k: v})
		if err := json.Unmarshal(b, &p); err != nil {
			return nil, invalidProperties("", k)
		}
	}
	return &p, nil
}

func (p *identityPatch) input() IdentityInput {
	return IdentityInput{Email: p.Email, Name: p.Name, ReplyTo: p.ReplyTo, Bcc: p.Bcc, TextSignature: p.TextSignature, HTMLSignature: p.HTMLSignature}
}

func identitySetError(err error, prop string) setError {
	switch {
	case errors.Is(err, ErrIdentityNotFound):
		return newSetError("notFound", "")
	case errors.Is(err, ErrIdentityNotAllowed):
		return newSetError("forbiddenFrom", "The address is not covered by this account's identities; add it in the gateway admin.")
	case errors.Is(err, ErrIdentityManaged):
		return newSetError("forbidden", "This identity is managed by the mailbox and cannot be changed this way.")
	case errors.Is(err, ErrInvalidIdentity):
		return invalidProperties("", prop)
	}
	return newSetError("serverFail", "")
}

func (s *Service) identitySet(_ context.Context, call *jmapserver.Call) (any, error) {
	var a setArgs
	if err := call.Decode(&a); err != nil {
		return nil, err
	}
	if _, err := call.Account(a.AccountID); err != nil {
		return nil, err
	}
	if err := checkLimit(len(a.Create) + len(a.Update) + len(a.Destroy)); err != nil {
		return nil, err
	}
	if _, err := s.Identities.List(a.AccountID); err != nil {
		return nil, err
	}
	old, err := s.Identities.State(a.AccountID)
	if err != nil {
		return nil, err
	}
	if a.IfInState != nil && *a.IfInState != old {
		return nil, jmapserver.ErrStateMismatch
	}
	resp := &setResponse{AccountID: a.AccountID, OldState: &old}
	for cid, raw := range a.Create {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			resp.failCreate(cid, invalidProperties("", ""))
			continue
		}
		p, serr := decodeIdentityPatch(obj)
		if serr != nil {
			resp.failCreate(cid, serr)
			continue
		}
		if p.ID != nil || p.MayDelete != nil {
			resp.failCreate(cid, invalidProperties("Server-set property.", "id", "mayDelete"))
			continue
		}
		row, err := s.Identities.Create(a.AccountID, p.input(), false)
		if err != nil {
			resp.failCreate(cid, identitySetError(err, "email"))
			continue
		}
		call.CreatedIDs[cid] = row.PublicID
		resp.create(cid, map[string]any{"id": row.PublicID, "mayDelete": row.Source == model.IdentityAlias})
	}
	for id, raw := range a.Update {
		p, serr := decodeIdentityPatch(raw)
		if serr != nil {
			resp.failUpdate(id, serr)
			continue
		}
		if p.ID != nil || p.MayDelete != nil {
			resp.failUpdate(id, invalidProperties("Server-set property.", "id", "mayDelete"))
			continue
		}
		if p.Email != nil {
			cur, err := s.Identities.Get(a.AccountID, id)
			if err != nil {
				resp.failUpdate(id, identitySetError(err, "email"))
				continue
			}
			if *p.Email != cur.Email {
				resp.failUpdate(id, invalidProperties("The email of an identity is immutable.", "email"))
				continue
			}
		}
		if _, err := s.Identities.Update(a.AccountID, id, p.input()); err != nil {
			resp.failUpdate(id, identitySetError(err, ""))
			continue
		}
		resp.update(id, nil)
	}
	for _, id := range a.Destroy {
		if err := s.Identities.Delete(a.AccountID, id); err != nil {
			resp.failDestroy(id, identitySetError(err, ""))
			continue
		}
		resp.Destroyed = append(resp.Destroyed, id)
	}
	if resp.NewState, err = s.Identities.State(a.AccountID); err != nil {
		return nil, err
	}
	return resp, nil
}

func identityView(r *model.Identity) map[string]any {
	var reply, bcc any
	if l := DecodeAddresses(r.ReplyTo); len(l) > 0 {
		reply = l
	}
	if l := DecodeAddresses(r.Bcc); len(l) > 0 {
		bcc = l
	}
	return map[string]any{
		"id": r.PublicID, "name": r.Name, "email": r.Email, "replyTo": reply, "bcc": bcc,
		"textSignature": r.TextSignature, "htmlSignature": r.HTMLSignature, "mayDelete": r.Source == model.IdentityAlias,
	}
}
