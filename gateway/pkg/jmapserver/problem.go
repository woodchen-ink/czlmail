package jmapserver

import (
	"encoding/json"
	"net/http"
)

// Request-level error types (RFC 8620 §3.6.1) and the gateway's own.
const (
	ProblemUnknownCapability = "urn:ietf:params:jmap:error:unknownCapability"
	ProblemNotJSON           = "urn:ietf:params:jmap:error:notJSON"
	ProblemNotRequest        = "urn:ietf:params:jmap:error:notRequest"
	ProblemLimit             = "urn:ietf:params:jmap:error:limit"
	ProblemNoUpstream        = "https://czl.net/jmap/gateway/no-upstream"
	ProblemUpstream          = "https://czl.net/jmap/gateway/upstream-unavailable"
	problemBlank             = "about:blank"
)

// Problem is an RFC 7807 problem details object. Besides the standard members it
// carries the gateway's numeric code and snake_case identifier.
type Problem struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Code   int    `json:"code"`
	Msg    string `json:"msg"`
	// Limit names the exceeded limit for ProblemLimit.
	Limit string `json:"limit,omitempty"`
	// AdminURL tells the user where to add a mailbox (no-upstream).
	AdminURL string `json:"adminUrl,omitempty"`
	// Upstream describes the unavailable upstream (upstream-unavailable).
	Upstream *UpstreamError `json:"czlUpstream,omitempty"`
}

func writeProblem(w http.ResponseWriter, p Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

func problemUnauthorized() Problem {
	return Problem{Type: problemBlank, Status: http.StatusUnauthorized, Code: 40100, Msg: "unauthenticated",
		Detail: "Valid credentials are required."}
}

func problemTooManyRequests() Problem {
	return Problem{Type: problemBlank, Status: http.StatusTooManyRequests, Code: 42900, Msg: "too_many_requests",
		Detail: "Too many failed sign-in attempts; retry later."}
}

func problemNoUpstream(baseURL string) Problem {
	return Problem{Type: ProblemNoUpstream, Status: http.StatusForbidden, Code: 40301, Msg: "no_upstream",
		Detail: "No mailbox is connected to this gateway user yet.", AdminURL: baseURL + "/admin/"}
}

func problemNotJSON() Problem {
	return Problem{Type: ProblemNotJSON, Status: http.StatusBadRequest, Code: 40020, Msg: "not_json",
		Detail: "The request body is not valid JSON."}
}

func problemNotRequest(detail string) Problem {
	return Problem{Type: ProblemNotRequest, Status: http.StatusBadRequest, Code: 40021, Msg: "not_request", Detail: detail}
}

func problemUnknownCapability(uri string) Problem {
	return Problem{Type: ProblemUnknownCapability, Status: http.StatusBadRequest, Code: 40022, Msg: "unknown_capability",
		Detail: "Unsupported capability: " + uri}
}

func problemLimit(limit string) Problem {
	return Problem{Type: ProblemLimit, Status: http.StatusBadRequest, Code: 40023, Msg: "limit_exceeded",
		Detail: "The request exceeds a server limit.", Limit: limit}
}

func problemBadQuery(detail string) Problem {
	return Problem{Type: problemBlank, Status: http.StatusBadRequest, Code: 40024, Msg: "invalid_query", Detail: detail}
}

func problemUploadTooLarge() Problem {
	return Problem{Type: ProblemLimit, Status: http.StatusRequestEntityTooLarge, Code: 41320, Msg: "upload_too_large",
		Detail: "The upload exceeds maxSizeUpload.", Limit: "maxSizeUpload"}
}

func problemAccountNotFound() Problem {
	return Problem{Type: problemBlank, Status: http.StatusNotFound, Code: 40420, Msg: "account_not_found",
		Detail: "The account does not exist."}
}

func problemBlobNotFound() Problem {
	return Problem{Type: problemBlank, Status: http.StatusNotFound, Code: 40421, Msg: "blob_not_found",
		Detail: "The blob does not exist."}
}

func problemUpstreamUnavailable(u *UpstreamError) Problem {
	return Problem{Type: ProblemUpstream, Status: http.StatusServiceUnavailable, Code: 50320, Msg: "upstream_unavailable",
		Detail: "The mailbox is temporarily unavailable.", Upstream: u}
}

func problemInternal() Problem {
	return Problem{Type: problemBlank, Status: http.StatusInternalServerError, Code: 50000, Msg: "internal_error",
		Detail: "Internal server error."}
}
