package mail

import (
	"errors"
	"fmt"
	"slices"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

// SetError is a per-object /set failure (RFC 8620 §5.3).
type SetError struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Properties  []string `json:"properties,omitempty"`
}

func setErr(typ, desc string, props ...string) *SetError {
	return &SetError{Type: typ, Description: desc, Properties: props}
}

var (
	errNotFound = &SetError{Type: "notFound"}
	errForbid   = &SetError{Type: "forbidden"}
)

func invalidProps(desc string, props ...string) *SetError {
	return &SetError{Type: "invalidProperties", Description: desc, Properties: props}
}

// getArgs are the standard /get arguments.
type getArgs struct {
	AccountID  string    `json:"accountId"`
	IDs        *[]string `json:"ids"`
	Properties *[]string `json:"properties"`
}

// changesArgs are the standard /changes arguments.
type changesArgs struct {
	AccountID  string `json:"accountId"`
	SinceState string `json:"sinceState"`
	MaxChanges *int   `json:"maxChanges"`
}

type changesResponse struct {
	AccountID      string   `json:"accountId"`
	OldState       string   `json:"oldState"`
	NewState       string   `json:"newState"`
	HasMoreChanges bool     `json:"hasMoreChanges"`
	Created        []string `json:"created"`
	Updated        []string `json:"updated"`
	Destroyed      []string `json:"destroyed"`
}

// maxChangesDefault caps a /changes answer when the client sets no maxChanges.
const maxChangesDefault = 5000

// changes runs a /changes call on the index.
func (s *Service) changes(call *jmapserver.Call, typ string) (*account, *mailindex.Changes, error) {
	var args changesArgs
	if err := call.Decode(&args); err != nil {
		return nil, nil, err
	}
	if args.MaxChanges != nil && *args.MaxChanges <= 0 {
		return nil, nil, jmapserver.InvalidArguments("maxChanges must be positive")
	}
	a, err := s.open(call, args.AccountID)
	if err != nil {
		return nil, nil, err
	}
	limit := maxChangesDefault
	if args.MaxChanges != nil {
		limit = min(*args.MaxChanges, maxChangesDefault)
	}
	ch, err := a.idx.Changes(typ, args.SinceState, limit)
	switch {
	case errors.Is(err, mailindex.ErrCannotCalculateChanges):
		return nil, nil, jmapserver.ErrCannotCalculateChanges
	case errors.Is(err, mailindex.ErrNotReady):
		return nil, nil, jmapserver.ServerUnavailable(&jmapserver.UpstreamError{Status: jmapserver.UpstreamSyncing, Code: jmapserver.CodeUpstreamSyncing})
	case err != nil:
		return nil, nil, err
	}
	return a, ch, nil
}

func toChangesResponse(accountID string, ch *mailindex.Changes) changesResponse {
	return changesResponse{AccountID: accountID, OldState: ch.OldState, NewState: ch.NewState, HasMoreChanges: ch.HasMoreChanges,
		Created: ch.Created, Updated: ch.Updated, Destroyed: ch.Destroyed}
}

// checkIDs enforces maxObjectsInGet on an explicit id list.
func checkIDs(ids *[]string) error {
	if ids != nil && len(*ids) > jmapserver.MaxObjectsInGet {
		return jmapserver.ErrRequestTooLarge
	}
	return nil
}

// pickProperties validates a properties argument against the known ones; the
// id is always returned.
func pickProperties(req *[]string, all, defaults []string) (map[string]bool, error) {
	list := defaults
	if req != nil {
		list = *req
	}
	out := make(map[string]bool, len(list)+1)
	for _, p := range list {
		if !slices.Contains(all, p) {
			return nil, jmapserver.InvalidArguments(fmt.Sprintf("unknown property %q", p))
		}
		out[p] = true
	}
	out["id"] = true
	return out, nil
}

// resolveID maps a "#creationId" reference to the id created earlier in the request.
func resolveID(call *jmapserver.Call, id string) (string, bool) {
	if len(id) > 1 && id[0] == '#' {
		real, ok := call.CreatedIDs[id[1:]]
		return real, ok
	}
	return id, true
}

// checkState enforces ifInState on a /set call.
func checkState(ifInState *string, current string) error {
	if ifInState != nil && *ifInState != current {
		return jmapserver.ErrStateMismatch
	}
	return nil
}

// dedupe drops repeated ids, keeping the first occurrence.
func dedupe(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := ids[:0:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
