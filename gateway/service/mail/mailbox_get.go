package mail

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

var mailboxProperties = []string{"id", "name", "parentId", "role", "sortOrder", "totalEmails", "unreadEmails",
	"totalThreads", "unreadThreads", "myRights", "isSubscribed"}

// mailboxCountProperties change when only the counts did (Mailbox/changes updatedProperties).
var mailboxCountProperties = []string{"totalEmails", "unreadEmails", "totalThreads", "unreadThreads"}

type mailboxRights struct {
	MayReadItems   bool `json:"mayReadItems"`
	MayAddItems    bool `json:"mayAddItems"`
	MayRemoveItems bool `json:"mayRemoveItems"`
	MaySetSeen     bool `json:"maySetSeen"`
	MaySetKeywords bool `json:"maySetKeywords"`
	MayCreateChild bool `json:"mayCreateChild"`
	MayRename      bool `json:"mayRename"`
	MayDelete      bool `json:"mayDelete"`
	MaySubmit      bool `json:"maySubmit"`
}

type mailboxCounts struct {
	MailboxID     string
	TotalEmails   int64
	UnreadEmails  int64
	TotalThreads  int64
	UnreadThreads int64
}

// mailboxCountsSQL counts every mailbox in one pass (RFC 8621 §2). An unread
// email has neither $seen nor $draft. A thread is unread for a mailbox when it
// has an unread email outside Trash and Junk; for Trash and Junk themselves
// unread emails anywhere count. Orphans are not there.
const mailboxCountsSQL = `
WITH bin AS (SELECT id FROM mailboxes WHERE role IN ('trash', 'junk')),
unread AS (SELECT id, thread_id FROM emails WHERE seen = 0 AND draft = 0 AND orphaned_from = ''),
ut_all AS (SELECT DISTINCT thread_id FROM unread),
ut AS (SELECT DISTINCT u.thread_id FROM unread u
       WHERE EXISTS (SELECT 1 FROM memberships m WHERE m.email_id = u.id AND m.mailbox_id NOT IN (SELECT id FROM bin)))
SELECT ms.mailbox_id AS mailbox_id,
       COUNT(*) AS total_emails,
       SUM(CASE WHEN e.seen = 0 AND e.draft = 0 THEN 1 ELSE 0 END) AS unread_emails,
       COUNT(DISTINCT e.thread_id) AS total_threads,
       COUNT(DISTINCT CASE WHEN ms.mailbox_id IN (SELECT id FROM bin) THEN uta.thread_id ELSE ut.thread_id END) AS unread_threads
FROM memberships ms
JOIN emails e ON e.id = ms.email_id AND e.orphaned_from = ''
LEFT JOIN ut ON ut.thread_id = e.thread_id
LEFT JOIN ut_all uta ON uta.thread_id = e.thread_id
GROUP BY ms.mailbox_id`

func loadMailboxes(idx *mailindex.Store) ([]model.IndexMailbox, map[string]mailboxCounts, error) {
	var boxes []model.IndexMailbox
	if err := idx.DB().Order("sort_order, name").Find(&boxes).Error; err != nil {
		return nil, nil, err
	}
	var rows []mailboxCounts
	if err := idx.DB().Raw(mailboxCountsSQL).Scan(&rows).Error; err != nil {
		return nil, nil, err
	}
	counts := make(map[string]mailboxCounts, len(rows))
	for _, r := range rows {
		counts[r.MailboxID] = r
	}
	return boxes, counts, nil
}

// leafName is the last level of an IMAP folder name.
func leafName(m *model.IndexMailbox) string {
	if m.Delimiter != "" {
		if i := strings.LastIndex(m.Name, m.Delimiter); i >= 0 {
			return m.Name[i+len(m.Delimiter):]
		}
	}
	return m.Name
}

func rightsOf(m *model.IndexMailbox) mailboxRights {
	if m.NoSelect {
		return mailboxRights{MayCreateChild: m.Delimiter != "", MayRename: true, MayDelete: true}
	}
	inbox := m.Role == "inbox"
	return mailboxRights{MayReadItems: true, MayAddItems: true, MayRemoveItems: true, MaySetSeen: true, MaySetKeywords: true,
		MayCreateChild: m.Delimiter != "", MayRename: !inbox, MayDelete: !inbox, MaySubmit: true}
}

func mailboxObject(m *model.IndexMailbox, c mailboxCounts, props map[string]bool) map[string]any {
	o := map[string]any{"id": m.ID}
	put := func(name string, v any) {
		if props[name] {
			o[name] = v
		}
	}
	put("name", leafName(m))
	put("parentId", nullable(m.ParentID))
	put("role", nullable(m.Role))
	put("sortOrder", m.SortOrder)
	put("totalEmails", c.TotalEmails)
	put("unreadEmails", c.UnreadEmails)
	put("totalThreads", c.TotalThreads)
	put("unreadThreads", c.UnreadThreads)
	put("myRights", rightsOf(m))
	put("isSubscribed", m.Subscribed)
	return o
}

// nullable is nil for "" (JSON null).
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Service) mailboxGet(_ context.Context, call *jmapserver.Call) (any, error) {
	var args getArgs
	if err := call.Decode(&args); err != nil {
		return nil, err
	}
	if err := checkIDs(args.IDs); err != nil {
		return nil, err
	}
	props, err := pickProperties(args.Properties, mailboxProperties, mailboxProperties)
	if err != nil {
		return nil, err
	}
	a, err := s.open(call, args.AccountID)
	if err != nil {
		return nil, err
	}
	state := a.state(mailindex.TypeMailbox)
	boxes, counts, err := loadMailboxes(a.idx)
	if err != nil {
		return nil, err
	}
	list := []map[string]any{}
	notFound := []string{}
	if args.IDs == nil {
		for i := range boxes {
			list = append(list, mailboxObject(&boxes[i], counts[boxes[i].ID], props))
		}
	} else {
		byID := make(map[string]*model.IndexMailbox, len(boxes))
		for i := range boxes {
			byID[boxes[i].ID] = &boxes[i]
		}
		for _, id := range dedupe(*args.IDs) {
			real, _ := resolveID(call, id)
			if m := byID[real]; m != nil {
				list = append(list, mailboxObject(m, counts[m.ID], props))
			} else {
				notFound = append(notFound, id)
			}
		}
	}
	return map[string]any{"accountId": a.id, "state": state, "list": list, "notFound": notFound}, nil
}

func (s *Service) mailboxChanges(_ context.Context, call *jmapserver.Call) (any, error) {
	a, ch, err := s.changes(call, mailindex.TypeMailbox)
	if err != nil {
		return nil, err
	}
	resp := struct {
		changesResponse
		UpdatedProperties []string `json:"updatedProperties"`
	}{changesResponse: toChangesResponse(a.id, ch)}
	if ch.CountsOnly && len(ch.Created) == 0 && len(ch.Destroyed) == 0 {
		resp.UpdatedProperties = mailboxCountProperties
	}
	return resp, nil
}

// Mailbox/query.

type mailboxFilter struct {
	Operator     string           `json:"operator"`
	Conditions   []*mailboxFilter `json:"conditions"`
	ParentID     json.RawMessage  `json:"parentId"`
	Name         *string          `json:"name"`
	Role         json.RawMessage  `json:"role"`
	HasAnyRole   *bool            `json:"hasAnyRole"`
	IsSubscribed *bool            `json:"isSubscribed"`
}

// optString decodes a String|null filter value: set reports presence.
func optString(raw json.RawMessage) (val string, set bool, err error) {
	if len(raw) == 0 {
		return "", false, nil
	}
	if string(raw) == "null" {
		return "", true, nil
	}
	err = json.Unmarshal(raw, &val)
	return val, true, err
}

func (f *mailboxFilter) match(m *model.IndexMailbox) (bool, error) {
	if f.Operator != "" {
		switch f.Operator {
		case "AND", "OR", "NOT":
		default:
			return false, jmapserver.ErrUnsupportedFilter
		}
		any, all := false, true
		for _, c := range f.Conditions {
			ok, err := c.match(m)
			if err != nil {
				return false, err
			}
			any, all = any || ok, all && ok
		}
		switch f.Operator {
		case "AND":
			return all, nil
		case "OR":
			return any, nil
		}
		return !any, nil
	}
	if v, set, err := optString(f.ParentID); err != nil {
		return false, jmapserver.ErrUnsupportedFilter
	} else if set && v != m.ParentID {
		return false, nil
	}
	if v, set, err := optString(f.Role); err != nil {
		return false, jmapserver.ErrUnsupportedFilter
	} else if set && v != m.Role {
		return false, nil
	}
	if f.Name != nil && !strings.Contains(strings.ToLower(leafName(m)), strings.ToLower(*f.Name)) {
		return false, nil
	}
	if f.HasAnyRole != nil && *f.HasAnyRole != (m.Role != "") {
		return false, nil
	}
	if f.IsSubscribed != nil && *f.IsSubscribed != m.Subscribed {
		return false, nil
	}
	return true, nil
}

type comparator struct {
	Property    string `json:"property"`
	IsAscending *bool  `json:"isAscending"`
	Collation   string `json:"collation"`
	Keyword     string `json:"keyword"`
}

func (c comparator) ascending() bool { return c.IsAscending == nil || *c.IsAscending }

func (s *Service) mailboxQuery(_ context.Context, call *jmapserver.Call) (any, error) {
	var args struct {
		AccountID      string         `json:"accountId"`
		Filter         *mailboxFilter `json:"filter"`
		Sort           []comparator   `json:"sort"`
		Position       int            `json:"position"`
		Anchor         *string        `json:"anchor"`
		AnchorOffset   int            `json:"anchorOffset"`
		Limit          *int           `json:"limit"`
		CalculateTotal bool           `json:"calculateTotal"`
		SortAsTree     bool           `json:"sortAsTree"`
		FilterAsTree   bool           `json:"filterAsTree"`
	}
	if err := call.Decode(&args); err != nil {
		return nil, err
	}
	a, err := s.open(call, args.AccountID)
	if err != nil {
		return nil, err
	}
	state := a.state(mailindex.TypeMailbox)
	var boxes []model.IndexMailbox
	if err := a.idx.DB().Order("name").Find(&boxes).Error; err != nil {
		return nil, err
	}
	for _, c := range args.Sort {
		if c.Property != "sortOrder" && c.Property != "name" {
			return nil, jmapserver.ErrUnsupportedSort
		}
	}
	less := func(x, y *model.IndexMailbox) int {
		for _, c := range args.Sort {
			var d int
			if c.Property == "sortOrder" {
				d = x.SortOrder - y.SortOrder
			} else {
				d = strings.Compare(strings.ToLower(leafName(x)), strings.ToLower(leafName(y)))
			}
			if !c.ascending() {
				d = -d
			}
			if d != 0 {
				return d
			}
		}
		return strings.Compare(x.Name, y.Name)
	}
	slices.SortStableFunc(boxes, func(x, y model.IndexMailbox) int { return less(&x, &y) })
	byID := map[string]*model.IndexMailbox{}
	for i := range boxes {
		byID[boxes[i].ID] = &boxes[i]
	}
	matched := map[string]bool{}
	for i := range boxes {
		ok := true
		if args.Filter != nil {
			if ok, err = args.Filter.match(&boxes[i]); err != nil {
				return nil, err
			}
		}
		matched[boxes[i].ID] = ok
	}
	if args.FilterAsTree {
		// A mailbox only matches when all its ancestors match too.
		for id := range matched {
			for p := byID[id].ParentID; p != "" && matched[id]; p = byID[p].ParentID {
				if byID[p] == nil {
					break
				}
				matched[id] = matched[id] && matched[p]
			}
		}
	}
	ordered := boxes
	if args.SortAsTree {
		ordered = treeOrder(boxes)
	}
	ids := []string{}
	for _, m := range ordered {
		if matched[m.ID] {
			ids = append(ids, m.ID)
		}
	}
	return pageIDs(ids, args.Position, args.Anchor, args.AnchorOffset, args.Limit, args.CalculateTotal, a.id, state, false)
}

// treeOrder lists parents before their children, siblings in the given order.
func treeOrder(boxes []model.IndexMailbox) []model.IndexMailbox {
	children := map[string][]model.IndexMailbox{}
	ids := map[string]bool{}
	for _, m := range boxes {
		ids[m.ID] = true
	}
	for _, m := range boxes {
		p := m.ParentID
		if !ids[p] {
			p = ""
		}
		children[p] = append(children[p], m)
	}
	out := make([]model.IndexMailbox, 0, len(boxes))
	var walk func(string)
	walk = func(p string) {
		for _, c := range children[p] {
			out = append(out, c)
			walk(c.ID)
		}
	}
	walk("")
	return out
}

// pageIDs applies position / anchor / limit to a full result (RFC 8620 §5.5).
func pageIDs(ids []string, position int, anchor *string, anchorOffset int, limit *int, calcTotal bool,
	accountID, state string, canCalc bool) (any, error) {
	if limit != nil && *limit < 0 {
		return nil, jmapserver.InvalidArguments("limit must not be negative")
	}
	start := position
	if anchor != nil {
		i := slices.Index(ids, *anchor)
		if i < 0 {
			return nil, jmapserver.ErrAnchorNotFound
		}
		start = max(i+anchorOffset, 0)
	} else if start < 0 {
		start = max(len(ids)+start, 0)
	}
	start = min(start, len(ids))
	end := len(ids)
	if limit != nil {
		end = min(start+*limit, len(ids))
	}
	out := map[string]any{"accountId": accountID, "queryState": state, "canCalculateChanges": canCalc,
		"position": start, "ids": ids[start:end]}
	if calcTotal {
		out["total"] = len(ids)
	}
	return out, nil
}
