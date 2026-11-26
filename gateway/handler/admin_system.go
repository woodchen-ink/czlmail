package handler

import (
	"net/http"
	"time"

	"github.com/woodchen-ink/go-web-utils/resputil"

	"github.com/woodchen-ink/czlmail/gateway/service"
)

// System: GET /api/admin/system — version, database, master key and environment. Admins only.
func (h *Admin) System(w http.ResponseWriter, r *http.Request) {
	info, err := h.SystemSvc.Info(&actor(r).User)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, info)
}

// ConfirmMasterKeyBackup: POST /api/admin/system/master-key-backup with {} — the admin
// confirms GATEWAY_MASTER_KEY is stored somewhere safe.
func (h *Admin) ConfirmMasterKeyBackup(w http.ResponseWriter, r *http.Request) {
	at, err := h.SystemSvc.ConfirmMasterKeyBackup(&actor(r).User)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, struct {
		BackupConfirmedAt time.Time `json:"backupConfirmedAt"`
	}{at})
}

// ListTasks: GET /api/admin/system/tasks — background jobs and their recent runs. Admins only.
func (h *Admin) ListTasks(w http.ResponseWriter, r *http.Request) {
	if !service.IsAdmin(&actor(r).User) {
		h.fail(w, r, service.ErrForbidden)
		return
	}
	resputil.OK(w, h.Tasks.List())
}

// RunTask: POST /api/admin/system/tasks/{name}/run — run a job now and wait for it.
func (h *Admin) RunTask(w http.ResponseWriter, r *http.Request) {
	if !service.IsAdmin(&actor(r).User) {
		h.fail(w, r, service.ErrForbidden)
		return
	}
	run, err := h.Tasks.RunNow(r.PathValue("name"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, run)
}
