package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/woodchen-ink/go-web-utils/resputil"

	"github.com/woodchen-ink/czlmail/gateway/service"
)

// ListSettings: GET /api/admin/settings. Admins only.
func (h *Admin) ListSettings(w http.ResponseWriter, r *http.Request) {
	if !service.IsAdmin(&actor(r).User) {
		h.fail(w, r, service.ErrForbidden)
		return
	}
	resputil.OK(w, h.Settings.List())
}

// UpdateSettings: PATCH /api/admin/settings with {"key": value, ...}.
func (h *Admin) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]json.RawMessage
	if err := decodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.Settings.Update(&actor(r).User, in); err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, h.Settings.List())
}

const (
	defaultAuditPage = 50
	maxAuditPage     = 200
)

type auditPageView struct {
	Items      []auditView `json:"items"`
	NextCursor string      `json:"nextCursor"`
}

// ListAudit: GET /api/admin/audit-logs?cursor=&limit= — newest first. Admins only.
func (h *Admin) ListAudit(w http.ResponseWriter, r *http.Request) {
	limit := defaultAuditPage
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxAuditPage {
			h.fail(w, r, errInvalidRequest)
			return
		}
		limit = n
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" && len(cursor) != 36 {
		h.fail(w, r, service.ErrInvalidCursor)
		return
	}
	rows, next, err := h.Audit.List(&actor(r).User, cursor, limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := auditPageView{Items: make([]auditView, 0, len(rows)), NextCursor: next}
	for i := range rows {
		out.Items = append(out.Items, newAuditView(&rows[i]))
	}
	resputil.OK(w, out)
}
