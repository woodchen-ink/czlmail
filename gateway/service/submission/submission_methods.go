package submission

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

var submissionProperties = map[string]bool{
	"id": true, "identityId": true, "emailId": true, "threadId": true, "envelope": true, "sendAt": true,
	"undoStatus": true, "deliveryStatus": true, "dsnBlobIds": true, "mdnBlobIds": true,
}

func submissionView(r *model.EmailSubmission) map[string]any {
	var env, status any
	_ = json.Unmarshal([]byte(r.Envelope), &env)
	_ = json.Unmarshal([]byte(r.DeliveryStatus), &status)
	return map[string]any{
		"id": r.PublicID, "identityId": r.IdentityID, "emailId": r.EmailID, "threadId": r.ThreadID,
		"envelope": env, "sendAt": r.SendAt.UTC().Format(time.RFC3339), "undoStatus": r.UndoStatus,
		"deliveryStatus": status, "dsnBlobIds": []string{}, "mdnBlobIds": []string{},
	}
}

func (s *Service) submissionState(accountID string) (string, error) {
	return s.changes.state(s.db, accountID, TypeEmailSubmission)
}

func (s *Service) submissionGet(_ context.Context, call *jmapserver.Call) (any, error) {
	var a getArgs
	if err := call.Decode(&a); err != nil {
		return nil, err
	}
	if _, err := call.Account(a.AccountID); err != nil {
		return nil, err
	}
	if err := validProperties(a.Properties, submissionProperties); err != nil {
		return nil, err
	}
	q := s.db.Where("account_id = ?", a.AccountID)
	if a.IDs != nil {
		if len(*a.IDs) > jmapserver.MaxObjectsInGet {
			return nil, jmapserver.ErrRequestTooLarge
		}
		q = q.Where("public_id IN ?", *a.IDs)
	}
	var rows []model.EmailSubmission
	if err := q.Order("id").Limit(jmapserver.MaxObjectsInGet).Find(&rows).Error; err != nil {
		return nil, err
	}
	state, err := s.submissionState(a.AccountID)
	if err != nil {
		return nil, err
	}
	list := []map[string]any{}
	found := map[string]bool{}
	for i := range rows {
		found[rows[i].PublicID] = true
		list = append(list, project(submissionView(&rows[i]), a.Properties))
	}
	notFound := []string{}
	if a.IDs != nil {
		for _, id := range *a.IDs {
			if !found[id] {
				notFound = append(notFound, id)
			}
		}
	}
	return map[string]any{"accountId": a.AccountID, "state": state, "list": list, "notFound": notFound}, nil
}

func (s *Service) submissionChanges(_ context.Context, call *jmapserver.Call) (any, error) {
	return s.changesOf(call, TypeEmailSubmission)
}

type submissionFilter struct {
	IdentityIDs []string `json:"identityIds"`
	EmailIDs    []string `json:"emailIds"`
	ThreadIDs   []string `json:"threadIds"`
	UndoStatus  *string  `json:"undoStatus"`
	Before      *string  `json:"before"`
	After       *string  `json:"after"`
	Operator    *string  `json:"operator"`
}

type comparator struct {
	Property    string `json:"property"`
	IsAscending *bool  `json:"isAscending"`
}

type queryArgs struct {
	AccountID      string            `json:"accountId"`
	Filter         *submissionFilter `json:"filter"`
	Sort           []comparator      `json:"sort"`
	Position       int               `json:"position"`
	Anchor         *string           `json:"anchor"`
	AnchorOffset   int               `json:"anchorOffset"`
	Limit          *int              `json:"limit"`
	CalculateTotal bool              `json:"calculateTotal"`
}

func (s *Service) submissionQuery(_ context.Context, call *jmapserver.Call) (any, error) {
	var a queryArgs
	if err := call.Decode(&a); err != nil {
		return nil, err
	}
	if _, err := call.Account(a.AccountID); err != nil {
		return nil, err
	}
	q := s.db.Model(&model.EmailSubmission{}).Where("account_id = ?", a.AccountID)
	if f := a.Filter; f != nil {
		if f.Operator != nil {
			return nil, jmapserver.ErrUnsupportedFilter
		}
		if f.IdentityIDs != nil {
			q = q.Where("identity_id IN ?", f.IdentityIDs)
		}
		if f.EmailIDs != nil {
			q = q.Where("email_id IN ?", f.EmailIDs)
		}
		if f.ThreadIDs != nil {
			q = q.Where("thread_id IN ?", f.ThreadIDs)
		}
		if f.UndoStatus != nil {
			q = q.Where("undo_status = ?", *f.UndoStatus)
		}
		for _, c := range []struct {
			v  *string
			op string
		}{{f.Before, "send_at < ?"}, {f.After, "send_at >= ?"}} {
			if c.v == nil {
				continue
			}
			t, err := time.Parse(time.RFC3339, *c.v)
			if err != nil {
				return nil, jmapserver.InvalidArguments("invalid UTCDate " + *c.v)
			}
			q = q.Where(c.op, t.UTC())
		}
	}
	order := []string{}
	for _, c := range a.Sort {
		col := map[string]string{"emailId": "email_id", "threadId": "thread_id", "sentAt": "send_at"}[c.Property]
		if col == "" {
			return nil, jmapserver.ErrUnsupportedSort
		}
		dir := " ASC"
		if c.IsAscending != nil && !*c.IsAscending {
			dir = " DESC"
		}
		order = append(order, col+dir)
	}
	order = append(order, "id ASC")
	var ids []string
	if err := q.Order(strings.Join(order, ", ")).Pluck("public_id", &ids).Error; err != nil {
		return nil, err
	}
	pos := a.Position
	if a.Anchor != nil {
		i := slices.Index(ids, *a.Anchor)
		if i < 0 {
			return nil, jmapserver.ErrAnchorNotFound
		}
		pos = max(i+a.AnchorOffset, 0)
	} else if pos < 0 {
		pos = max(len(ids)+pos, 0)
	}
	pos = min(pos, len(ids))
	page := ids[pos:]
	if a.Limit != nil {
		if *a.Limit < 0 {
			return nil, jmapserver.InvalidArguments("limit must not be negative")
		}
		page = page[:min(*a.Limit, len(page))]
	}
	state, err := s.submissionState(a.AccountID)
	if err != nil {
		return nil, err
	}
	resp := map[string]any{"accountId": a.AccountID, "queryState": state, "canCalculateChanges": false, "position": pos, "ids": page}
	if a.CalculateTotal {
		resp["total"] = len(ids)
	}
	return resp, nil
}

func (s *Service) submissionSet(ctx context.Context, call *jmapserver.Call) (any, error) {
	var a setArgs
	if err := call.Decode(&a); err != nil {
		return nil, err
	}
	if _, err := call.Account(a.AccountID); err != nil {
		return nil, err
	}
	if err := checkLimit(len(a.Create) + len(a.Update) + len(a.Destroy)); err != nil {
		return nil, err
	}
	old, err := s.submissionState(a.AccountID)
	if err != nil {
		return nil, err
	}
	if a.IfInState != nil && *a.IfInState != old {
		return nil, jmapserver.ErrStateMismatch
	}
	resp := &setResponse{AccountID: a.AccountID, OldState: &old}

	var t *sendTarget
	if len(a.Create) > 0 {
		h, uerr := s.mgr.Lookup(a.AccountID)
		if uerr != nil {
			return nil, jmapserver.ServerUnavailable(uerr)
		}
		var row model.Upstream
		if err := s.db.Where("public_id = ?", a.AccountID).First(&row).Error; err != nil {
			return nil, err
		}
		t = &sendTarget{accountID: a.AccountID, handle: h, row: &row}
	}

	// Plans for the implicit Email/set, keyed by what the client used: "#cid" or an id.
	plans := map[string]*emailPlan{}
	for key, patch := range a.OnSuccessUpdateEmail {
		plans[key] = &emailPlan{s: s, patch: patch}
	}
	for _, key := range a.OnSuccessDestroyEmail {
		if _, dup := plans[key]; !dup {
			plans[key] = &emailPlan{s: s, destroy: true}
		}
	}

	type sent struct {
		row  *model.EmailSubmission
		plan *emailPlan
	}
	var done []sent
	cids := slices.Sorted(maps.Keys(a.Create))
	for _, cid := range cids {
		var args createArgs
		if err := json.Unmarshal(a.Create[cid], &args); err != nil {
			resp.failCreate(cid, invalidProperties("The create object is not valid."))
			continue
		}
		plan := plans["#"+cid]
		row, dup, serr := s.create(ctx, t, call.CreatedIDs, &args, plan)
		if serr != nil {
			resp.failCreate(cid, serr)
			continue
		}
		call.CreatedIDs[cid] = row.PublicID
		v := map[string]any{"id": row.PublicID, "threadId": row.ThreadID, "sendAt": row.SendAt.UTC().Format(time.RFC3339),
			"undoStatus": row.UndoStatus, "dsnBlobIds": []string{}, "mdnBlobIds": []string{}}
		var status any
		_ = json.Unmarshal([]byte(row.DeliveryStatus), &status)
		v["deliveryStatus"] = status
		resp.create(cid, v)
		if plan == nil {
			plan = plans[row.PublicID]
			if plan != nil {
				plan.emailID = row.EmailID
			}
		}
		if dup {
			// Answered with the first submission, whose request filed the email.
			plan = nil
		}
		done = append(done, sent{row: row, plan: plan})
	}

	for id, patch := range a.Update {
		var row model.EmailSubmission
		if err := s.db.Where("account_id = ? AND public_id = ?", a.AccountID, id).First(&row).Error; err != nil {
			resp.failUpdate(id, newSetError("notFound", ""))
			continue
		}
		bad := ""
		for k, v := range patch {
			if k != "undoStatus" {
				bad = k
				break
			}
			var st string
			_ = json.Unmarshal(v, &st)
			if st != "canceled" && st != row.UndoStatus {
				bad = k
			}
		}
		switch {
		case bad != "":
			resp.failUpdate(id, invalidProperties("", bad))
		case len(patch) > 0 && string(patch["undoStatus"]) != `"`+row.UndoStatus+`"`:
			resp.failUpdate(id, newSetError("cannotUnsend", "The message has already been sent."))
		default:
			resp.update(id, nil)
		}
	}

	for _, id := range a.Destroy {
		var row model.EmailSubmission
		if err := s.db.Where("account_id = ? AND public_id = ?", a.AccountID, id).First(&row).Error; err != nil {
			resp.failDestroy(id, newSetError("notFound", ""))
			continue
		}
		err := s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Delete(&row).Error; err != nil {
				return err
			}
			return s.changes.record(tx, a.AccountID, TypeEmailSubmission, row.PublicID, kindDestroyed)
		})
		if err != nil {
			return nil, err
		}
		resp.Destroyed = append(resp.Destroyed, id)
	}

	if resp.NewState, err = s.submissionState(a.AccountID); err != nil {
		return nil, err
	}
	if resp.NewState != old {
		s.mgr.Publish(a.AccountID, map[string]string{TypeEmailSubmission: resp.NewState})
	}

	// The implicit Email/set (RFC 8621 §7.5) after every create was processed.
	var withPlan []*emailPlan
	for _, d := range done {
		if d.plan != nil {
			withPlan = append(withPlan, d.plan)
		}
	}
	if len(withPlan) > 0 {
		call.AddResponse("Email/set", s.applyPlans(ctx, t, withPlan))
	}
	return resp, nil
}

// applyPlans runs the onSuccess plans and builds the Email/set response.
func (s *Service) applyPlans(ctx context.Context, t *sendTarget, plans []*emailPlan) *setResponse {
	idx := t.handle.Upstream.Index()
	old, _ := idx.State(mailindex.TypeEmail)
	resp := &setResponse{AccountID: t.accountID, OldState: &old}
	for _, p := range plans {
		// The index's commit hook pushes the new Email / Mailbox / Thread states.
		_, serr := p.apply(ctx, t)
		switch {
		case p.destroy && serr == nil:
			resp.Destroyed = append(resp.Destroyed, p.emailID)
		case p.destroy:
			resp.failDestroy(p.emailID, serr)
		case serr == nil:
			resp.update(p.emailID, nil)
		default:
			resp.failUpdate(p.emailID, serr)
		}
	}
	resp.NewState, _ = idx.State(mailindex.TypeEmail)
	return resp
}
