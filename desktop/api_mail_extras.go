package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/pim"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 阅读邮件时的横幅: 退订(List-Unsubscribe)与日历邀请(text/calendar 附件)。

// LoadListHeaders 补查正文早已缓存、还没有退订信息的邮件。
func (a *App) LoadListHeaders(accountID, emailID string) (store.Unsubscribe, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return store.Unsubscribe{}, err
	}
	return s.FetchListHeaders(a.ctx, accountID, emailID)
}

// Unsubscribe 按邮件的退订头退订, 返回采用的方式: posted(一键退订) / sent(发退订邮件) / opened(打开网页)。
//
// 优先级按 RFC 8058: 有一键退订就直接 POST, 用户不必离开客户端; 其次发 mailto 退订邮件; 最后才打开网页。
func (a *App) Unsubscribe(accountID, emailID string) (string, error) {
	st, err := a.currentStore()
	if err != nil {
		return "", err
	}
	d, err := st.Email(a.ctx, accountID, emailID)
	if err != nil {
		return "", err
	}
	u := d.Unsubscribe
	if u == nil || *u == (store.Unsubscribe{}) {
		return "", fmt.Errorf("2220 this email has no unsubscribe link")
	}

	if u.OneClick {
		if err := oneClickUnsubscribe(a.ctx, u.HTTP); err == nil {
			return "posted", nil
		} else if u.Mailto == "" {
			return "", err
		}
	}
	if u.Mailto != "" {
		if err := a.sendUnsubscribeMail(accountID, d, u.Mailto); err != nil {
			return "", err
		}
		return "sent", nil
	}
	wruntime.BrowserOpenURL(a.ctx, u.HTTP)
	return "opened", nil
}

func oneClickUnsubscribe(ctx context.Context, target string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil {
		return fmt.Errorf("2221 unsubscribe: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("2221 unsubscribe: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("2221 unsubscribe: HTTP %d", resp.StatusCode)
	}
	return nil
}

// sendUnsubscribeMail 用收到这封邮件的地址发退订邮件, 没有对应身份时用第一个身份。
func (a *App) sendUnsubscribeMail(accountID string, d *store.EmailDetail, mailto string) error {
	req := parseMailto(mailto)
	if req.To == "" {
		return fmt.Errorf("2222 invalid unsubscribe address")
	}
	ids, err := a.ListIdentities(accountID)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fmt.Errorf("2223 no sender identity")
	}
	identity := ids[0]
	recipients := map[string]bool{}
	for _, list := range [][]store.Address{d.To, d.CC, d.BCC} {
		for _, addr := range list {
			recipients[strings.ToLower(addr.Email)] = true
		}
	}
	for _, id := range ids {
		if recipients[strings.ToLower(id.Email)] {
			identity = id
			break
		}
	}
	subject := req.Subject
	if subject == "" {
		subject = "unsubscribe"
	}
	body := req.Body
	if body == "" {
		body = "unsubscribe"
	}
	return a.SendEmail(ComposeRequest{
		AccountID: accountID, IdentityID: identity.ID,
		To:      []store.Address{{Email: req.To}},
		Subject: subject, TextBody: body,
	})
}

// Invite 是邮件里日历邀请的摘要。
type Invite struct {
	UID       string        `json:"uid"`
	Title     string        `json:"title"`
	Start     string        `json:"start"` // 本机 LocalDateTime
	End       string        `json:"end"`
	AllDay    bool          `json:"allDay"`
	Location  string        `json:"location"`
	Organizer string        `json:"organizer"`
	Cancelled bool          `json:"cancelled"`
	Recurring bool          `json:"recurring"`
	Attendees []Participant `json:"attendees"`
	// EventID 是日历里已有的同 UID 事件(服务器收到邀请时可能已自动放进日历)。
	EventID  string `json:"eventId"`
	MyStatus string `json:"myStatus"`
}

// inviteAttachment 找邮件里的 iCalendar 附件 blob。
func inviteAttachment(d *store.EmailDetail) string {
	for _, att := range d.Attachments {
		t := strings.ToLower(att.Type)
		if t == "text/calendar" || t == "application/ics" || strings.HasSuffix(strings.ToLower(att.Name), ".ics") {
			return att.BlobID
		}
	}
	return ""
}

// parseInvite 让服务器解析邀请附件, 返回第一个事件对象。
func (a *App) parseInvite(accountID, emailID string) (map[string]any, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	d, err := st.Email(a.ctx, accountID, emailID)
	if err != nil {
		return nil, err
	}
	blobID := inviteAttachment(d)
	if blobID == "" {
		return nil, nil
	}
	req := &jmap.Request{Context: a.ctx}
	req.Invoke(&jmapx.Call{Method: jmapx.CalendarEvent + "/parse", Args: map[string]any{
		"accountId": accountID, "blobIds": []string{blobID},
	}})
	resp, err := a.doJMAP(req)
	if err != nil {
		return nil, err
	}
	parsed, ok := resp.Responses[0].Args.(*jmapx.ParseResponse)
	if !ok {
		return nil, fmt.Errorf("2131 unexpected parse response")
	}
	raw, ok := parsed.Parsed[blobID]
	if !ok {
		return nil, nil
	}
	var objs []map[string]any
	if json.Unmarshal(raw, &objs) != nil {
		var one map[string]any
		if json.Unmarshal(raw, &one) != nil {
			return nil, nil
		}
		objs = []map[string]any{one}
	}
	for _, o := range objs {
		if t, _ := o["@type"].(string); !strings.EqualFold(t, "task") {
			return o, nil
		}
	}
	return nil, nil
}

// GetInvite 解析邮件里的日历邀请; 邮件没有邀请时返回 nil。
func (a *App) GetInvite(accountID, emailID string) (*Invite, error) {
	obj, err := a.parseInvite(accountID, emailID)
	if err != nil || obj == nil {
		return nil, err
	}
	raw, _ := json.Marshal(obj)
	e, err := pim.ParseEvent(raw)
	if err != nil {
		return nil, nil
	}
	inv := &Invite{
		UID: e.UID, Title: e.Title, AllDay: e.ShowWithoutTime, Location: e.Location(),
		Cancelled: strings.EqualFold(e.Status, "cancelled"), Recurring: e.IsRecurring(),
	}
	if t, err := e.StartTime(); err == nil {
		inv.Start = t.In(time.Local).Format(pim.LocalDateTime)
		inv.End = t.Add(e.DurationValue()).In(time.Local).Format(pim.LocalDateTime)
	}
	var full map[string]json.RawMessage
	_ = json.Unmarshal(raw, &full)
	inv.Attendees = participants(full["participants"])
	var organizer string
	_ = json.Unmarshal(full["organizerCalendarAddress"], &organizer)
	inv.Organizer = strings.TrimPrefix(strings.ToLower(organizer), "mailto:")
	for _, p := range inv.Attendees {
		if p.Role == "owner" && inv.Organizer == "" {
			inv.Organizer = strings.ToLower(p.Email)
		}
	}

	if st, err := a.currentStore(); err == nil && e.UID != "" {
		if id, err := st.EventIDByUID(a.ctx, accountID, e.UID); err == nil && id != "" {
			inv.EventID = id
			if d, err := a.GetEvent(accountID, id); err == nil {
				inv.MyStatus = d.MyStatus
			}
		}
	}
	return inv, nil
}

// AddInviteToCalendar 把邀请加入默认日历, 返回事件 id。已在日历里时直接返回已有的 id。
// status 非空时加入后立即回复邀请(接受/暂定/拒绝)。
func (a *App) AddInviteToCalendar(accountID, emailID, status string) (string, error) {
	inv, err := a.GetInvite(accountID, emailID)
	if err != nil {
		return "", err
	}
	if inv == nil {
		return "", fmt.Errorf("2224 this email has no calendar invitation")
	}
	eventID := inv.EventID
	if eventID == "" {
		obj, err := a.parseInvite(accountID, emailID)
		if err != nil || obj == nil {
			return "", fmt.Errorf("2224 this email has no calendar invitation")
		}
		calID, err := a.defaultCalendar(accountID)
		if err != nil {
			return "", err
		}
		delete(obj, "id")
		obj["calendarIds"] = map[string]bool{calID: true}
		s, err := a.currentSyncer()
		if err != nil {
			return "", err
		}
		// 加入日历本身不发调度消息, 否则服务器可能把自己当组织者重新发邀请。
		created, err := s.SetPIM(a.ctx, accountID, jmapx.CalendarEvent, map[string]any{"inv": obj}, nil, nil,
			map[string]any{"sendSchedulingMessages": false})
		if err != nil {
			return "", err
		}
		eventID = created["inv"]
	}
	if status != "" && eventID != "" {
		if err := a.RespondEvent(accountID, eventID, status); err != nil {
			return eventID, err
		}
	}
	return eventID, nil
}

func (a *App) defaultCalendar(accountID string) (string, error) {
	st, err := a.currentStore()
	if err != nil {
		return "", err
	}
	cals, err := st.Calendars(a.ctx)
	if err != nil {
		return "", err
	}
	first := ""
	for _, c := range cals {
		if c.AccountID != accountID {
			continue
		}
		if c.IsDefault {
			return c.ID, nil
		}
		if first == "" {
			first = c.ID
		}
	}
	if first == "" {
		return "", fmt.Errorf("2225 no calendar in this account")
	}
	return first, nil
}
