// Package handler parses HTTP requests and writes responses; the work happens in service.
package handler

import (
	"log/slog"
	"net/http"

	"github.com/woodchen-ink/go-web-utils/resputil"
	"gorm.io/gorm"
)

// Health answers /healthz for Docker and reverse proxies.
type Health struct {
	DB      *gorm.DB
	Version string
	Log     *slog.Logger
}

type healthView struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func (h *Health) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sqlDB, err := h.DB.DB()
	if err == nil {
		err = sqlDB.PingContext(r.Context())
	}
	if err != nil {
		h.Log.Error("health check failed", "err", err)
		resputil.FailStatus(w, http.StatusServiceUnavailable, 50300, "database_unavailable")
		return
	}
	resputil.OK(w, healthView{Status: "ok", Version: h.Version})
}
