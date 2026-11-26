package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/woodchen-ink/go-web-utils/resputil"

	"github.com/woodchen-ink/czlmail/gateway/cron"
	"github.com/woodchen-ink/czlmail/gateway/middleware"
	"github.com/woodchen-ink/czlmail/gateway/service"
	"github.com/woodchen-ink/czlmail/gateway/service/submission"
)

// apiError is a public error: HTTP status, five-digit code (status + sequence) and a
// snake_case identifier. The full table is in README.md.
type apiError struct {
	status int
	code   int
	msg    string
}

var adminErrors = []struct {
	err error
	api apiError
}{
	{errInvalidRequest, apiError{400, 40000, "invalid_request"}},
	{errInvalidEmail, apiError{400, 40001, "invalid_email"}},
	{errInvalidPassword, apiError{400, 40002, "invalid_password"}},
	{errInvalidRole, apiError{400, 40003, "invalid_role"}},
	{errInvalidName, apiError{400, 40004, "invalid_name"}},
	{service.ErrUnknownSetting, apiError{400, 40005, "unknown_setting"}},
	{service.ErrInvalidSetting, apiError{400, 40006, "invalid_setting"}},
	{service.ErrWrongPassword, apiError{400, 40007, "wrong_password"}},
	{service.ErrInvalidCursor, apiError{400, 40008, "invalid_cursor"}},
	{service.ErrInvalidScope, apiError{400, 40009, "invalid_scope"}},
	{service.ErrInvalidRange, apiError{400, 40010, "invalid_range"}},
	{errInvalidHost, apiError{400, 40030, "invalid_host"}},
	{errInvalidPort, apiError{400, 40031, "invalid_port"}},
	{errInvalidTLSMode, apiError{400, 40032, "invalid_tls_mode"}},
	{errInvalidProxy, apiError{400, 40033, "invalid_proxy"}},
	{errMissingPassword, apiError{400, 40034, "missing_password"}},
	{service.ErrPasswordRequired, apiError{400, 40034, "missing_password"}},
	{service.ErrPrivateNetwork, apiError{400, 40035, "private_network_not_allowed"}},
	{errInvalidSentCopy, apiError{400, 40036, "invalid_sent_copy"}},
	{submission.ErrInvalidIdentity, apiError{400, 40037, "invalid_identity"}},
	{errInvalidExpiry, apiError{400, 40040, "invalid_secret_expiry"}},
	{service.ErrInvalidClientID, apiError{400, 40041, "invalid_client_id"}},
	{service.ErrMissingClientSecret, apiError{400, 40038, "missing_client_secret"}},
	{service.ErrOAuthState, apiError{400, 40039, "invalid_oauth_state"}},
	{service.ErrUnauthenticated, apiError{401, 40100, "unauthenticated"}},
	{service.ErrInvalidCredentials, apiError{401, 40101, "invalid_credentials"}},
	{service.ErrAccountDisabled, apiError{401, 40102, "account_disabled"}},
	{service.ErrInvalidSetupToken, apiError{401, 40103, "invalid_setup_token"}},
	{service.ErrForbidden, apiError{403, 40301, "forbidden"}},
	{service.ErrSelfModify, apiError{403, 40302, "cannot_modify_self"}},
	{service.ErrRegistrationClosed, apiError{403, 40303, "registration_closed"}},
	{service.ErrOAuthDenied, apiError{403, 40330, "oauth_denied"}},
	{service.ErrOAuthScopeDenied, apiError{403, 40331, "oauth_scope_denied"}},
	{service.ErrUserNotFound, apiError{404, 40401, "user_not_found"}},
	{service.ErrAppPasswordNotFound, apiError{404, 40402, "app_password_not_found"}},
	{cron.ErrUnknownTask, apiError{404, 40403, "task_not_found"}},
	{service.ErrUpstreamNotFound, apiError{404, 40430, "upstream_not_found"}},
	{submission.ErrIdentityNotFound, apiError{404, 40431, "identity_not_found"}},
	{submission.ErrAccountNotFound, apiError{404, 40430, "upstream_not_found"}},
	{service.ErrOAuthProviderNotFound, apiError{404, 40432, "oauth_provider_not_found"}},
	{service.ErrEmailTaken, apiError{409, 40900, "email_taken"}},
	{service.ErrSetupCompleted, apiError{409, 40901, "setup_completed"}},
	{cron.ErrTaskRunning, apiError{409, 40902, "task_running"}},
	{service.ErrUpstreamKindReadOnly, apiError{409, 40930, "upstream_kind_managed"}},
	{submission.ErrIdentityManaged, apiError{409, 40931, "identity_managed"}},
	{submission.ErrUpstreamNotRunning, apiError{409, 40932, "upstream_not_running"}},
	{submission.ErrCannotTestSend, apiError{409, 40933, "send_test_unsupported"}},
	{service.ErrOAuthAppNotConfigured, apiError{409, 40934, "oauth_app_not_configured"}},
	{service.ErrOAuthAccountMismatch, apiError{409, 40935, "oauth_account_mismatch"}},
	{service.ErrOAuthKindMismatch, apiError{409, 40936, "oauth_kind_mismatch"}},
	{service.ErrOAuthExchange, apiError{502, 50230, "oauth_exchange_failed"}},
}

// fail writes the public form of err; unknown errors are logged and become 50000.
func (h *Admin) fail(w http.ResponseWriter, r *http.Request, err error) {
	var rl *service.RateLimitError
	if errors.As(err, &rl) {
		w.Header().Set("Retry-After", strconv.Itoa(rl.RetryAfterSeconds()))
		resputil.FailStatus(w, http.StatusTooManyRequests, 42900, "too_many_requests")
		return
	}
	for _, e := range adminErrors {
		if errors.Is(err, e.err) {
			resputil.FailStatus(w, e.api.status, e.api.code, e.api.msg)
			return
		}
	}
	h.Log.Error("admin request failed", "trace_id", middleware.TraceID(r.Context()), "path", r.URL.Path, "err", err)
	resputil.FailStatus(w, http.StatusInternalServerError, 50000, "internal_error")
}
