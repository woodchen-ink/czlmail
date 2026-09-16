package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/pim"
)

// 日历的参加者、回复邀请、任务与日历管理。

// myAddresses 返回当前用户在该账号下可用的发件地址(小写), 用于识别"我"是组织者还是受邀者。
func (a *App) myAddresses(accountID string) map[string]bool {
	out := map[string]bool{}
	a.mu.RLock()
	user := strings.ToLower(a.cfg.Username)
	a.mu.RUnlock()
	if user != "" {
		out[user] = true
	}
	if ids, err := a.ListIdentities(accountID); err == nil {
		for _, id := range ids {
			out[strings.ToLower(id.Email)] = true
		}
	}
	return out
}

// buildParticipants 生成 JSCalendar participants。没有受邀者时返回 nil(清除参加者)。
//
// 与 Bulwark 保持一致: 组织者只标 owner 不标 attendee(否则 Stalwart 发出的邀请里组织者会出现两次),
// 地址写 calendarAddress(jscalendarbis), 调度交给服务器(scheduleAgent=server)。
func (a *App) buildParticipants(accountID string, attendees []Attendee) (any, any, error) {
	seen := map[string]bool{}
	var list []Attendee
	for _, at := range attendees {
		email := strings.TrimSpace(at.Email)
		key := strings.ToLower(email)
		if key == "" || seen[key] {
			continue
		}
		if !strings.Contains(email, "@") {
			return nil, nil, fmt.Errorf("2105 invalid attendee address %q", email)
		}
		seen[key] = true
		list = append(list, Attendee{Name: strings.TrimSpace(at.Name), Email: email})
	}
	if len(list) == 0 {
		return nil, nil, nil
	}

	orgName, orgEmail := "", ""
	if ids, err := a.ListIdentities(accountID); err == nil && len(ids) > 0 {
		orgName, orgEmail = ids[0].Name, ids[0].Email
	}
	if orgEmail == "" {
		a.mu.RLock()
		orgEmail = a.cfg.Username
		a.mu.RUnlock()
	}
	if orgEmail == "" {
		return nil, nil, fmt.Errorf("2106 cannot determine organizer address")
	}

	participants := map[string]any{
		randomID(): map[string]any{
			"@type": "Participant", "name": orgName, "email": orgEmail, "calendarAddress": "mailto:" + orgEmail,
			"roles": map[string]bool{"owner": true}, "participationStatus": "accepted",
			"scheduleAgent": "server", "expectReply": false, "kind": "individual",
		},
	}
	for _, at := range list {
		if strings.EqualFold(at.Email, orgEmail) {
			continue
		}
		participants[randomID()] = map[string]any{
			"@type": "Participant", "name": at.Name, "email": at.Email, "calendarAddress": "mailto:" + at.Email,
			"roles": map[string]bool{"attendee": true}, "participationStatus": "needs-action",
			"scheduleAgent": "server", "expectReply": true, "kind": "individual",
		}
	}
	return participants, "mailto:" + orgEmail, nil
}

// RespondEvent 以受邀者身份回复邀请: accepted / tentative / declined。服务器负责给组织者发回复。
func (a *App) RespondEvent(accountID, eventID, status string) error {
	switch status {
	case "accepted", "tentative", "declined":
	default:
		return fmt.Errorf("2107 invalid status %q", status)
	}
	d, err := a.GetEvent(accountID, eventID)
	if err != nil {
		return err
	}
	mine := a.myAddresses(accountID)
	partID := ""
	for _, p := range d.Participants {
		if mine[strings.ToLower(p.Email)] && p.Role != "owner" {
			partID = p.ID
			break
		}
	}
	if partID == "" {
		return fmt.Errorf("2108 you are not an attendee of this event")
	}
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	_, err = s.SetPIM(a.ctx, accountID, jmapx.CalendarEvent, nil,
		map[string]map[string]any{eventID: {"participants/" + partID + "/participationStatus": status}},
		nil, map[string]any{"sendSchedulingMessages": true})
	return err
}

func firstVirtualLocation(raw json.RawMessage) string {
	var m map[string]struct {
		URI string `json:"uri"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if m[k].URI != "" {
			return m[k].URI
		}
	}
	return ""
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

/* ---------------- 任务 ---------------- */

// TaskItem 是任务列表的一行。
type TaskItem struct {
	AccountID   string `json:"accountId"`
	ID          string `json:"id"`
	CalendarID  string `json:"calendarId"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Due 为本机时间 LocalDateTime, 没有截止时间时为空。
	Due       string `json:"due"`
	DueAllDay bool   `json:"dueAllDay"`
	Completed bool   `json:"completed"`
	Priority  int    `json:"priority"`
	Updated   string `json:"updated"`
}

// ListTasks 列出全部账号的任务。
func (a *App) ListTasks() ([]TaskItem, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	raws, err := st.AllEventObjects(a.ctx)
	if err != nil {
		return nil, err
	}
	var out []TaskItem
	for _, r := range raws {
		e, err := pim.ParseEvent([]byte(r.Raw))
		if err != nil || !e.IsTask() {
			continue
		}
		t := TaskItem{
			AccountID: r.AccountID, ID: e.ID, Title: e.Title, Description: e.Description,
			Priority: e.Priority, Updated: e.Updated, DueAllDay: e.ShowWithoutTime,
			Completed: e.Progress != nil && *e.Progress == "completed",
		}
		if ids := e.CalendarIDList(); len(ids) > 0 {
			t.CalendarID = ids[0]
		}
		if e.Due != nil && *e.Due != "" {
			if due, err := pim.ParseLocalIn(*e.Due, e.Loc()); err == nil {
				t.Due = due.In(time.Local).Format(pim.LocalDateTime)
			}
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Completed != out[j].Completed {
			return !out[i].Completed
		}
		if (out[i].Due == "") != (out[j].Due == "") {
			return out[i].Due != ""
		}
		return out[i].Due < out[j].Due
	})
	return out, nil
}

// TaskInput 是保存任务的表单。
type TaskInput struct {
	AccountID   string `json:"accountId"`
	ID          string `json:"id"`
	CalendarID  string `json:"calendarId"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Due         string `json:"due"` // 本机时间 LocalDateTime, 空表示无截止
	DueAllDay   bool   `json:"dueAllDay"`
	Priority    int    `json:"priority"`
	Completed   bool   `json:"completed"`
}

// SaveTask 新建或修改任务。Stalwart 把任务存为 @type=Task 的 CalendarEvent。
func (a *App) SaveTask(in TaskInput) (string, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Title) == "" {
		return "", fmt.Errorf("2101 title is required")
	}
	props := map[string]any{
		"title":           in.Title,
		"description":     in.Description,
		"priority":        in.Priority,
		"showWithoutTime": in.DueAllDay,
		"due":             nil,
		"timeZone":        nil,
	}
	if in.Due != "" {
		due, err := time.ParseInLocation(pim.LocalDateTime, in.Due, time.Local)
		if err != nil {
			return "", fmt.Errorf("2102 invalid due time")
		}
		props["due"] = due.Format(pim.LocalDateTime)
		if !in.DueAllDay {
			if tz := localTimeZone(); tz != "" {
				props["timeZone"] = tz
			}
		}
	}
	setTaskProgress(props, in.Completed)
	extra := map[string]any{"sendSchedulingMessages": false}

	if in.ID == "" {
		if in.CalendarID == "" {
			return "", fmt.Errorf("2104 calendar is required")
		}
		props["@type"] = "Task"
		props["calendarIds"] = map[string]bool{in.CalendarID: true}
		clean := map[string]any{}
		for k, v := range props {
			if v != nil {
				clean[k] = v
			}
		}
		created, err := s.SetPIM(a.ctx, in.AccountID, jmapx.CalendarEvent, map[string]any{"task": clean}, nil, nil, extra)
		if err != nil {
			return "", err
		}
		return created["task"], nil
	}
	_, err = s.SetPIM(a.ctx, in.AccountID, jmapx.CalendarEvent, nil, map[string]map[string]any{in.ID: props}, nil, extra)
	return in.ID, err
}

// SetTaskCompleted 勾选或取消完成。
func (a *App) SetTaskCompleted(accountID, id string, completed bool) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	props := map[string]any{}
	setTaskProgress(props, completed)
	_, err = s.SetPIM(a.ctx, accountID, jmapx.CalendarEvent, nil, map[string]map[string]any{id: props}, nil,
		map[string]any{"sendSchedulingMessages": false})
	return err
}

func setTaskProgress(props map[string]any, completed bool) {
	if completed {
		props["progress"] = "completed"
		props["percentComplete"] = 100
	} else {
		props["progress"] = "needs-action"
		props["percentComplete"] = 0
	}
}

// DeleteTask 删除任务。
func (a *App) DeleteTask(accountID, id string) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	_, err = s.SetPIM(a.ctx, accountID, jmapx.CalendarEvent, nil, nil, []string{id},
		map[string]any{"sendSchedulingMessages": false})
	return err
}

/* ---------------- 日历管理 ---------------- */

// SaveCalendar 新建或修改日历(名称、颜色), 返回 id。
func (a *App) SaveCalendar(accountID, id, name, color string) (string, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("2109 calendar name is required")
	}
	props := map[string]any{"name": name, "color": nil}
	if color != "" {
		props["color"] = color
	}
	if id == "" {
		props["isVisible"] = true
		props["isSubscribed"] = true
		if color == "" {
			delete(props, "color")
		}
		created, err := s.SetPIM(a.ctx, accountID, jmapx.Calendar, map[string]any{"cal": props}, nil, nil, nil)
		if err != nil {
			return "", err
		}
		return created["cal"], nil
	}
	_, err = s.SetPIM(a.ctx, accountID, jmapx.Calendar, nil, map[string]map[string]any{id: props}, nil, nil)
	return id, err
}

// DeleteCalendar 删除日历及其中的全部日程。
func (a *App) DeleteCalendar(accountID, id string) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	_, err = s.SetPIM(a.ctx, accountID, jmapx.Calendar, nil, nil, []string{id},
		map[string]any{"onDestroyRemoveEvents": true})
	if err == nil {
		s.ResyncPIM(a.ctx, accountID, jmapx.CalendarEvent)
	}
	return err
}
