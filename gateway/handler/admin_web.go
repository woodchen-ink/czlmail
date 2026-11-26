package handler

import (
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/woodchen-ink/go-web-utils/nextstatic"
)

// AdminWebPrefix is where the admin pages live; the Next.js build uses it as basePath.
const AdminWebPrefix = "/admin"

// NewAdminWeb serves the admin web build in root under /admin/. The build is exported
// with basePath /admin, so its files sit at the root of the directory and the prefix is
// stripped before lookup. Without a build it answers 503 with a hint instead.
func NewAdminWeb(root string) (h http.Handler, installed bool) {
	static, err := nextstatic.New(nextstatic.Config{Root: root, TrailingSlash: true, APIPrefixes: []string{}})
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "The admin web is not installed in this build (GATEWAY_WEB_DIR). The admin API is under /api/admin/.", http.StatusServiceUnavailable)
		}), false
	}
	inner := http.StripPrefix(AdminWebPrefix, static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		// nextstatic redirects to a trailing slash itself, but it would drop the prefix.
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && !strings.HasSuffix(p, "/") && path.Ext(p) == "" {
			redirectKeepQuery(w, r, p+"/", http.StatusPermanentRedirect)
			return
		}
		inner.ServeHTTP(w, r)
	}), true
}

// LegacySetup sends the setup link printed by earlier versions (/setup?token=) to the
// admin's setup page.
func LegacySetup(w http.ResponseWriter, r *http.Request) {
	redirectKeepQuery(w, r, AdminWebPrefix+"/setup/", http.StatusFound)
}

// AdminHome sends the bare origin to the admin.
func AdminHome(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, AdminWebPrefix+"/", http.StatusFound)
}

func redirectKeepQuery(w http.ResponseWriter, r *http.Request, to string, status int) {
	u := url.URL{Path: to, RawQuery: r.URL.RawQuery}
	http.Redirect(w, r, u.String(), status)
}
