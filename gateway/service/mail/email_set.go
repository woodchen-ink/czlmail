package mail

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

func (s *Service) emailSet(ctx context.Context, call *jmapserver.Call) (any, error) {
	var args setArgs
	if err := call.Decode(&args); err != nil {
		return nil, err
	}
	if err := tooLarge(&args); err != nil {
		return nil, err
	}
	a, err := s.open(call, args.AccountID)
	if err != nil {
		return nil, err
	}
	state := a.state(mailindex.TypeEmail)
	if err := checkState(args.IfInState, state); err != nil {
		return nil, err
	}
	resp := newSetResponse(a.id, state)
	if args.count() > 0 {
		if err := a.online(); err != nil {
			return nil, err
		}
	}
	cids := make([]string, 0, len(args.Create))
	for cid := range args.Create {
		cids = append(cids, cid)
	}
	sort.Strings(cids)
	for _, cid := range cids {
		if err := s.createEmail(ctx, call, a, resp, cid, args.Create[cid]); err != nil {
			return nil, err
		}
	}
	if len(args.Update) > 0 {
		if err := s.updateEmails(ctx, call, a, resp, args.Update); err != nil {
			return nil, err
		}
	}
	if len(args.Destroy) > 0 {
		if err := s.destroyEmails(ctx, call, a, resp, args.Destroy); err != nil {
			return nil, err
		}
	}
	resp.NewState = a.state(mailindex.TypeEmail)
	return resp, nil
}

// EmailPatch is the effect of an Email/set update on one email, resolved
// against the index: the keywords and mailbox to end up with.
type EmailPatch struct {
	Keywords  mailindex.Keywords
	MailboxID string
}

// patchState is one email's current keywords and mailboxes.
type patchState struct {
	keywords  mailindex.Keywords
	mailboxes []string
}

var errTooManyMailboxes = setErr("tooManyMailboxes", "an email is in exactly one mailbox on this server", "mailboxIds")

// ApplyEmailPatch applies an Email/set patch (RFC 8620 §5.3: whole keywords or
// mailboxIds, or "keywords/<k>" and "mailboxIds/<id>" pointers) to an email's
// current keywords and mailboxes. resolveMailbox maps a mailbox id or
// "#creationId" to an existing mailbox id, ok false when there is none.
// EmailSubmission/set onSuccessUpdateEmail (#41) reaches it through ImplicitEmailSet.
func applyEmailPatch(cur patchState, patch map[string]json.RawMessage, resolveMailbox func(string) (string, bool)) (*EmailPatch, *SetError) {
	kw := mailindex.Keywords{}
	for k, on := range cur.keywords {
		if on {
			kw[k] = true
		}
	}
	boxes := map[string]bool{}
	for _, m := range cur.mailboxes {
		boxes[m] = true
	}
	// Whole replacements first, then pointers into them (a patch may not mix
	// both for one property, RFC 8620 §5.3; applying in this order is lenient).
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int { return strings.Count(a, "/") - strings.Count(b, "/") })
	for _, k := range keys {
		raw := patch[k]
		switch {
		case k == "keywords":
			var m map[string]bool
			if json.Unmarshal(raw, &m) != nil {
				return nil, invalidProps("keywords must be an object of true values", "keywords")
			}
			kw = mailindex.Keywords{}
			for name, on := range m {
				if !on || !validKeyword(name) {
					return nil, invalidProps("invalid keyword", "keywords")
				}
				kw[strings.ToLower(name)] = true
			}
		case strings.HasPrefix(k, "keywords/"):
			name := unescapePointer(strings.TrimPrefix(k, "keywords/"))
			if !validKeyword(name) {
				return nil, invalidProps("invalid keyword", k)
			}
			switch string(raw) {
			case "true":
				kw[strings.ToLower(name)] = true
			case "null", "false":
				delete(kw, strings.ToLower(name))
			default:
				return nil, invalidProps("a keyword is set with true and removed with null", k)
			}
		case k == "mailboxIds":
			var m map[string]bool
			if json.Unmarshal(raw, &m) != nil {
				return nil, invalidProps("mailboxIds must be an object of true values", "mailboxIds")
			}
			boxes = map[string]bool{}
			for id, on := range m {
				real, ok := resolveMailbox(id)
				if !on || !ok {
					return nil, invalidProps("unknown mailbox", "mailboxIds")
				}
				boxes[real] = true
			}
		case strings.HasPrefix(k, "mailboxIds/"):
			id := unescapePointer(strings.TrimPrefix(k, "mailboxIds/"))
			real, ok := resolveMailbox(id)
			switch string(raw) {
			case "true":
				if !ok {
					return nil, invalidProps("unknown mailbox", k)
				}
				boxes[real] = true
			case "null", "false":
				if ok {
					delete(boxes, real)
				}
			default:
				return nil, invalidProps("a mailbox is added with true and removed with null", k)
			}
		default:
			return nil, invalidProps("only keywords and mailboxIds can be changed", k)
		}
	}
	if len(boxes) == 0 {
		return nil, invalidProps("an email must be in a mailbox", "mailboxIds")
	}
	if len(boxes) > 1 {
		return nil, errTooManyMailboxes
	}
	out := &EmailPatch{Keywords: kw}
	for id := range boxes {
		out.MailboxID = id
	}
	return out, nil
}

func unescapePointer(s string) string {
	return strings.NewReplacer("~1", "/", "~0", "~").Replace(s)
}

// validKeyword follows RFC 8621 §4.1.1 (IMAP atom characters, 1..255).
func validKeyword(k string) bool {
	if k == "" || len(k) > 255 {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if c <= 0x20 || c >= 0x7f || strings.IndexByte(`()]{%*"\`, c) >= 0 {
			return false
		}
	}
	return true
}

func (s *Service) mailboxResolver(call *jmapserver.Call, a *account) (func(string) (string, bool), error) {
	var ids []string
	if err := a.idx.DB().Model(&model.IndexMailbox{}).Where("no_select = ?", false).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, id := range ids {
		known[id] = true
	}
	return func(id string) (string, bool) {
		real, ok := resolveID(call, id)
		return real, ok && known[real]
	}, nil
}

// updateEmails applies Email/set updates, batching identical keyword changes
// and moves to the same mailbox into one upstream call each.
func (s *Service) updateEmails(ctx context.Context, call *jmapserver.Call, a *account, resp *setResponse,
	updates map[string]map[string]json.RawMessage) error {
	resolve, err := s.mailboxResolver(call, a)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(updates))
	realOf := map[string]string{}
	for id := range updates {
		real, _ := resolveID(call, id)
		realOf[id] = real
		ids = append(ids, real)
	}
	msgs, err := loadMessages(a.idx, ids)
	if err != nil {
		return err
	}
	type kwKey struct{ add, remove string }
	moves := map[string][]string{} // target mailbox → client ids
	kwOps := map[kwKey][]string{}  // keyword change → client ids
	kwSets := map[kwKey][2][]string{}
	wroteBefore := resp.wrote
	var order []string
	for id := range updates {
		order = append(order, id)
	}
	sort.Strings(order)
	for _, id := range order {
		m := msgs[realOf[id]]
		if m == nil {
			resp.notUpdated(id, errNotFound)
			continue
		}
		cur := patchState{keywords: mailindex.ParseKeywords(m.row.Keywords), mailboxes: m.mailboxes}
		p, se := applyEmailPatch(cur, updates[id], resolve)
		if se != nil {
			resp.notUpdated(id, se)
			continue
		}
		if !slices.Contains(m.mailboxes, p.MailboxID) || len(m.mailboxes) != 1 {
			moves[p.MailboxID] = append(moves[p.MailboxID], id)
		}
		var add, remove []string
		for k := range p.Keywords {
			if !cur.keywords[k] {
				add = append(add, k)
			}
		}
		for k, on := range cur.keywords {
			if on && !p.Keywords[k] {
				remove = append(remove, k)
			}
		}
		sort.Strings(add)
		sort.Strings(remove)
		if len(add)+len(remove) > 0 {
			key := kwKey{strings.Join(add, " "), strings.Join(remove, " ")}
			kwOps[key] = append(kwOps[key], id)
			kwSets[key] = [2][]string{add, remove}
		}
		resp.updated(id, nil) // provisional: withdrawn below if an upstream call fails
	}
	resp.wrote = wroteBefore
	failed := map[string]bool{}
	moved := map[string]bool{}
	fail := func(clientIDs []string, err error, refused *SetError) error {
		for _, id := range clientIDs {
			delete(resp.Updated, id)
		}
		se, ferr := resp.upstreamFailure(err, refused)
		if ferr != nil {
			return ferr
		}
		for _, id := range clientIDs {
			failed[id] = true
			e := se
			if moved[id] {
				// The email did move; only its keywords are as before.
				e = &SetError{Type: se.Type, Properties: []string{"keywords"},
					Description: "The email was moved, but its keywords were not changed."}
			}
			resp.notUpdated(id, e)
		}
		return nil
	}
	refused := setErr("forbidden", "the mail server refused the change")
	targets := make([]string, 0, len(moves))
	for t := range moves {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	for _, target := range targets {
		clientIDs := moves[target]
		if _, err := a.up.Move(ctx, realIDs(clientIDs, realOf), target); err != nil {
			if isNotFound(err) {
				// The target mailbox (or an email) went away since the patch was checked.
				for _, id := range clientIDs {
					delete(resp.Updated, id)
					failed[id] = true
					resp.notUpdated(id, invalidProps("unknown mailbox", "mailboxIds"))
				}
				continue
			}
			if ferr := fail(clientIDs, err, refused); ferr != nil {
				return ferr
			}
			continue
		}
		for _, id := range clientIDs {
			moved[id] = true
		}
		resp.wrote = true
	}
	keys := make([]kwKey, 0, len(kwOps))
	for k := range kwOps {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].add+"|"+keys[i].remove < keys[j].add+"|"+keys[j].remove })
	for _, key := range keys {
		var clientIDs []string
		for _, id := range kwOps[key] {
			if !failed[id] {
				clientIDs = append(clientIDs, id)
			}
		}
		if len(clientIDs) == 0 {
			continue
		}
		set := kwSets[key]
		if _, err := a.up.SetKeywords(ctx, realIDs(clientIDs, realOf), set[0], set[1]); err != nil {
			if ferr := fail(clientIDs, err, refused); ferr != nil {
				return ferr
			}
			continue
		}
		resp.wrote = true
	}
	return nil
}

// ImplicitEmailSet runs the update and destroy parts of an Email/set for
// another method (EmailSubmission/set onSuccessUpdateEmail and
// onSuccessDestroyEmail, RFC 8621 §7.5) and returns the Email/set response to
// add with call.AddResponse. Ids may be "#creationId" references.
func (s *Service) ImplicitEmailSet(ctx context.Context, call *jmapserver.Call, accountID string,
	update map[string]map[string]json.RawMessage, destroy []string) (any, error) {
	a, err := s.lookup(accountID)
	if err != nil {
		return nil, err
	}
	if err := a.online(); err != nil {
		return nil, err
	}
	resp := newSetResponse(a.id, a.state(mailindex.TypeEmail))
	if len(update) > 0 {
		if err := s.updateEmails(ctx, call, a, resp, update); err != nil {
			return nil, err
		}
	}
	if len(destroy) > 0 {
		if err := s.destroyEmails(ctx, call, a, resp, destroy); err != nil {
			return nil, err
		}
	}
	resp.NewState = a.state(mailindex.TypeEmail)
	return resp, nil
}

func realIDs(clientIDs []string, realOf map[string]string) []string {
	out := make([]string, len(clientIDs))
	for i, id := range clientIDs {
		out[i] = realOf[id]
	}
	return out
}

func (s *Service) destroyEmails(ctx context.Context, call *jmapserver.Call, a *account, resp *setResponse, destroy []string) error {
	var clientIDs, ids []string
	for _, id := range dedupe(destroy) {
		real, _ := resolveID(call, id)
		clientIDs = append(clientIDs, id)
		ids = append(ids, real)
	}
	msgs, err := loadMessages(a.idx, ids)
	if err != nil {
		return err
	}
	var okClient, okReal []string
	for i, id := range ids {
		if msgs[id] == nil {
			resp.notDestroyed(clientIDs[i], errNotFound)
			continue
		}
		okClient, okReal = append(okClient, clientIDs[i]), append(okReal, id)
	}
	if len(okReal) == 0 {
		return nil
	}
	if _, err := a.up.Destroy(ctx, okReal); err != nil {
		se, ferr := resp.upstreamFailure(err, setErr("forbidden", "the mail server refused to delete the email"))
		if ferr != nil {
			return ferr
		}
		for _, id := range okClient {
			resp.notDestroyed(id, se)
		}
		return nil
	}
	for _, id := range okClient {
		resp.destroyed(id)
	}
	return nil
}
