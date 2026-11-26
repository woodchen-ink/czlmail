package handler

import (
	"encoding/json"
	"net/http"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

// CzlmailConfig serves /.well-known/czlmail.json, the sign-in auto-configuration
// czlmail reads from the server address. The gateway object tells the client it
// talks to a gateway and where its admin is.
type CzlmailConfig struct {
	Gateway jmapserver.GatewayInfo
}

type czlmailConfigView struct {
	Version int                `json:"version"`
	Gateway czlmailGatewayView `json:"gateway"`
}

type czlmailGatewayView struct {
	Name            string `json:"name"`
	AdminURL        string `json:"adminUrl"`
	ProtocolVersion int    `json:"protocolVersion"`
	Version         string `json:"version"`
}

func (h *CzlmailConfig) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(czlmailConfigView{
		Version: 1,
		Gateway: czlmailGatewayView{
			Name:            "czlmail 网关",
			AdminURL:        h.Gateway.AdminURL,
			ProtocolVersion: h.Gateway.ProtocolVersion,
			Version:         h.Gateway.Version,
		},
	})
}
