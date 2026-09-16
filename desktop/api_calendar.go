package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/pim"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 日历。读走本地缓存并在 Go 里展开重复事件; 写走 CalendarEvent/set。

// ListCalendars 列出全部账号的日历。
func (a *App) ListCalendars() ([]store.Calendar, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	return st.Calendars(a.ctx)
}

// EventOccurrence 是日程视图里的一格。
type EventOccurrence struct {
	AccountID string `json:"accountId"`
	pim.Occurrence
}

// maxRangeDays 限制一次展开的区间, 防止界面传入一个超大区间把重复事件展开成几十万条。
const maxRangeDays = 400

// ListEvents 返回 [from, to) 内的全部发生次。时间为 RFC 3339。
func (a *App) ListEvents(from, to string) ([]EventOccurrence, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	start, err1 := time.Parse(time.RFC3339, from)
	end, err2 := time.Parse(time.RFC3339, to)
	if err1 != nil || err2 != nil || !end.After(start) {
		return nil, fmt.Errorf("2100 invalid range %q..%q", from, to)
	}
	if end.Sub(start) > maxRangeDays*24*time.Hour {
		end = start.Add(maxRangeDays * 24 * time.Hour)
	}
	return a.expandEvents(st, nil, start, end)
}

func (a *App) expandEvents(st *store.Store, accountIDs []string, start, end time.Time) ([]EventOccurrence, error) {
	// 查询下界放宽一天: 跨天事件与浮动时区事件的 start_at 可能早于视图起点。
	raws, err := st.EventsInRange(a.ctx, accountIDs, start.Add(-24*time.Hour).Unix(), end.Unix())
	if err != nil {
		return nil, err
	}
	var out []EventOccurrence
	for _, r := range raws {
		e, err := pim.ParseEvent([]byte(r.Raw))
		if err != nil || e.IsTask() {
			continue
		}
		occs, err := e.Expand(start, end)
		if err != nil {
			continue
		}
		for _, o := range occs {
			out = append(out, EventOccurrence{AccountID: r.AccountID, Occurrence: o})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].AllDay && !out[j].AllDay
	})
	return out, nil
}

// EventDetail 是事件编辑表单的数据。
type EventDetail struct {
	AccountID    string        `json:"accountId"`
	ID           string        `json:"id"`
	CalendarID   string        `json:"calendarId"`
	Title        string        `json:"title"`
	Description  string        `json:"description"`
	Location     string        `json:"location"`
	Start        string        `json:"start"` // LocalDateTime
	End          string        `json:"end"`   // LocalDateTime; 全天事件为结束日的次日零点
	AllDay       bool          `json:"allDay"`
	TimeZone     string        `json:"timeZone"`
	Recurrence   *Recurrence   `json:"recurrence"`
	AlertMinutes []int         `json:"alertMinutes"`
	Participants []Participant `json:"participants"`
	ReadOnly     bool          `json:"readOnly"`
	// VirtualLocation 是会议链接。
	VirtualLocation string `json:"virtualLocation"`
	// Organizer 是组织者邮箱; IsOrganizer 表示当前用户就是组织者(可以编辑参加者)。
	Organizer   string `json:"organizer"`
	IsOrganizer bool   `json:"isOrganizer"`
	// MyStatus 是当前用户作为受邀者的回复状态, 不是受邀者时为空。
	MyStatus string `json:"myStatus"`
}

// Recurrence 是界面支持的重复规则子集。更复杂的规则只读展示, 保存时原样保留。
type Recurrence struct {
	Frequency string   `json:"frequency"` // daily weekly monthly yearly
	Interval  int      `json:"interval"`
	Count     int      `json:"count"`
	Until     string   `json:"until"` // LocalDateTime 或空
	ByDay     []string `json:"byDay"` // mo tu ...
	// Complex 为真表示规则里有界面编辑不了的部分, 此时不允许修改重复设置。
	Complex bool `json:"complex"`
}

type Participant struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Status string `json:"status"`
	Role   string `json:"role"`
}

// GetEvent 读事件详情。
func (a *App) GetEvent(accountID, eventID string) (*EventDetail, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	raw, err := st.Event(a.ctx, accountID, eventID)
	if err != nil {
		return nil, err
	}
	e, err := pim.ParseEvent([]byte(raw))
	if err != nil {
		return nil, err
	}

	d := &EventDetail{
		AccountID: accountID, ID: e.ID, Title: e.Title, Description: e.Description,
		Location: e.Location(), AllDay: e.ShowWithoutTime,
	}
	if ids := e.CalendarIDList(); len(ids) > 0 {
		d.CalendarID = ids[0]
	}
	if e.TimeZone != nil {
		d.TimeZone = *e.TimeZone
	}
	// 界面一律使用本机时间; 写回时再换算到事件自己的时区。
	if startT, err := e.StartTime(); err == nil {
		d.Start = startT.In(time.Local).Format(pim.LocalDateTime)
		d.End = startT.Add(e.DurationValue()).In(time.Local).Format(pim.LocalDateTime)
	}

	if rules := e.Rules(); len(rules) > 0 {
		r := rules[0]
		rec := &Recurrence{Frequency: r.Frequency, Interval: r.Interval, Count: r.Count, Until: r.Until}
		for _, day := range r.ByDay {
			rec.ByDay = append(rec.ByDay, day.Day)
			if day.NthOfPeriod != 0 {
				rec.Complex = true
			}
		}
		if len(rules) > 1 || len(r.ByMonthDay)+len(r.ByMonth)+len(r.ByYearDay)+len(r.ByWeekNo)+
			len(r.BySetPosition)+len(r.ByHour)+len(r.ByMinute) > 0 || r.RScale != "" ||
			!map[string]bool{"daily": true, "weekly": true, "monthly": true, "yearly": true}[r.Frequency] {
			rec.Complex = true
		}
		d.Recurrence = rec
	}

	var full map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &full)
	d.AlertMinutes = alertMinutes(full["alerts"])
	d.Participants = participants(full["participants"])
	d.VirtualLocation = firstVirtualLocation(full["virtualLocations"])

	var organizer string
	_ = json.Unmarshal(full["organizerCalendarAddress"], &organizer)
	d.Organizer = strings.TrimPrefix(strings.ToLower(organizer), "mailto:")
	mine := a.myAddresses(accountID)
	for _, p := range d.Participants {
		if p.Role == "owner" && d.Organizer == "" {
			d.Organizer = strings.ToLower(p.Email)
		}
		if mine[strings.ToLower(p.Email)] && p.Role != "owner" {
			d.MyStatus = p.Status
			if d.MyStatus == "" {
				d.MyStatus = "needs-action"
			}
		}
	}
	d.IsOrganizer = len(d.Participants) == 0 || mine[d.Organizer]

	if cal := a.calendar(st, accountID, d.CalendarID); cal != nil {
		d.ReadOnly = cal.MyRights != nil && !cal.MyRights["mayWriteAll"] && !cal.MyRights["mayWriteOwn"]
	}
	return d, nil
}

func (a *App) calendar(st *store.Store, accountID, id string) *store.Calendar {
	cals, _ := st.Calendars(a.ctx)
	for i := range cals {
		if cals[i].AccountID == accountID && cals[i].ID == id {
			return &cals[i]
		}
	}
	return nil
}

func alertMinutes(raw json.RawMessage) []int {
	var alerts map[string]struct {
		Trigger struct {
			Offset string `json:"offset"`
		} `json:"trigger"`
	}
	if json.Unmarshal(raw, &alerts) != nil {
		return nil
	}
	var out []int
	for _, al := range alerts {
		neg := strings.HasPrefix(al.Trigger.Offset, "-")
		d, err := pim.ParseDuration(strings.TrimLeft(al.Trigger.Offset, "+-"))
		if err != nil {
			continue
		}
		m := int(d / time.Minute)
		if !neg {
			m = -m
		}
		out = append(out, m)
	}
	sort.Ints(out)
	return out
}

func participants(raw json.RawMessage) []Participant {
	var ps map[string]struct {
		Name                string            `json:"name"`
		Email               string            `json:"email"`
		CalendarAddress     string            `json:"calendarAddress"`
		SendTo              map[string]string `json:"sendTo"`
		ParticipationStatus string            `json:"participationStatus"`
		Roles               map[string]bool   `json:"roles"`
	}
	if json.Unmarshal(raw, &ps) != nil {
		return nil
	}
	var out []Participant
	for id, p := range ps {
		email := p.Email
		if email == "" {
			email = strings.TrimPrefix(p.CalendarAddress, "mailto:")
		}
		if email == "" {
			email = strings.TrimPrefix(p.SendTo["imip"], "mailto:")
		}
		role := "attendee"
		if p.Roles["owner"] {
			role = "owner"
		}
		out = append(out, Participant{ID: id, Name: p.Name, Email: email, Status: p.ParticipationStatus, Role: role})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role == "owner" && out[j].Role != "owner" })
	return out
}

// EventInput 是保存事件的表单。
type EventInput struct {
	AccountID    string      `json:"accountId"`
	ID           string      `json:"id"`
	CalendarID   string      `json:"calendarId"`
	Title        string      `json:"title"`
	Description  string      `json:"description"`
	Location     string      `json:"location"`
	Start        string      `json:"start"` // LocalDateTime
	End          string      `json:"end"`
	AllDay       bool        `json:"allDay"`
	TimeZone     string      `json:"timeZone"`
	Recurrence   *Recurrence `json:"recurrence"`
	AlertMinutes []int       `json:"alertMinutes"`
	// Scope 为 "this" 且 RecurrenceID 非空时只修改重复事件的这一次。
	Scope        string `json:"scope"`
	RecurrenceID string `json:"recurrenceId"`
	// VirtualLocation 是会议链接。
	VirtualLocation string `json:"virtualLocation"`
	// Attendees 为受邀者; 非 nil 时覆盖事件原有的参加者, 并由服务器发送邀请。
	// 为 nil 表示不修改参加者(例如受邀者自己只改提醒)。
	Attendees []Attendee `json:"attendees"`
}

// Attendee 是受邀者。
type Attendee struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// SaveEvent 新建或修改事件, 返回事件 id。
func (a *App) SaveEvent(in EventInput) (string, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Title) == "" {
		return "", fmt.Errorf("2101 title is required")
	}
	if in.TimeZone == "" && !in.AllDay {
		in.TimeZone = localTimeZone()
	}
	// 表单时间是本机时间; 有时区的事件按其时区存 LocalDateTime。
	loc := time.Local
	if in.TimeZone != "" && !in.AllDay {
		if l, err := time.LoadLocation(in.TimeZone); err == nil {
			loc = l
		}
	}
	start, err1 := time.ParseInLocation(pim.LocalDateTime, in.Start, time.Local)
	end, err2 := time.ParseInLocation(pim.LocalDateTime, in.End, time.Local)
	if err1 != nil || err2 != nil {
		return "", fmt.Errorf("2102 invalid start or end time")
	}
	if end.Before(start) {
		return "", fmt.Errorf("2103 end is before start")
	}
	duration := end.Sub(start)
	if in.AllDay && duration < 24*time.Hour {
		duration = 24 * time.Hour
	}

	props := map[string]any{
		"title":           in.Title,
		"description":     in.Description,
		"start":           start.In(loc).Format(pim.LocalDateTime),
		"duration":        pim.FormatDuration(duration),
		"showWithoutTime": in.AllDay,
		"locations":       nil,
		"alerts":          nil,
	}
	if in.AllDay {
		props["timeZone"] = nil
	} else {
		props["timeZone"] = in.TimeZone
	}
	if loc := strings.TrimSpace(in.Location); loc != "" {
		props["locations"] = map[string]any{"l1": map[string]any{"@type": "Location", "name": loc}}
	}
	props["virtualLocations"] = nil
	if uri := strings.TrimSpace(in.VirtualLocation); uri != "" {
		props["virtualLocations"] = map[string]any{"v1": map[string]any{"@type": "VirtualLocation", "uri": uri}}
	}

	// 有参加者时由服务器发 iTIP 邀请/更新/取消。
	extra := map[string]any{}
	if in.Attendees != nil {
		participants, organizer, err := a.buildParticipants(in.AccountID, in.Attendees)
		if err != nil {
			return "", err
		}
		props["participants"] = participants
		props["organizerCalendarAddress"] = organizer
		if participants != nil {
			extra["sendSchedulingMessages"] = true
		}
	}
	props["useDefaultAlerts"] = false
	if len(in.AlertMinutes) > 0 {
		alerts := map[string]any{}
		for i, m := range in.AlertMinutes {
			alerts[fmt.Sprintf("a%d", i+1)] = map[string]any{
				"@type":   "Alert",
				"trigger": map[string]any{"@type": "OffsetTrigger", "offset": "-" + pim.FormatDuration(time.Duration(m)*time.Minute)},
			}
		}
		props["alerts"] = alerts
	}

	// 只改这一次: 写进 recurrenceOverrides, 主体不动。
	if in.ID != "" && in.Scope == "this" && in.RecurrenceID != "" {
		override := map[string]any{}
		for _, k := range []string{"title", "description", "start", "duration", "locations"} {
			override[k] = props[k]
		}
		return in.ID, a.patchOverride(in.AccountID, in.ID, in.RecurrenceID, override)
	}

	if in.Recurrence == nil || !in.Recurrence.Complex {
		props["recurrenceRule"] = recurrenceRule(in.Recurrence)
	}

	if in.ID == "" {
		if in.CalendarID == "" {
			return "", fmt.Errorf("2104 calendar is required")
		}
		props["@type"] = "Event"
		props["calendarIds"] = map[string]bool{in.CalendarID: true}
		clean := map[string]any{}
		for k, v := range props {
			if v != nil {
				clean[k] = v
			}
		}
		created, err := s.SetPIM(a.ctx, in.AccountID, jmapx.CalendarEvent, map[string]any{"new": clean}, nil, nil, extra)
		if err != nil {
			return "", err
		}
		return created["new"], nil
	}

	if in.CalendarID != "" {
		props["calendarIds"] = map[string]bool{in.CalendarID: true}
	}
	_, err = s.SetPIM(a.ctx, in.AccountID, jmapx.CalendarEvent, nil, map[string]map[string]any{in.ID: props}, nil, extra)
	return in.ID, err
}

func recurrenceRule(r *Recurrence) any {
	if r == nil || r.Frequency == "" {
		return nil
	}
	rule := map[string]any{"@type": "RecurrenceRule", "frequency": r.Frequency}
	if r.Interval > 1 {
		rule["interval"] = r.Interval
	}
	if r.Count > 0 {
		rule["count"] = r.Count
	} else if r.Until != "" {
		rule["until"] = r.Until
	}
	if len(r.ByDay) > 0 {
		days := make([]any, 0, len(r.ByDay))
		for _, d := range r.ByDay {
			days = append(days, map[string]any{"@type": "NDay", "day": d})
		}
		rule["byDay"] = days
	}
	return rule
}

// patchOverride 修改重复事件的单次。事件还没有 recurrenceOverrides 时必须整体写入:
// Stalwart 对不存在的父路径做 JSON Pointer 补丁会报 invalidProperties。
func (a *App) patchOverride(accountID, eventID, recurrenceID string, override map[string]any) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	raw, err := st.Event(a.ctx, accountID, eventID)
	if err != nil {
		return err
	}
	e, err := pim.ParseEvent([]byte(raw))
	if err != nil {
		return err
	}

	patch := map[string]any{}
	if len(e.RecurrenceOverrides) == 0 {
		patch["recurrenceOverrides"] = map[string]any{recurrenceID: override}
	} else {
		patch["recurrenceOverrides/"+recurrenceID] = override
	}
	_, err = s.SetPIM(a.ctx, accountID, jmapx.CalendarEvent, nil, map[string]map[string]any{eventID: patch}, nil, nil)
	return err
}

// DeleteEvent 删除事件; scope 为 "this" 时只排除重复事件的这一次。
func (a *App) DeleteEvent(accountID, eventID, recurrenceID, scope string) error {
	if scope == "this" && recurrenceID != "" {
		return a.patchOverride(accountID, eventID, recurrenceID, map[string]any{"excluded": true})
	}
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	// 有参加者的事件删除时让服务器发取消通知。
	extra := map[string]any{}
	if d, err := a.GetEvent(accountID, eventID); err == nil && len(d.Participants) > 0 && d.IsOrganizer {
		extra["sendSchedulingMessages"] = true
	}
	_, err = s.SetPIM(a.ctx, accountID, jmapx.CalendarEvent, nil, nil, []string{eventID}, extra)
	return err
}

// MoveEvent 拖动改期: 把事件(或其中一次)平移到新的开始时间, 时长不变。
func (a *App) MoveEvent(accountID, eventID, recurrenceID, newStart string) error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	raw, err := st.Event(a.ctx, accountID, eventID)
	if err != nil {
		return err
	}
	e, err := pim.ParseEvent([]byte(raw))
	if err != nil {
		return err
	}
	local, err := time.ParseInLocation(pim.LocalDateTime, newStart, time.Local)
	if err != nil {
		return fmt.Errorf("2102 invalid start time")
	}
	target := local.In(e.Loc())
	if recurrenceID != "" {
		return a.patchOverride(accountID, eventID, recurrenceID, map[string]any{"start": target.Format(pim.LocalDateTime)})
	}
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	_, err = s.SetPIM(a.ctx, accountID, jmapx.CalendarEvent, nil,
		map[string]map[string]any{eventID: {"start": target.Format(pim.LocalDateTime)}}, nil, nil)
	return err
}

// SetCalendarVisible 切换日历在视图中是否显示。存服务端 isVisible, 多端一致。
func (a *App) SetCalendarVisible(accountID, calendarID string, visible bool) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	_, err = s.SetPIM(a.ctx, accountID, jmapx.Calendar, nil,
		map[string]map[string]any{calendarID: {"isVisible": visible}}, nil, nil)
	return err
}

// localTimeZone 返回本机 IANA 时区名。Windows 上 time.Local 的名字是 "Local",
// 退回到按偏移匹配常见时区, 匹配不到时用 UTC 偏移不带名的浮动时间。
func localTimeZone() string {
	if name := time.Local.String(); name != "Local" && name != "" {
		return name
	}
	_, offset := time.Now().Zone()
	for _, name := range []string{
		"Asia/Shanghai", "Asia/Tokyo", "Asia/Seoul", "Asia/Singapore", "Asia/Kolkata", "Asia/Dubai",
		"Europe/London", "Europe/Paris", "Europe/Moscow", "America/New_York", "America/Chicago",
		"America/Denver", "America/Los_Angeles", "Australia/Sydney", "UTC",
	} {
		loc, err := time.LoadLocation(name)
		if err != nil {
			continue
		}
		if _, o := time.Now().In(loc).Zone(); o == offset {
			return name
		}
	}
	return ""
}
