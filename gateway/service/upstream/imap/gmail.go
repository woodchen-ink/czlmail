package imap

import (
	"strconv"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/pkg/netx"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Gmail is read over IMAP with XOAUTH2 and the X-GM-EXT-1 extension, and sends
// through the Gmail API.
//
// Only [Gmail]/All Mail, Trash and Spam are synced as folders. All Mail is a
// hidden mailbox row; every other Gmail folder is a label and becomes a virtual
// mailbox whose memberships come from the X-GM-LABELS of All Mail's messages
// (one email, several mailboxes). Messages without a visible label are in the
// virtual Archive mailbox. Ids: X-GM-MSGID (stable across labels and Trash),
// threads: X-GM-THRID. \Starred is the \Flagged flag, so the $flagged keyword.
func init() {
	upstream.Register(upstream.KindGmail, func(cfg upstream.Config, idx *mailindex.Store) (upstream.Upstream, error) {
		return NewGmail(cfg, idx, Options{}), nil
	})
	upstream.RegisterProber(upstream.KindGmail, Probe)
}

// gmailState is what a Gmail upstream keeps besides the IMAP engine.
type gmailState struct {
	api *gmailAPI

	idMu       sync.Mutex
	identities []upstream.Identity
	identAt    time.Time
}

// Gmail is a Gmail upstream: the IMAP engine with Gmail's label model, plus the
// optional interfaces (several mailboxes per email, X-GM-RAW search, sending and
// identities through the Gmail API).
type Gmail struct {
	*Upstream
}

var (
	_ upstream.Upstream       = (*Gmail)(nil)
	_ upstream.MailboxSetter  = (*Gmail)(nil)
	_ upstream.TextSearcher   = (*Gmail)(nil)
	_ upstream.MessageSender  = (*Gmail)(nil)
	_ upstream.IdentitySource = (*Gmail)(nil)
)

// NewGmail returns a Gmail upstream over idx. cfg.AccessToken signs in;
// cfg.APIBase is the Gmail API root.
func NewGmail(cfg upstream.Config, idx *mailindex.Store, opts Options) *Gmail {
	opts.Dialect = Dialect{
		FetchItems: []string{"X-GM-MSGID", "X-GM-THRID", "X-GM-LABELS"},
		StableKey:  func(m *imapc.Message) string { return gmKey(m, "X-GM-MSGID") },
		ThreadKey:  func(m *imapc.Message) string { return gmKey(m, "X-GM-THRID") },
	}
	u := NewWithOptions(cfg, idx, opts)
	u.gm = &gmailState{api: newGmailAPI(cfg, netx.HTTPClient(cfg.Dialer, gmailAPITimeout))}
	return &Gmail{Upstream: u}
}

// gmKey is "gm:<decimal X-GM-MSGID>" (or THRID).
func gmKey(m *imapc.Message, item string) string {
	v, ok := m.Extra[item]
	if !ok {
		return ""
	}
	if v.Kind == imapc.KindNumber {
		return "gm:" + strconv.FormatUint(v.Num, 10)
	}
	if s := v.String(); s != "" {
		return "gm:" + s
	}
	return ""
}

// flagItems are the items of a flag compare; Gmail adds the labels.
func (u *Upstream) flagItems(base ...string) []string {
	if u.gm != nil {
		return append(base, "X-GM-LABELS")
	}
	return base
}

// labelRowsFor loads the label mailboxes for a Gmail transaction (nil otherwise).
func (u *Upstream) labelRowsFor(tx *mailindex.Tx) (*labelRows, error) {
	if u.gm == nil {
		return nil, nil
	}
	return loadLabelRows(tx)
}

// placeLabels gives a newly placed Gmail message the mailboxes of its labels.
func (u *Upstream) placeLabels(tx *mailindex.Tx, lr *labelRows, src *model.IndexMailbox, emailID string, m *imapc.Message) error {
	if lr == nil {
		return nil
	}
	labels, _ := gmLabels(m)
	if src.Hidden {
		// Back in All Mail (restored outside the gateway): the labels remembered
		// for an undo are moot.
		if err := tx.SetTrashLabels(emailID, nil); err != nil {
			return err
		}
	}
	return u.setLabels(tx, lr, src, emailID, m.UID, labels)
}

// applyFlagLabels applies the labels of a folder's flag compare (Gmail).
func (u *Upstream) applyFlagLabels(tx *mailindex.Tx, d *folderDelta) error {
	if u.gm == nil || !d.mbox.Hidden {
		return nil
	}
	lr, err := loadLabelRows(tx)
	if err != nil {
		return err
	}
	for _, f := range d.flags {
		info, ok := d.known[f.uid]
		if !ok || !f.hasLabels {
			continue
		}
		if err := u.setLabels(tx, lr, d.mbox, info.EmailID, f.uid, f.labels); err != nil {
			return err
		}
	}
	return nil
}

// watchFolder is the folder IDLE watches: INBOX, or All Mail on Gmail (a label
// change anywhere shows up there).
func (u *Upstream) watchFolder() string {
	if u.gm != nil {
		if m := u.gmFolder("all"); m != nil {
			return m.Name
		}
	}
	return "INBOX"
}

// gmFolder returns the synced Gmail folder of a role (all, trash, junk).
func (u *Upstream) gmFolder(role string) *model.IndexMailbox {
	var m model.IndexMailbox
	if err := u.idx.DB().Where("role = ? AND virtual = ?", role, false).First(&m).Error; err != nil {
		return nil
	}
	return &m
}
