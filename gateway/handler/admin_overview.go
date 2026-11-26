package handler

import (
	"net/http"

	"github.com/woodchen-ink/go-web-utils/resputil"
)

// Overview: GET /api/admin/overview?scope=mine|all&range=24h|7d|30d — upstream cards,
// online clients, to-dos and the hourly trend. Admins default to all upstreams.
func (h *Admin) Overview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	v, err := h.OverviewSvc.Get(r.Context(), &actor(r).User, q.Get("scope"), q.Get("range"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, v)
}
