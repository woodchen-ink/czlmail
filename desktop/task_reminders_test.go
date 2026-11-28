package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/pim"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

func putTask(t *testing.T, st *store.Store, id, due string, rest string) {
	t.Helper()
	ctx := context.Background()
	raw := fmt.Sprintf(`{"@type":"Task","id":%q,"title":%q,"due":%q,"calendarIds":{"cal":true}%s}`, id, id, due, rest)
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpsertEvents(ctx, tx, "acc", []store.EventRow{{ID: id, CalendarIDs: []string{"cal"}, Title: id, Raw: raw}})
	}); err != nil {
		t.Fatal(err)
	}
}

// 任务提醒: 定时任务在截止时刻、全天任务当天 9 点、自带提醒按截止时间算; 已完成的与没有截止时间的不提醒;
// 和日程提醒一起登记, 关掉任务提醒只去掉任务的。
func TestTaskReminderPlan(t *testing.T) {
	a, st := reminderTestApp(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 8, 0, 0, 0, time.Local)
	at := func(d time.Duration) string { return now.Add(d).Format(pim.LocalDateTime) }

	putTask(t, st, "timed", at(2*time.Hour), `,"timeZone":"`+time.Local.String()+`"`)
	putTask(t, st, "allday", time.Date(2026, 11, 17, 0, 0, 0, 0, time.Local).Format(pim.LocalDateTime), `,"showWithoutTime":true`)
	putTask(t, st, "early", at(5*time.Hour), `,"timeZone":"`+time.Local.String()+`","alerts":{"a":{"trigger":{"@type":"OffsetTrigger","offset":"-PT1H","relativeTo":"end"}}}`)
	putTask(t, st, "done", at(3*time.Hour), `,"progress":"completed"`)
	putEvent(t, st, "meeting", now.Add(4*time.Hour), `,"alerts":{"a":{"trigger":{"@type":"OffsetTrigger","offset":"-PT15M"}}}`)

	list, err := a.reminderPlan(now)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		action string
		at     time.Time
	}{
		{"task:acc/timed", now.Add(2 * time.Hour)},
		{"event:acc/meeting", now.Add(4*time.Hour - 15*time.Minute)},
		{"task:acc/early", now.Add(4 * time.Hour)},
		{"task:acc/allday", time.Date(2026, 11, 17, 9, 0, 0, 0, time.Local)},
	}
	if len(list) != len(want) {
		t.Fatalf("plan = %+v", list)
	}
	for i, w := range want {
		if list[i].ActionID != w.action || list[i].At != w.at.Unix() {
			t.Fatalf("plan[%d] = %+v, want %s at %v", i, list[i], w.action, w.at)
		}
	}

	if err := st.WithTx(ctx, func(tx *sql.Tx) error { return store.SetBoolSetting(ctx, tx, "notifyTasks", false) }); err != nil {
		t.Fatal(err)
	}
	if list, err := a.reminderPlan(now); err != nil || len(list) != 1 || list[0].ActionID != "event:acc/meeting" {
		t.Fatalf("with task reminders off: %+v, %v", list, err)
	}
}

func TestTaskReminderText(t *testing.T) {
	tr := func(s string, v ...any) string {
		for i, x := range v {
			s = strings.ReplaceAll(s, fmt.Sprintf("{%d}", i), fmt.Sprint(x))
		}
		return s
	}
	shown := time.Date(2026, 11, 16, 9, 0, 0, 0, time.Local)
	cases := []struct {
		r    taskReminder
		want string
	}{
		{taskReminder{Title: "a", Due: shown.Add(6 * time.Hour)}, "今天 15:00 到期"},
		{taskReminder{Title: "a", Due: time.Date(2026, 11, 16, 0, 0, 0, 0, time.Local), AllDay: true}, "今天到期"},
		{taskReminder{Title: "a", Due: time.Date(2026, 11, 18, 0, 0, 0, 0, time.Local), AllDay: true}, "11月18日 到期"},
	}
	for _, c := range cases {
		if _, body := taskReminderText(c.r, shown, tr); body != c.want {
			t.Errorf("body = %q, want %q", body, c.want)
		}
	}
	if title, _ := taskReminderText(taskReminder{}, shown, tr); title != "(无标题任务)" {
		t.Errorf("untitled = %q", title)
	}
}

// 重复的任务每次截止各提醒一次; 自带提醒在别处已确认 (关掉) 时不改用默认提醒。
func TestTaskReminderRecurringAndAcknowledged(t *testing.T) {
	a, st := reminderTestApp(t)
	now := time.Date(2026, 11, 16, 8, 0, 0, 0, time.Local)
	first := now.Add(-7 * 24 * time.Hour).Add(2 * time.Hour) // 上周一 10:00, 已过
	tz := `,"timeZone":"` + time.Local.String() + `"`
	putTask(t, st, "weekly", first.Format(pim.LocalDateTime), tz+`,"recurrenceRule":{"@type":"RecurrenceRule","frequency":"weekly"}`)
	putTask(t, st, "dismissed", now.Add(3*time.Hour).Format(pim.LocalDateTime),
		tz+`,"alerts":{"a":{"trigger":{"@type":"OffsetTrigger","offset":"-PT1H"},"acknowledged":"2026-11-16T07:00:00Z"}}`)

	list, err := a.reminderPlan(now)
	if err != nil {
		t.Fatal(err)
	}
	var weekly []time.Time
	for _, r := range list {
		if r.ActionID == "task:acc/dismissed" {
			t.Fatalf("acknowledged alert fell back to the default: %+v", r)
		}
		if r.ActionID == "task:acc/weekly" {
			weekly = append(weekly, time.Unix(r.At, 0))
		}
	}
	// 30 天的登记范围里有 5 次 (本周一起, 每周一 10:00)。
	if len(weekly) < 4 || !weekly[0].Equal(now.Add(2*time.Hour)) || !weekly[1].Equal(now.Add(7*24*time.Hour+2*time.Hour)) {
		t.Fatalf("weekly reminders = %v", weekly)
	}

	tasks, err := a.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	// ListTasks 按真实的此刻取下一次截止 (测试时刻是固定的), 这里只核对重复标记与截止不是那个已过的第一次。
	for _, task := range tasks {
		if task.ID == "weekly" && (!task.Recurring || task.Due == "" || task.Due == first.Format(pim.LocalDateTime)) {
			t.Fatalf("weekly task = %+v", task)
		}
	}
}
