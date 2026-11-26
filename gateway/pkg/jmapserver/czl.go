package jmapserver

import "time"

// czlmail extensions to the Session.
const (
	// GatewayCapability is the session-level capability identifying a czlmail gateway.
	GatewayCapability = "https://czl.net/jmap/gateway"
	// UpstreamCapability is the account-level capability describing the mailbox
	// behind the account. It lives on the account because clients drop unknown
	// session-level capabilities but keep account capabilities.
	UpstreamCapability = "https://czl.net/jmap/upstream"
)

// GatewayInfo is the value of GatewayCapability.
type GatewayInfo struct {
	Version string `json:"version"`
	// ProtocolVersion changes only when the gateway's extensions change incompatibly.
	ProtocolVersion int    `json:"protocolVersion"`
	AdminURL        string `json:"adminUrl"`
}

// Upstream statuses and the codes reported with them.
const (
	UpstreamOK           = "ok"
	UpstreamAuthRequired = "authRequired" // 40110: the user must sign in to the mailbox again
	UpstreamServerError  = "error"        // 50210: the mailbox server answered with an error
	UpstreamUnavailable  = "unavailable"  // 50310: the mailbox server cannot be reached
	UpstreamRateLimited  = "rateLimited"  // 42910: the provider throttles the gateway
	UpstreamLocked       = "locked"       // 42310: the provider locked or disabled the mailbox
)

// UpstreamInfo is the value of UpstreamCapability on an account. An account
// never disappears from the Session because its upstream fails (the client would
// drop its cache); its status changes instead, and with it the Session state.
type UpstreamInfo struct {
	// Kind is the upstream type: "imap", "gmail", "microsoft".
	Kind   string `json:"kind"`
	Status string `json:"status"`
	// StatusSince is when Status last changed (UTC).
	StatusSince time.Time `json:"statusSince"`
	// Code is the gateway error code for a non-ok status, null when ok.
	Code *int `json:"code"`
	// ReauthURL opens the gateway admin page that re-authorises this mailbox,
	// null unless Status is authRequired.
	ReauthURL *string `json:"reauthUrl"`
	// InitialSync reports the first full import, null once it has finished.
	InitialSync *InitialSync `json:"initialSync"`
}

// InitialSync is the progress of an upstream's first import.
type InitialSync struct {
	MessagesDone  int `json:"messagesDone"`
	MessagesTotal int `json:"messagesTotal"`
}

// UpstreamError says why an upstream cannot serve a call; methods return it
// through ServerUnavailable and blob sources return it directly.
type UpstreamError struct {
	Status string `json:"status"`
	Code   int    `json:"code"`
}

func (e *UpstreamError) Error() string { return "upstream " + e.Status }
