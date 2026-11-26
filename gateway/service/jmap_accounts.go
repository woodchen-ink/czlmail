package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// JMAP capabilities of upstream accounts (RFC 8621).
const (
	MailCapability       = "urn:ietf:params:jmap:mail"
	SubmissionCapability = "urn:ietf:params:jmap:submission"
)

// UpstreamAccounts lists a gateway user's upstream mailboxes as JMAP accounts.
// An account never leaves the Session because its upstream fails: its upstream
// capability reports the status instead.
type UpstreamAccounts struct {
	db       *gorm.DB
	mgr      *UpstreamManager
	adminURL string
}

// NewUpstreamAccounts returns the account source. adminURL is the admin's base URL
// (".../admin/"), for re-authorisation links.
func NewUpstreamAccounts(db *gorm.DB, mgr *UpstreamManager, adminURL string) *UpstreamAccounts {
	return &UpstreamAccounts{db: db, mgr: mgr, adminURL: adminURL}
}

// UserSession implements jmapserver.AccountSource. The username is the user's email.
func (a *UpstreamAccounts) UserSession(ctx context.Context, userPublicID string) (*jmapserver.UserSession, error) {
	var u model.User
	err := a.db.WithContext(ctx).Select("id", "email").Where("public_id = ?", userPublicID).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Deleted between authentication and now: nothing to show.
		return &jmapserver.UserSession{}, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []model.Upstream
	if err := a.db.WithContext(ctx).Where("user_id = ?", u.ID).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	us := &jmapserver.UserSession{Username: u.Email, Accounts: map[string]*jmapserver.Account{}, PrimaryAccounts: map[string]string{}}
	for i := range rows {
		row := &rows[i]
		us.Accounts[row.PublicID] = &jmapserver.Account{
			Name:       row.Name,
			IsPersonal: true,
			AccountCapabilities: map[string]any{
				MailCapability:                mailAccountCapability(row),
				SubmissionCapability:          map[string]any{"maxDelayedSend": 0, "submissionExtensions": map[string]any{}},
				jmapserver.UpstreamCapability: a.upstreamInfo(row),
			},
		}
		if row.IsDefault {
			us.PrimaryAccounts[MailCapability] = row.PublicID
			us.PrimaryAccounts[SubmissionCapability] = row.PublicID
		}
	}
	return us, nil
}

func mailAccountCapability(row *model.Upstream) map[string]any {
	// Sorting and paging come from the index; sorting by address fields needs the
	// server's SORT, so it is offered only when the server has it.
	sorts := []string{"receivedAt", "sentAt", "size", "subject", "hasKeyword", "allInThreadHaveKeyword", "someInThreadHaveKeyword"}
	var caps upstream.Capabilities
	if row.ServerCaps != "" && json.Unmarshal([]byte(row.ServerCaps), &caps) == nil && caps.Sort {
		sorts = append(sorts, "from", "to")
	}
	return map[string]any{
		"maxMailboxesPerEmail":       1,
		"maxMailboxDepth":            nil,
		"maxSizeMailboxName":         490,
		"maxSizeAttachmentsPerEmail": jmapserver.MaxSizeUpload,
		"emailQuerySortOptions":      sorts,
		"mayCreateTopLevelMailbox":   true,
	}
}

func (a *UpstreamAccounts) upstreamInfo(row *model.Upstream) jmapserver.UpstreamInfo {
	st := a.mgr.Status(row)
	info := jmapserver.UpstreamInfo{Kind: row.Kind, Status: st.State, StatusSince: st.Since.UTC()}
	if info.Status == "" {
		info.Status = jmapserver.UpstreamSyncing
	}
	code := 0
	switch info.Status {
	case jmapserver.UpstreamSyncing:
		code = jmapserver.CodeUpstreamSyncing
		p := jmapserver.InitialSync{}
		if st.Progress != nil {
			p = *st.Progress
		}
		info.InitialSync = &p
	case jmapserver.UpstreamAuthRequired:
		code = jmapserver.CodeUpstreamAuthRequired
		link := a.adminURL + "upstreams/" + url.PathEscape(row.PublicID)
		info.ReauthURL = &link
	case jmapserver.UpstreamPaused:
		code = jmapserver.CodeUpstreamPaused
	case jmapserver.UpstreamUnreachable:
		code = st.Code
		if code == 0 {
			code = jmapserver.CodeUpstreamUnreachable
		}
	}
	if code != 0 {
		info.Code = &code
	}
	return info
}
