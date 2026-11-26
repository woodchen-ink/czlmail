package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// emailFilter is a FilterOperator or a FilterCondition (RFC 8621 §4.4.1).
type emailFilter struct {
	Operator   string         `json:"operator"`
	Conditions []*emailFilter `json:"conditions"`

	InMailbox               *string    `json:"inMailbox"`
	InMailboxOtherThan      []string   `json:"inMailboxOtherThan"`
	Before                  *time.Time `json:"before"`
	After                   *time.Time `json:"after"`
	MinSize                 *int64     `json:"minSize"`
	MaxSize                 *int64     `json:"maxSize"`
	AllInThreadHaveKeyword  *string    `json:"allInThreadHaveKeyword"`
	SomeInThreadHaveKeyword *string    `json:"someInThreadHaveKeyword"`
	NoneInThreadHaveKeyword *string    `json:"noneInThreadHaveKeyword"`
	HasKeyword              *string    `json:"hasKeyword"`
	NotKeyword              *string    `json:"notKeyword"`
	HasAttachment           *bool      `json:"hasAttachment"`
	Text                    *string    `json:"text"`
	From                    *string    `json:"from"`
	To                      *string    `json:"to"`
	Cc                      *string    `json:"cc"`
	Bcc                     *string    `json:"bcc"`
	Subject                 *string    `json:"subject"`
	Body                    *string    `json:"body"`
	Header                  []string   `json:"header"`
}

var filterKeys = map[string]bool{"operator": true, "conditions": true, "inMailbox": true, "inMailboxOtherThan": true,
	"before": true, "after": true, "minSize": true, "maxSize": true, "allInThreadHaveKeyword": true,
	"someInThreadHaveKeyword": true, "noneInThreadHaveKeyword": true, "hasKeyword": true, "notKeyword": true,
	"hasAttachment": true, "text": true, "from": true, "to": true, "cc": true, "bcc": true, "subject": true,
	"body": true, "header": true}

func (f *emailFilter) UnmarshalJSON(b []byte) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return err
	}
	for k := range keys {
		if !filterKeys[k] {
			return fmt.Errorf("unsupported filter property %q", k)
		}
	}
	type plain emailFilter
	return json.Unmarshal(b, (*plain)(f))
}

// upstreamPart is the part of a condition the index cannot answer.
func (f *emailFilter) upstreamPart() (upstream.SearchQuery, bool) {
	var q upstream.SearchQuery
	if f.Text != nil {
		q.Text = []string{*f.Text}
	}
	if f.Body != nil {
		q.Body = []string{*f.Body}
	}
	if f.Bcc != nil {
		q.Bcc = []string{*f.Bcc}
	}
	if len(f.Header) > 0 {
		q.Header = [][]string{f.Header}
	}
	return q, f.Text != nil || f.Body != nil || f.Bcc != nil || len(f.Header) > 0
}

// sqlExpr is a WHERE / ORDER BY fragment with its parameters.
type sqlExpr struct {
	sql  string
	args []any
}

// queryBuilder turns a filter into SQL over the emails table, running the
// server searches it needs first.
type queryBuilder struct {
	ctx  context.Context
	s    *Service
	a    *account
	call *jmapserver.Call
}

const maxFilterDepth = 10

func (qb *queryBuilder) build(f *emailFilter, depth int) (sqlExpr, error) {
	if depth > maxFilterDepth {
		return sqlExpr{}, jmapserver.ErrUnsupportedFilter
	}
	if f.Operator != "" {
		var parts []string
		var args []any
		for _, c := range f.Conditions {
			e, err := qb.build(c, depth+1)
			if err != nil {
				return sqlExpr{}, err
			}
			parts = append(parts, "("+e.sql+")")
			args = append(args, e.args...)
		}
		switch f.Operator {
		case "AND":
			if len(parts) == 0 {
				return sqlExpr{sql: "1"}, nil
			}
			return sqlExpr{sql: strings.Join(parts, " AND "), args: args}, nil
		case "OR":
			if len(parts) == 0 {
				return sqlExpr{sql: "0"}, nil
			}
			return sqlExpr{sql: strings.Join(parts, " OR "), args: args}, nil
		case "NOT":
			if len(parts) == 0 {
				return sqlExpr{sql: "1"}, nil
			}
			return sqlExpr{sql: "NOT (" + strings.Join(parts, " OR ") + ")", args: args}, nil
		}
		return sqlExpr{}, jmapserver.ErrUnsupportedFilter
	}
	var conds []string
	var args []any
	add := func(sql string, a ...any) {
		conds = append(conds, sql)
		args = append(args, a...)
	}
	if f.InMailbox != nil {
		id, _ := resolveID(qb.call, *f.InMailbox)
		add("EXISTS (SELECT 1 FROM memberships m WHERE m.email_id = emails.id AND m.mailbox_id = ?)", id)
	}
	if f.InMailboxOtherThan != nil {
		ids := make([]string, 0, len(f.InMailboxOtherThan))
		for _, x := range f.InMailboxOtherThan {
			id, _ := resolveID(qb.call, x)
			ids = append(ids, id)
		}
		add("EXISTS (SELECT 1 FROM memberships m WHERE m.email_id = emails.id AND m.mailbox_id NOT IN (SELECT value FROM json_each(?)))", jsonList(ids))
	}
	if f.Before != nil {
		add("emails.received_at < ?", f.Before.UTC())
	}
	if f.After != nil {
		add("emails.received_at >= ?", f.After.UTC())
	}
	if f.MinSize != nil {
		add("emails.size >= ?", *f.MinSize)
	}
	if f.MaxSize != nil {
		add("emails.size < ?", *f.MaxSize)
	}
	if f.HasKeyword != nil {
		e := keywordExpr("emails", *f.HasKeyword)
		add(e.sql, e.args...)
	}
	if f.NotKeyword != nil {
		e := keywordExpr("emails", *f.NotKeyword)
		add("NOT "+e.sql, e.args...)
	}
	if f.AllInThreadHaveKeyword != nil {
		e := keywordExpr("t", *f.AllInThreadHaveKeyword)
		add("NOT EXISTS (SELECT 1 FROM emails t WHERE t.thread_id = emails.thread_id AND t.orphaned_from = '' AND NOT "+e.sql+")", e.args...)
	}
	if f.SomeInThreadHaveKeyword != nil {
		e := keywordExpr("t", *f.SomeInThreadHaveKeyword)
		add("EXISTS (SELECT 1 FROM emails t WHERE t.thread_id = emails.thread_id AND t.orphaned_from = '' AND "+e.sql+")", e.args...)
	}
	if f.NoneInThreadHaveKeyword != nil {
		e := keywordExpr("t", *f.NoneInThreadHaveKeyword)
		add("NOT EXISTS (SELECT 1 FROM emails t WHERE t.thread_id = emails.thread_id AND t.orphaned_from = '' AND "+e.sql+")", e.args...)
	}
	if f.HasAttachment != nil {
		add("emails.has_attachment = ?", *f.HasAttachment)
	}
	for col, v := range map[string]*string{"from_addr": f.From, "to_addr": f.To, "cc": f.Cc, "subject": f.Subject} {
		if v != nil {
			add("lower(emails."+col+") LIKE ? ESCAPE '\\'", likePattern(*v))
		}
	}
	if q, ok := f.upstreamPart(); ok {
		var mailboxes []string
		if f.InMailbox != nil {
			id, _ := resolveID(qb.call, *f.InMailbox)
			mailboxes = []string{id}
		}
		ids, err := qb.s.search(qb.ctx, qb.a, mailboxes, q)
		if err != nil {
			return sqlExpr{}, err
		}
		add("emails.id IN (SELECT value FROM json_each(?))", jsonList(ids))
	}
	if len(conds) == 0 {
		return sqlExpr{sql: "1"}, nil
	}
	return sqlExpr{sql: strings.Join(conds, " AND "), args: args}, nil
}

// keywordExpr tests a keyword on the emails row aliased tbl.
func keywordExpr(tbl, kw string) sqlExpr {
	kw = strings.ToLower(kw)
	switch kw {
	case "$seen", "$flagged", "$draft", "$answered":
		return sqlExpr{sql: tbl + "." + kw[1:] + " = 1"}
	}
	return sqlExpr{sql: "EXISTS (SELECT 1 FROM json_each(" + tbl + ".keywords) WHERE key = ?)", args: []any{kw}}
}

func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + strings.ToLower(r.Replace(s)) + "%"
}

func jsonList(ids []string) string {
	if ids == nil {
		ids = []string{}
	}
	b, _ := json.Marshal(ids)
	return string(b)
}

// maxSearchParallel is how many folders are searched on the server at once.
const maxSearchParallel = 4

// search runs a server-side search in mailboxes (all selectable ones when empty).
func (s *Service) search(ctx context.Context, a *account, mailboxes []string, q upstream.SearchQuery) ([]string, error) {
	if err := a.online(); err != nil {
		return nil, err
	}
	if len(mailboxes) == 0 {
		if err := a.idx.DB().Model(&model.IndexMailbox{}).Where("no_select = ?", false).Pluck("id", &mailboxes).Error; err != nil {
			return nil, err
		}
	}
	var mu sync.Mutex
	var out []string
	var outage, refused error
	answered := 0
	sem := make(chan struct{}, maxSearchParallel)
	var wg sync.WaitGroup
	for _, id := range mailboxes {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			ids, err := a.up.Search(ctx, id, q)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				answered++
				out = append(out, ids...)
			case isNotFound(err):
				answered++ // the folder went away since the last sync
			case upstream.IsKind(err, upstream.KindRejected):
				if refused == nil {
					refused = err
				}
				s.log.Warn("folder refused search", "account", a.id, "mailbox", id, "err", err)
			default:
				if outage == nil {
					outage = err
				}
			}
		}()
	}
	wg.Wait()
	if outage != nil {
		if me, ok := unavailable(outage); ok {
			return nil, me
		}
		return nil, outage
	}
	// One folder refusing (a stale or odd folder) does not fail the search;
	// every folder refusing means the server cannot run it.
	if refused != nil && answered == 0 {
		return nil, &jmapserver.MethodError{Type: "unsupportedFilter", Description: "The mail server cannot run this search."}
	}
	return out, nil
}

// sortExpr maps a comparator to an ORDER BY term.
func sortExpr(c comparator) (sqlExpr, error) {
	dir := " DESC"
	if c.ascending() {
		dir = " ASC"
	}
	first := func(col string) string {
		return "lower(COALESCE(NULLIF(json_extract(emails." + col + ", '$[0].name'), ''), json_extract(emails." + col + ", '$[0].email'), ''))"
	}
	switch c.Property {
	case "receivedAt":
		return sqlExpr{sql: "emails.received_at" + dir}, nil
	case "sentAt":
		return sqlExpr{sql: "COALESCE(emails.sent_at, emails.received_at)" + dir}, nil
	case "size":
		return sqlExpr{sql: "emails.size" + dir}, nil
	case "subject":
		return sqlExpr{sql: "lower(emails.subject)" + dir}, nil
	case "from":
		return sqlExpr{sql: first("from_addr") + dir}, nil
	case "to":
		return sqlExpr{sql: first("to_addr") + dir}, nil
	case "hasKeyword", "someInThreadHaveKeyword", "allInThreadHaveKeyword":
		if c.Keyword == "" {
			return sqlExpr{}, jmapserver.ErrUnsupportedSort
		}
		e := keywordExpr("emails", c.Keyword)
		t := keywordExpr("t", c.Keyword)
		switch c.Property {
		case "hasKeyword":
			return sqlExpr{sql: "(" + e.sql + ")" + dir, args: e.args}, nil
		case "someInThreadHaveKeyword":
			return sqlExpr{sql: "EXISTS (SELECT 1 FROM emails t WHERE t.thread_id = emails.thread_id AND t.orphaned_from = '' AND " + t.sql + ")" + dir, args: t.args}, nil
		default:
			return sqlExpr{sql: "NOT EXISTS (SELECT 1 FROM emails t WHERE t.thread_id = emails.thread_id AND t.orphaned_from = '' AND NOT " + t.sql + ")" + dir, args: t.args}, nil
		}
	}
	return sqlExpr{}, jmapserver.ErrUnsupportedSort
}

// maxQueryLimit caps a query page when the client gives no (or a larger) limit.
const maxQueryLimit = 10000

type emailQueryArgs struct {
	AccountID       string       `json:"accountId"`
	Filter          *emailFilter `json:"filter"`
	Sort            []comparator `json:"sort"`
	Position        int          `json:"position"`
	Anchor          *string      `json:"anchor"`
	AnchorOffset    int          `json:"anchorOffset"`
	Limit           *int         `json:"limit"`
	CalculateTotal  bool         `json:"calculateTotal"`
	CollapseThreads bool         `json:"collapseThreads"`
}

func (s *Service) emailQuery(ctx context.Context, call *jmapserver.Call) (any, error) {
	var args emailQueryArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		if strings.Contains(err.Error(), "unsupported filter property") {
			return nil, &jmapserver.MethodError{Type: "unsupportedFilter", Description: err.Error()}
		}
		return nil, jmapserver.InvalidArguments(err.Error())
	}
	if args.Limit != nil && *args.Limit < 0 {
		return nil, jmapserver.InvalidArguments("limit must not be negative")
	}
	a, err := s.open(call, args.AccountID)
	if err != nil {
		return nil, err
	}
	state := a.state(mailindex.TypeEmail)
	where := sqlExpr{sql: "1"}
	if args.Filter != nil {
		qb := &queryBuilder{ctx: ctx, s: s, a: a, call: call}
		if where, err = qb.build(args.Filter, 0); err != nil {
			return nil, err
		}
	}
	sorts := args.Sort
	if len(sorts) == 0 {
		f := false
		sorts = []comparator{{Property: "receivedAt", IsAscending: &f}}
	}
	var order []string
	var orderArgs []any
	for _, c := range sorts {
		e, err := sortExpr(c)
		if err != nil {
			return nil, err
		}
		order = append(order, e.sql)
		orderArgs = append(orderArgs, e.args...)
	}
	order = append(order, "emails.id DESC") // stable pages
	base := "FROM emails WHERE emails.orphaned_from = '' AND (" + where.sql + ")"
	orderBy := " ORDER BY " + strings.Join(order, ", ")
	limit := maxQueryLimit
	if args.Limit != nil {
		limit = min(*args.Limit, maxQueryLimit)
	}

	// Fast path: the page straight from SQL.
	if !args.CollapseThreads && args.Anchor == nil && args.Position >= 0 {
		var ids []string
		q := "SELECT emails.id " + base + orderBy + " LIMIT ? OFFSET ?"
		qargs := append(append(append([]any{}, where.args...), orderArgs...), limit, args.Position)
		if err := a.idx.DB().Raw(q, qargs...).Scan(&ids).Error; err != nil {
			return nil, err
		}
		if ids == nil {
			ids = []string{}
		}
		out := map[string]any{"accountId": a.id, "queryState": state, "canCalculateChanges": false,
			"position": args.Position, "ids": ids}
		if args.CalculateTotal {
			var total int64
			if err := a.idx.DB().Raw("SELECT COUNT(*) "+base, where.args...).Scan(&total).Error; err != nil {
				return nil, err
			}
			out["total"] = total
			if int64(args.Position) > total {
				out["position"] = total
			}
		}
		return out, nil
	}

	type row struct {
		ID       string
		ThreadID string
	}
	var rows []row
	q := "SELECT emails.id AS id, emails.thread_id AS thread_id " + base + orderBy
	if err := a.idx.DB().Raw(q, append(append([]any{}, where.args...), orderArgs...)...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, r := range rows {
		if args.CollapseThreads {
			if seen[r.ThreadID] {
				continue
			}
			seen[r.ThreadID] = true
		}
		ids = append(ids, r.ID)
	}
	return pageIDs(ids, args.Position, args.Anchor, args.AnchorOffset, &limit, args.CalculateTotal, a.id, state, false)
}
