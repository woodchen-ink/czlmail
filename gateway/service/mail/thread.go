package mail

import (
	"context"
	"encoding/json"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

func (s *Service) emailChanges(_ context.Context, call *jmapserver.Call) (any, error) {
	a, ch, err := s.changes(call, mailindex.TypeEmail)
	if err != nil {
		return nil, err
	}
	return toChangesResponse(a.id, ch), nil
}

func (s *Service) threadChanges(_ context.Context, call *jmapserver.Call) (any, error) {
	a, ch, err := s.changes(call, mailindex.TypeThread)
	if err != nil {
		return nil, err
	}
	return toChangesResponse(a.id, ch), nil
}

var threadProperties = []string{"id", "emailIds"}

func (s *Service) threadGet(_ context.Context, call *jmapserver.Call) (any, error) {
	var args getArgs
	if err := call.Decode(&args); err != nil {
		return nil, err
	}
	if args.IDs == nil {
		// Listing every thread is not useful and not required (RFC 8620 §5.1 allows a limit).
		return nil, jmapserver.ErrRequestTooLarge
	}
	if err := checkIDs(args.IDs); err != nil {
		return nil, err
	}
	props, err := pickProperties(args.Properties, threadProperties, threadProperties)
	if err != nil {
		return nil, err
	}
	a, err := s.open(call, args.AccountID)
	if err != nil {
		return nil, err
	}
	state := a.state(mailindex.TypeThread)
	ids := dedupe(*args.IDs)
	type row struct {
		ID       string
		ThreadID string
	}
	members := map[string][]string{}
	for part := range chunk(ids, 500) {
		var rows []row
		err := a.idx.DB().Table("emails").Select("id, thread_id").
			Where("thread_id IN ? AND orphaned_from = ''", part).Order("received_at, id").Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			members[r.ThreadID] = append(members[r.ThreadID], r.ID)
		}
	}
	list := []map[string]any{}
	notFound := []string{}
	for _, id := range ids {
		emails := members[id]
		if len(emails) == 0 {
			notFound = append(notFound, id)
			continue
		}
		o := map[string]any{"id": id}
		if props["emailIds"] {
			o["emailIds"] = emails
		}
		list = append(list, o)
	}
	return map[string]any{"accountId": a.id, "state": state, "list": list, "notFound": notFound}, nil
}

// SearchSnippet/get: subject and preview are returned unmarked when they hold a
// search term, null otherwise. Highlighting would need the body text, which is
// on the upstream; clients show the preview anyway.
func (s *Service) snippetGet(ctx context.Context, call *jmapserver.Call) (any, error) {
	var args struct {
		AccountID string          `json:"accountId"`
		Filter    json.RawMessage `json:"filter"`
		EmailIDs  []string        `json:"emailIds"`
	}
	if err := call.Decode(&args); err != nil {
		return nil, err
	}
	if len(args.EmailIDs) > jmapserver.MaxObjectsInGet {
		return nil, jmapserver.ErrRequestTooLarge
	}
	a, err := s.open(call, args.AccountID)
	if err != nil {
		return nil, err
	}
	var terms []string
	if len(args.Filter) > 0 && string(args.Filter) != "null" {
		var f emailFilter
		if err := json.Unmarshal(args.Filter, &f); err != nil {
			return nil, &jmapserver.MethodError{Type: "unsupportedFilter", Description: err.Error()}
		}
		terms = snippetTerms(&f)
	}
	msgs, err := loadMessages(a.idx, args.EmailIDs)
	if err != nil {
		return nil, err
	}
	list := []map[string]any{}
	notFound := []string{}
	for _, id := range args.EmailIDs {
		m := msgs[id]
		if m == nil {
			notFound = append(notFound, id)
			continue
		}
		list = append(list, map[string]any{
			"emailId": id,
			"subject": highlight(m.row.Subject, terms),
			"preview": highlight(m.row.Preview, terms),
		})
	}
	return map[string]any{"accountId": a.id, "list": list, "notFound": notFound}, nil
}

// snippetTerms collects the free-text terms of a filter.
func snippetTerms(f *emailFilter) []string {
	var out []string
	for _, v := range []*string{f.Text, f.Subject, f.Body} {
		if v != nil && *v != "" {
			out = append(out, *v)
		}
	}
	for _, c := range f.Conditions {
		if f.Operator != "NOT" {
			out = append(out, snippetTerms(c)...)
		}
	}
	return out
}
