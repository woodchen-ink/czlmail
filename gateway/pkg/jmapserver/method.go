package jmapserver

import (
	"context"
	"encoding/json"
	"maps"
)

// MethodFunc handles one method call. It returns the response arguments, or an
// error: a *MethodError becomes an "error" response, anything else is logged and
// answered with serverFail.
type MethodFunc func(ctx context.Context, call *Call) (any, error)

type method struct {
	capability string
	fn         MethodFunc
}

// Register adds a method. The method is only callable when the request's
// `using` contains capability; otherwise the client gets unknownMethod.
// Register before serving.
func (s *Server) Register(name, capability string, fn MethodFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.methods[name] = method{capability: capability, fn: fn}
}

// Call is one method invocation with its references already resolved.
type Call struct {
	UserID string
	Name   string
	CallID string
	// Args are the resolved arguments as a JSON object.
	Args json.RawMessage
	// CreatedIDs is the request's creation id → server id map, shared by all
	// calls of the request. /set methods add the ids they create.
	CreatedIDs map[string]string

	capability string
	session    *session
	extra      []invocation
}

// Decode unmarshals the arguments; a failure is invalidArguments.
func (c *Call) Decode(v any) error {
	if err := json.Unmarshal(c.Args, v); err != nil {
		return InvalidArguments(err.Error())
	}
	return nil
}

// Account returns the account named by the call, checking that the user has it
// and that it supports the method's capability.
func (c *Call) Account(accountID string) (*Account, error) {
	a, ok := c.session.Accounts[accountID]
	if !ok {
		return nil, ErrAccountNotFound
	}
	if c.capability != CoreCapability && !a.HasCapability(c.capability) {
		return nil, ErrAccountNotSupportedByMethod
	}
	return a, nil
}

// Session returns the user's session as loaded for this request.
func (c *Call) Session() *UserSession { return c.session.UserSession }

// AddResponse appends an implicit response (such as the Email/set issued by
// EmailSubmission/set onSuccessUpdateEmail); it is sent after this call's own
// response, with the same call id.
func (c *Call) AddResponse(name string, args any) {
	c.extra = append(c.extra, invocation{name: name, args: args, callID: c.CallID})
}

// MethodError is a method-level error (RFC 8620 §3.6.2). Extra members are
// marshalled next to type and description.
type MethodError struct {
	Type        string
	Description string
	Extra       map[string]any
}

func (e *MethodError) Error() string {
	if e.Description != "" {
		return e.Type + ": " + e.Description
	}
	return e.Type
}

// MarshalJSON flattens Extra into the error object.
func (e *MethodError) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, len(e.Extra)+2)
	maps.Copy(m, e.Extra)
	m["type"] = e.Type
	if e.Description != "" {
		m["description"] = e.Description
	}
	return json.Marshal(m)
}

// Standard method errors.
var (
	ErrUnknownMethod               = &MethodError{Type: "unknownMethod"}
	ErrAccountNotFound             = &MethodError{Type: "accountNotFound"}
	ErrAccountNotSupportedByMethod = &MethodError{Type: "accountNotSupportedByMethod"}
	ErrAccountReadOnly             = &MethodError{Type: "accountReadOnly"}
	ErrForbidden                   = &MethodError{Type: "forbidden"}
	ErrServerFail                  = &MethodError{Type: "serverFail"}
	ErrCannotCalculateChanges      = &MethodError{Type: "cannotCalculateChanges"}
	ErrStateMismatch               = &MethodError{Type: "stateMismatch"}
	ErrAnchorNotFound              = &MethodError{Type: "anchorNotFound"}
	ErrUnsupportedSort             = &MethodError{Type: "unsupportedSort"}
	ErrUnsupportedFilter           = &MethodError{Type: "unsupportedFilter"}
	ErrRequestTooLarge             = &MethodError{Type: "requestTooLarge"}
	ErrTooManyChanges              = &MethodError{Type: "tooManyChanges"}
)

// InvalidArguments is the invalidArguments error with a description.
func InvalidArguments(description string) *MethodError {
	return &MethodError{Type: "invalidArguments", Description: description}
}

func invalidResultReference(description string) *MethodError {
	return &MethodError{Type: "invalidResultReference", Description: description}
}

// ServerUnavailable is serverUnavailable for a call that needs an upstream that
// cannot be reached; czlUpstream tells the client which state it is in.
func ServerUnavailable(u *UpstreamError) *MethodError {
	return &MethodError{Type: "serverUnavailable", Extra: map[string]any{"czlUpstream": u}}
}

func echo(_ context.Context, call *Call) (any, error) {
	return call.Args, nil
}
