package jmapserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type invocation struct {
	name   string
	args   any
	callID string
}

func (i invocation) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{i.name, i.args, i.callID})
}

type apiRequest struct {
	using      []string
	calls      []rawCall
	createdIDs map[string]string
}

type rawCall struct {
	name   string
	args   map[string]json.RawMessage
	callID string
}

type apiResponse struct {
	MethodResponses []invocation      `json:"methodResponses"`
	CreatedIDs      map[string]string `json:"createdIds,omitempty"`
	SessionState    string            `json:"sessionState"`
}

// API serves POST /jmap/api.
func (s *Server) API(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if !s.reqs.acquire(userID) {
		writeProblem(w, problemLimit("maxConcurrentRequests"))
		return
	}
	defer s.reqs.release(userID)

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxSizeRequest))
	if err != nil {
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			writeProblem(w, problemLimit("maxSizeRequest"))
			return
		}
		return // the client went away
	}
	req, p := parseRequest(body)
	if p != nil {
		writeProblem(w, *p)
		return
	}
	if p := s.checkUsing(req.using); p != nil {
		writeProblem(w, *p)
		return
	}
	sess, err := s.loadSession(r.Context(), userID)
	if err != nil {
		s.log.Error("load session", "trace_id", s.traceID(r.Context()), "user", userID, "err", err)
		writeProblem(w, problemInternal())
		return
	}

	resp := s.run(r.Context(), userID, sess, req)
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	writeJSON(w, http.StatusOK, resp)
}

func parseRequest(body []byte) (*apiRequest, *Problem) {
	if !json.Valid(body) {
		p := problemNotJSON()
		return nil, &p
	}
	var raw struct {
		Using       *[]string          `json:"using"`
		MethodCalls *[]json.RawMessage `json:"methodCalls"`
		CreatedIDs  map[string]string  `json:"createdIds"`
	}
	notRequest := func(detail string) (*apiRequest, *Problem) {
		p := problemNotRequest(detail)
		return nil, &p
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return notRequest("The request does not match the Request object.")
	}
	if raw.Using == nil || raw.MethodCalls == nil {
		return notRequest("using and methodCalls are required.")
	}
	if len(*raw.MethodCalls) > MaxCallsInRequest {
		p := problemLimit("maxCallsInRequest")
		return nil, &p
	}
	req := &apiRequest{using: *raw.Using, createdIDs: raw.CreatedIDs}
	for i, c := range *raw.MethodCalls {
		call, err := parseCall(c)
		if err != nil {
			return notRequest(fmt.Sprintf("methodCalls[%d]: %v", i, err))
		}
		req.calls = append(req.calls, call)
	}
	return req, nil
}

func parseCall(data json.RawMessage) (rawCall, error) {
	var parts []json.RawMessage
	if err := json.Unmarshal(data, &parts); err != nil || len(parts) != 3 {
		return rawCall{}, errors.New("an invocation is [name, arguments, callId]")
	}
	var c rawCall
	if err := json.Unmarshal(parts[0], &c.name); err != nil {
		return rawCall{}, errors.New("the method name must be a string")
	}
	if !bytes.HasPrefix(bytes.TrimSpace(parts[1]), []byte("{")) {
		return rawCall{}, errors.New("the arguments must be an object")
	}
	if err := json.Unmarshal(parts[1], &c.args); err != nil {
		return rawCall{}, errors.New("the arguments must be an object")
	}
	if err := json.Unmarshal(parts[2], &c.callID); err != nil {
		return rawCall{}, errors.New("the call id must be a string")
	}
	return c, nil
}

func (s *Server) checkUsing(using []string) *Problem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, uri := range using {
		if _, ok := s.caps[uri]; !ok {
			p := problemUnknownCapability(uri)
			return &p
		}
	}
	return nil
}

// run processes the calls in order (RFC 8620 §3.3).
func (s *Server) run(ctx context.Context, userID string, sess *session, req *apiRequest) apiResponse {
	using := make(map[string]bool, len(req.using))
	for _, u := range req.using {
		using[u] = true
	}
	created := req.createdIDs
	if created == nil {
		created = map[string]string{}
	}
	var out []invocation
	var done []resolvedResponse
	for _, rc := range req.calls {
		for _, inv := range s.invoke(ctx, userID, sess, using, created, rc, done) {
			out = append(out, inv)
			done = append(done, resolvedResponse{inv: inv})
		}
	}
	resp := apiResponse{MethodResponses: out, SessionState: sess.state}
	if out == nil {
		resp.MethodResponses = []invocation{}
	}
	if req.createdIDs != nil {
		resp.CreatedIDs = created
	}
	return resp
}

func (s *Server) invoke(ctx context.Context, userID string, sess *session, using map[string]bool,
	created map[string]string, rc rawCall, done []resolvedResponse) []invocation {
	fail := func(err *MethodError) []invocation {
		return []invocation{{name: "error", args: err, callID: rc.callID}}
	}
	s.mu.RLock()
	m, ok := s.methods[rc.name]
	s.mu.RUnlock()
	if !ok || !using[m.capability] {
		return fail(ErrUnknownMethod)
	}
	args, merr := resolveReferences(rc.args, done)
	if merr != nil {
		return fail(merr)
	}
	call := &Call{
		UserID: userID, Name: rc.name, CallID: rc.callID, Args: args, CreatedIDs: created,
		capability: m.capability, session: sess,
	}
	result, err := m.fn(ctx, call)
	if err != nil {
		if me, ok := errors.AsType[*MethodError](err); ok {
			return fail(me)
		}
		s.log.Error("method failed", "trace_id", s.traceID(ctx), "method", rc.name, "user", userID, "err", err)
		return fail(ErrServerFail)
	}
	return append([]invocation{{name: rc.name, args: result, callID: rc.callID}}, call.extra...)
}

// isReferenceKey reports whether an argument name is a result reference ("#ids").
func isReferenceKey(k string) bool { return strings.HasPrefix(k, "#") && len(k) > 1 }
