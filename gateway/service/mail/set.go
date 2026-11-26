package mail

import (
	"encoding/json"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// setArgs are the standard /set arguments; objects stay raw for per-type decoding.
type setArgs struct {
	AccountID string                                `json:"accountId"`
	IfInState *string                               `json:"ifInState"`
	Create    map[string]map[string]json.RawMessage `json:"create"`
	Update    map[string]map[string]json.RawMessage `json:"update"`
	Destroy   []string                              `json:"destroy"`
}

func (a *setArgs) count() int { return len(a.Create) + len(a.Update) + len(a.Destroy) }

// setResponse is a /set response; empty maps marshal as null (RFC 8620 §5.3).
type setResponse struct {
	AccountID    string               `json:"accountId"`
	OldState     string               `json:"oldState"`
	NewState     string               `json:"newState"`
	Created      map[string]any       `json:"created"`
	Updated      map[string]any       `json:"updated"`
	Destroyed    []string             `json:"destroyed"`
	NotCreated   map[string]*SetError `json:"notCreated"`
	NotUpdated   map[string]*SetError `json:"notUpdated"`
	NotDestroyed map[string]*SetError `json:"notDestroyed"`

	// wrote is true once one change reached the upstream: an outage after that
	// is reported per object, before it the whole call is serverUnavailable.
	wrote bool
}

func newSetResponse(accountID, state string) *setResponse {
	return &setResponse{AccountID: accountID, OldState: state, NewState: state}
}

func (r *setResponse) created(cid string, v any) {
	if r.Created == nil {
		r.Created = map[string]any{}
	}
	r.Created[cid] = v
	r.wrote = true
}

func (r *setResponse) updated(id string, v any) {
	if r.Updated == nil {
		r.Updated = map[string]any{}
	}
	r.Updated[id] = v
	r.wrote = true
}

func (r *setResponse) destroyed(id string) {
	r.Destroyed = append(r.Destroyed, id)
	r.wrote = true
}

func (r *setResponse) notCreated(cid string, e *SetError) {
	if r.NotCreated == nil {
		r.NotCreated = map[string]*SetError{}
	}
	r.NotCreated[cid] = e
}

func (r *setResponse) notUpdated(id string, e *SetError) {
	if r.NotUpdated == nil {
		r.NotUpdated = map[string]*SetError{}
	}
	r.NotUpdated[id] = e
}

func (r *setResponse) notDestroyed(id string, e *SetError) {
	if r.NotDestroyed == nil {
		r.NotDestroyed = map[string]*SetError{}
	}
	r.NotDestroyed[id] = e
}

// upstreamFailure handles an error from an upstream write. It returns the
// SetError for the object, or a method error that fails the whole call (an
// outage before anything was written, or an internal error).
func (r *setResponse) upstreamFailure(err error, rejected *SetError) (*SetError, error) {
	if isNotFound(err) {
		return errNotFound, nil
	}
	if me, ok := unavailable(err); ok {
		if !r.wrote {
			return nil, me
		}
		return &SetError{Type: "serverFail", Description: "The mailbox server is unavailable."}, nil
	}
	if upstream.IsKind(err, upstream.KindRejected) {
		return rejected, nil
	}
	return nil, err
}

// decodeProp decodes one property value; a failure names the property.
func decodeProp(raw json.RawMessage, v any, name string) *SetError {
	if err := json.Unmarshal(raw, v); err != nil {
		return invalidProps("invalid value", name)
	}
	return nil
}

func tooLarge(args *setArgs) error {
	if args.count() > jmapserver.MaxObjectsInSet {
		return jmapserver.ErrRequestTooLarge
	}
	return nil
}
