package syncer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/pim"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 日历、通讯录、文件的同步。
//
// 与邮件不同, 这几类对象总量有限(几千条量级), 首次直接全量引导, 之后走 /changes。
// 容器类型(Calendar / AddressBook)每次变化都整表重取: 数量以个计, 全量比算增量简单可靠。
//
// 共享账号可能没有这几类权限(Stalwart 可用角色禁掉), 也可能根本不公布该能力;
// 单个类型失败只记日志, 不影响邮件与其它类型。

// pimKind 描述一类对象如何落库。
type pimKind struct {
	typeName string
	dataType store.DataType
	table    string
	// container 为真表示整表替换, 不走 /changes。
	container bool
	apply     func(ctx context.Context, tx *sql.Tx, accountID string, list []json.RawMessage) error
}

var pimKinds = []pimKind{
	{typeName: jmapx.Calendar, dataType: store.TypeCalendar, table: store.TableCalendars, container: true, apply: applyCalendars},
	{typeName: jmapx.CalendarEvent, dataType: store.TypeCalendarEvent, table: store.TableEvents, apply: applyEvents},
	{typeName: jmapx.AddressBook, dataType: store.TypeAddressBook, table: store.TableAddressBooks, container: true, apply: applyAddressBooks},
	{typeName: jmapx.ContactCard, dataType: store.TypeContactCard, table: store.TableContacts, apply: applyContacts},
	{typeName: jmapx.FileNode, dataType: store.TypeFileNode, table: store.TableFileNodes, apply: applyFileNodes},
}

func pimKindFor(dt store.DataType) (pimKind, bool) {
	for _, k := range pimKinds {
		if k.dataType == dt {
			return k, true
		}
	}
	return pimKind{}, false
}

// accountSupports 报告账号是否公布了某能力。
func (s *Syncer) accountSupports(accountID string, uri jmap.URI) bool {
	if s.client.Session == nil {
		return false
	}
	acc, ok := s.client.Session.Accounts[jmap.ID(accountID)]
	if !ok {
		return false
	}
	_, ok = acc.RawCapabilities[uri]
	return ok
}

// syncPIM 同步一个账号的全部 PIM 类型。调用方持有 s.mu。
func (s *Syncer) syncPIM(ctx context.Context, accountID string) {
	for _, k := range pimKinds {
		if err := s.syncPIMKind(ctx, accountID, k); err != nil {
			s.log.Warn("sync pim", "account", accountID, "type", k.typeName, "err", err)
		}
	}
}

func (s *Syncer) syncPIMKind(ctx context.Context, accountID string, k pimKind) error {
	if !s.accountSupports(accountID, jmapx.URIFor(k.typeName)) {
		return nil
	}
	local, err := s.store.State(ctx, accountID, k.dataType)
	if err != nil {
		return err
	}
	if local == "" || k.container {
		return s.bootstrapPIM(ctx, accountID, k)
	}

	for {
		req := &jmap.Request{Context: ctx}
		req.Invoke(jmapx.Changes(k.typeName, accountID, local, s.maxObjectsInGet))
		resp, err := s.do(req)
		if err != nil {
			if methodErrorType(err) == errCannotCalculateChanges {
				return s.bootstrapPIM(ctx, accountID, k)
			}
			return err
		}
		ch, ok := resp.Responses[0].Args.(*jmapx.ChangesResponse)
		if !ok {
			return &Error{Code: CodeUnhandled, Msg: "unexpected response to " + k.typeName + "/changes"}
		}

		touched := append(append([]string{}, ch.Created...), ch.Updated...)
		objs, err := s.getPIM(ctx, accountID, k.typeName, touched)
		if err != nil {
			return err
		}
		if err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
			if err := k.apply(ctx, tx, accountID, objs); err != nil {
				return err
			}
			if err := store.DeleteObjects(ctx, tx, k.table, accountID, ch.Destroyed); err != nil {
				return err
			}
			return store.SetState(ctx, tx, accountID, k.dataType, ch.NewState)
		}); err != nil {
			return err
		}

		local = ch.NewState
		if !ch.HasMoreChanges {
			return nil
		}
	}
}

// bootstrapPIM 全量引导。先用空 ids 的 /get 取当前 state 再分页拉取:
// 拉取期间发生的变更会在下一次 /changes 里重放, 重放是幂等的。
func (s *Syncer) bootstrapPIM(ctx context.Context, accountID string, k pimKind) error {
	req := &jmap.Request{Context: ctx}
	if k.container {
		req.Invoke(jmapx.Get(k.typeName, accountID, nil))
	} else {
		req.Invoke(jmapx.Get(k.typeName, accountID, []string{}))
		req.Invoke(&jmapx.Call{Method: k.typeName + "/query", Args: map[string]any{"accountId": accountID}})
	}
	resp, err := s.do(req)
	if err != nil {
		return err
	}
	got, ok := resp.Responses[0].Args.(*jmapx.GetResponse)
	if !ok {
		return &Error{Code: CodeUnhandled, Msg: "unexpected response to " + k.typeName + "/get"}
	}
	state := got.State
	objs := got.List

	if !k.container {
		q, ok := resp.Responses[1].Args.(*jmapx.QueryResponse)
		if !ok {
			return &Error{Code: CodeUnhandled, Msg: "unexpected response to " + k.typeName + "/query"}
		}
		ids := q.IDs
		// 服务端可能对 query 结果分页, 取到 total 为止。
		for len(ids) < q.Total {
			more := &jmap.Request{Context: ctx}
			more.Invoke(&jmapx.Call{Method: k.typeName + "/query", Args: map[string]any{
				"accountId": accountID, "position": len(ids),
			}})
			r, err := s.do(more)
			if err != nil {
				return err
			}
			page, ok := r.Responses[0].Args.(*jmapx.QueryResponse)
			if !ok || len(page.IDs) == 0 {
				break
			}
			ids = append(ids, page.IDs...)
		}
		if objs, err = s.getPIM(ctx, accountID, k.typeName, ids); err != nil {
			return err
		}
	}

	s.log.Info("bootstrapped pim", "account", accountID, "type", k.typeName, "count", len(objs))
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if !k.container {
			if err := store.ClearObjects(ctx, tx, k.table, accountID); err != nil {
				return err
			}
		}
		if err := k.apply(ctx, tx, accountID, objs); err != nil {
			return err
		}
		return store.SetState(ctx, tx, accountID, k.dataType, state)
	})
}

// getPIM 按批次取对象原文。
func (s *Syncer) getPIM(ctx context.Context, accountID, typeName string, ids []string) ([]json.RawMessage, error) {
	var out []json.RawMessage
	size := s.maxObjectsInGet
	for start := 0; start < len(ids); start += size {
		end := min(start+size, len(ids))
		req := &jmap.Request{Context: ctx}
		req.Invoke(jmapx.Get(typeName, accountID, ids[start:end]))
		resp, err := s.do(req)
		if err != nil {
			return nil, err
		}
		got, ok := resp.Responses[0].Args.(*jmapx.GetResponse)
		if !ok {
			return nil, &Error{Code: CodeUnhandled, Msg: "unexpected response to " + typeName + "/get"}
		}
		out = append(out, got.List...)
	}
	return out, nil
}

func applyCalendars(ctx context.Context, tx *sql.Tx, accountID string, list []json.RawMessage) error {
	cals := make([]store.Calendar, 0, len(list))
	for _, raw := range list {
		var c struct {
			ID           string          `json:"id"`
			Name         string          `json:"name"`
			Description  *string         `json:"description"`
			Color        *string         `json:"color"`
			SortOrder    int64           `json:"sortOrder"`
			IsVisible    *bool           `json:"isVisible"`
			IsSubscribed *bool           `json:"isSubscribed"`
			IsDefault    bool            `json:"isDefault"`
			MyRights     map[string]bool `json:"myRights"`
			WithTime     json.RawMessage `json:"defaultAlertsWithTime"`
			WithoutTime  json.RawMessage `json:"defaultAlertsWithoutTime"`
		}
		if json.Unmarshal(raw, &c) != nil {
			continue
		}
		cals = append(cals, store.Calendar{
			DefaultAlertsWithTime: string(c.WithTime), DefaultAlertsWithoutTime: string(c.WithoutTime),
			ID: c.ID, Name: c.Name, Description: deref(c.Description), Color: deref(c.Color),
			SortOrder: c.SortOrder, IsVisible: c.IsVisible == nil || *c.IsVisible,
			IsSubscribed: c.IsSubscribed == nil || *c.IsSubscribed, IsDefault: c.IsDefault, MyRights: c.MyRights,
		})
	}
	return store.ReplaceCalendars(ctx, tx, accountID, cals)
}

func applyAddressBooks(ctx context.Context, tx *sql.Tx, accountID string, list []json.RawMessage) error {
	books := make([]store.AddressBook, 0, len(list))
	for _, raw := range list {
		var b struct {
			ID           string          `json:"id"`
			Name         string          `json:"name"`
			Description  *string         `json:"description"`
			SortOrder    int64           `json:"sortOrder"`
			IsSubscribed *bool           `json:"isSubscribed"`
			IsDefault    bool            `json:"isDefault"`
			MyRights     map[string]bool `json:"myRights"`
		}
		if json.Unmarshal(raw, &b) != nil {
			continue
		}
		books = append(books, store.AddressBook{
			ID: b.ID, Name: b.Name, Description: deref(b.Description), SortOrder: b.SortOrder,
			IsSubscribed: b.IsSubscribed == nil || *b.IsSubscribed, IsDefault: b.IsDefault, MyRights: b.MyRights,
		})
	}
	return store.ReplaceAddressBooks(ctx, tx, accountID, books)
}

func applyEvents(ctx context.Context, tx *sql.Tx, accountID string, list []json.RawMessage) error {
	rows := make([]store.EventRow, 0, len(list))
	for _, raw := range list {
		row, err := EventRowFrom(raw)
		if err != nil {
			continue
		}
		rows = append(rows, row)
	}
	return store.UpsertEvents(ctx, tx, accountID, rows)
}

// EventRowFrom 把事件原文转成落库形态。创建/修改事件后本地回写也用它。
func EventRowFrom(raw []byte) (store.EventRow, error) {
	e, err := pim.ParseEvent(raw)
	if err != nil {
		return store.EventRow{}, err
	}
	if e.ID == "" {
		return store.EventRow{}, errors.New("event without id")
	}
	start, end, err := e.Span()
	if err != nil {
		// 解析不出开始时间的事件仍然落库(可被搜索到), 只是不会出现在日程视图里。
		start = 0
		zero := int64(0)
		end = &zero
	}
	tz := ""
	if e.TimeZone != nil {
		tz = *e.TimeZone
	}
	return store.EventRow{
		ID: e.ID, CalendarIDs: e.CalendarIDList(), UID: e.UID, Title: e.Title,
		Description: e.Description, Location: e.Location(), StartAt: start, EndAt: end,
		AllDay: e.ShowWithoutTime, TimeZone: tz, Recurring: e.IsRecurring(), Status: e.Status,
		Raw: string(raw), UpdatedAt: parseUpdated(e.Updated),
	}, nil
}

func applyContacts(ctx context.Context, tx *sql.Tx, accountID string, list []json.RawMessage) error {
	rows := make([]store.ContactRow, 0, len(list))
	for _, raw := range list {
		row, err := ContactRowFrom(raw)
		if err != nil {
			continue
		}
		rows = append(rows, row)
	}
	return store.UpsertContacts(ctx, tx, accountID, rows)
}

// ContactRowFrom 把联系人原文转成落库形态。
func ContactRowFrom(raw []byte) (store.ContactRow, error) {
	c, err := pim.ParseCard(raw)
	if err != nil {
		return store.ContactRow{}, err
	}
	if c.ID == "" {
		return store.ContactRow{}, errors.New("card without id")
	}
	var emails []store.ContactEmailRow
	for _, e := range c.EmailList() {
		emails = append(emails, store.ContactEmailRow{Email: e.Address, Label: e.Label})
	}
	var phones []string
	for _, p := range c.PhoneList() {
		phones = append(phones, p.Number)
	}
	var extra struct {
		Keywords map[string]bool `json:"keywords"`
		Media    map[string]struct {
			Kind string `json:"kind"`
		} `json:"media"`
	}
	_ = json.Unmarshal(raw, &extra)
	var keywords []string
	for k, on := range extra.Keywords {
		if on {
			keywords = append(keywords, k)
		}
	}
	sort.Strings(keywords)
	hasPhoto := false
	for _, m := range extra.Media {
		hasPhoto = hasPhoto || m.Kind == "photo"
	}
	return store.ContactRow{
		Keywords: keywords, HasPhoto: hasPhoto,
		ID: c.ID, AddressBookIDs: c.AddressBookIDList(), UID: c.UID, Kind: c.Kind,
		DisplayName: c.DisplayName(), SortName: c.SortName(), Emails: emails, Phones: phones,
		Organization: c.Organization(), Raw: string(raw), UpdatedAt: parseUpdated(c.Updated),
	}, nil
}

func applyFileNodes(ctx context.Context, tx *sql.Tx, accountID string, list []json.RawMessage) error {
	nodes := make([]store.FileNode, 0, len(list))
	raws := make([]string, 0, len(list))
	for _, raw := range list {
		n, err := FileNodeFrom(raw)
		if err != nil {
			continue
		}
		nodes = append(nodes, n)
		raws = append(raws, string(raw))
	}
	return store.UpsertFileNodes(ctx, tx, accountID, nodes, raws)
}

// FileNodeFrom 解析文件节点。
func FileNodeFrom(raw []byte) (store.FileNode, error) {
	var n struct {
		ID       string          `json:"id"`
		ParentID *string         `json:"parentId"`
		NodeType string          `json:"nodeType"`
		BlobID   *string         `json:"blobId"`
		Size     int64           `json:"size"`
		Name     string          `json:"name"`
		Type     *string         `json:"type"`
		Created  string          `json:"created"`
		Modified string          `json:"modified"`
		Role     *string         `json:"role"`
		MyRights map[string]bool `json:"myRights"`
	}
	if err := json.Unmarshal(raw, &n); err != nil {
		return store.FileNode{}, err
	}
	if n.ID == "" {
		return store.FileNode{}, errors.New("file node without id")
	}
	// 有的实现不给 nodeType, 以"没有 blob"判定目录。
	isDir := n.NodeType == "directory" || n.NodeType == "folder" || (n.NodeType == "" && deref(n.BlobID) == "")
	return store.FileNode{
		ID: n.ID, ParentID: deref(n.ParentID), Name: n.Name, IsDirectory: isDir,
		ContentType: deref(n.Type), BlobID: deref(n.BlobID), Size: n.Size,
		Created: parseUpdated(n.Created), Modified: parseUpdated(n.Modified),
		Role: deref(n.Role), MyRights: n.MyRights,
	}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func parseUpdated(s string) int64 {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix()
	}
	return 0
}
