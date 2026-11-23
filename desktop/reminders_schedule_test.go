package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/i18n"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/pim"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

func occurrence(id string, start time.Time, alerts ...pim.Alert) EventOccurrence {
	return EventOccurrence{AccountID: "acc", Occurrence: pim.Occurrence{
		EventID: id, Title: id, Start: start, End: start.Add(time.Hour), CalendarIDs: []string{"cal"}, Alerts: alerts,
	}}
}

// 三种触发、日历默认提醒、过期与超出范围的剔除、同刻合并、按时间排序。
func TestUpcomingReminders(t *testing.T) {
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.Local)
	horizon := 30 * 24 * time.Hour
	start := now.Add(2 * time.Hour)

	byDefault := occurrence("default", now.Add(5*time.Hour))
	byDefault.UseDefaultAlerts = true
	// 开了默认提醒的事件不用自带的那条。
	byDefault.Alerts = []pim.Alert{{Offset: -4 * time.Hour}}
	allDay := occurrence("allday", now.Add(24*time.Hour))
	allDay.UseDefaultAlerts = true
	allDay.AllDay = true
	recurring := occurrence("weekly", now.Add(48*time.Hour), pim.Alert{Offset: -time.Hour})
	recurring.RecurrenceID = "2026-11-18T09:00:00"

	occs := []EventOccurrence{
		occurrence("start", start, pim.Alert{Offset: -15 * time.Minute}),
		occurrence("end", start, pim.Alert{Offset: -10 * time.Minute, RelativeToEnd: true}),
		occurrence("absolute", start, pim.Alert{At: now.Add(3 * time.Hour)}),
		byDefault,
		allDay,
		recurring,
		// 同一时刻的两条只留一条。
		occurrence("twice", start, pim.Alert{Offset: -30 * time.Minute}, pim.Alert{Offset: -30 * time.Minute}),
		// 已过点 (含正好此刻) 与超出范围的不登记。
		occurrence("past", now.Add(10*time.Minute), pim.Alert{Offset: -15 * time.Minute}),
		occurrence("now", now.Add(15*time.Minute), pim.Alert{Offset: -15 * time.Minute}),
		occurrence("far", now.Add(horizon+2*time.Hour), pim.Alert{Offset: -time.Hour}),
	}
	defaults := defaultAlerts{"acc/cal": {
		withTime:    []pim.Alert{{Offset: -time.Hour}},
		withoutTime: []pim.Alert{{Offset: -6 * time.Hour}},
	}}

	got := upcomingReminders(occs, defaults, now, horizon, 100, sourceText)
	want := []struct {
		event string
		at    time.Time
	}{
		{"twice", start.Add(-30 * time.Minute)},
		{"start", start.Add(-15 * time.Minute)},
		{"end", start.Add(50 * time.Minute)},
		{"absolute", now.Add(3 * time.Hour)},
		{"default", now.Add(4 * time.Hour)},
		{"allday", now.Add(18 * time.Hour)},
		{"weekly", now.Add(47 * time.Hour)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d reminders, want %d: %+v", len(got), len(want), got)
	}
	ids := map[string]bool{}
	for i, w := range want {
		g := got[i]
		if g.ActionID != "event:acc/"+w.event || g.At != w.at.Unix() {
			t.Errorf("reminder %d = %s at %s, want %s at %s", i, g.ActionID, time.Unix(g.At, 0), w.event, w.at)
		}
		if g.ID == "" || ids[g.ID] {
			t.Errorf("reminder %d has empty or duplicate id %q", i, g.ID)
		}
		ids[g.ID] = true
		if g.Title != w.event || g.Body == "" {
			t.Errorf("reminder %d text = %q / %q", i, g.Title, g.Body)
		}
	}

	// 标识只取决于事件、发生次与触发时刻: 重算后不变, 原生壳才能认出是同一条。
	again := upcomingReminders(occs, defaults, now.Add(time.Minute), horizon, 100, sourceText)
	if again[0].ID != got[0].ID {
		t.Errorf("id changed between plans: %q vs %q", again[0].ID, got[0].ID)
	}
}

// 超过上限时按触发时间留最近的一批。
func TestUpcomingRemindersLimit(t *testing.T) {
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.Local)
	var occs []EventOccurrence
	// 倒着放: 结果不能依赖输入顺序。
	for i := 100; i >= 1; i-- {
		occs = append(occs, occurrence(fmt.Sprintf("e%03d", i), now.Add(time.Duration(i)*time.Hour), pim.Alert{}))
	}
	got := upcomingReminders(occs, nil, now, 30*24*time.Hour, scheduledReminderMax, sourceText)
	if len(got) != scheduledReminderMax {
		t.Fatalf("got %d reminders, want %d", len(got), scheduledReminderMax)
	}
	for i, g := range got {
		if want := fmt.Sprintf("event:acc/e%03d", i+1); g.ActionID != want {
			t.Fatalf("reminder %d = %s, want %s", i, g.ActionID, want)
		}
	}
}

// 提醒弹出当天的事件写「今天」, 提前几天的写日期。
func TestReminderText(t *testing.T) {
	start := time.Date(2026, 11, 20, 14, 30, 0, 0, time.Local)
	o := occurrence("", start)
	o.Title = ""
	o.Location = " 3 号会议室 "
	if title, body := reminderText(o, start.Add(-15*time.Minute), sourceText); title != "(无标题日程)" || body != "今天 14:30 · 3 号会议室" {
		t.Errorf("same day: %q / %q", title, body)
	}
	if _, body := reminderText(o, start.Add(-48*time.Hour), sourceText); body != "11月20日 14:30 · 3 号会议室" {
		t.Errorf("earlier day: %q", body)
	}
}

func reminderTestApp(t *testing.T) (*App, *store.Store) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := &App{
		ctx:   ctx,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		store: st,
	}
	a.primary.cfg = config.Config{SessionEndpoint: "https://mail.example/jmap/session", Username: "wood@example", AuthMethod: config.AuthPassword}
	return a, st
}

func putEvent(t *testing.T, st *store.Store, id string, start time.Time, rest string) {
	t.Helper()
	ctx := context.Background()
	end := start.Add(time.Hour).Unix()
	raw := fmt.Sprintf(`{"@type":"Event","id":%q,"title":%q,"start":%q,"timeZone":%q,"duration":"PT1H","calendarIds":{"cal":true}%s}`,
		id, id, start.Format(pim.LocalDateTime), start.Location().String(), rest)
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpsertEvents(ctx, tx, "acc", []store.EventRow{{
			ID: id, CalendarIDs: []string{"cal"}, Title: id, StartAt: start.Unix(), EndAt: &end, Raw: raw,
		}})
	}); err != nil {
		t.Fatal(err)
	}
}

// 从缓存库算提醒表: 日历默认提醒生效, 已确认的提醒不登记; 关掉开关或退出登录后是空表。
func TestReminderPlan(t *testing.T) {
	a, st := reminderTestApp(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		return store.ReplaceCalendars(ctx, tx, "acc", []store.Calendar{{
			ID: "cal", Name: "日历", DefaultAlertsWithTime: `{"d":{"trigger":{"@type":"OffsetTrigger","offset":"-PT30M"}}}`,
		}})
	}); err != nil {
		t.Fatal(err)
	}
	putEvent(t, st, "own", now.Add(3*time.Hour), `,"alerts":{"a":{"trigger":{"@type":"OffsetTrigger","offset":"-PT15M"}}}`)
	putEvent(t, st, "default", now.Add(5*time.Hour), `,"useDefaultAlerts":true`)
	putEvent(t, st, "acked", now.Add(6*time.Hour), `,"alerts":{"a":{"trigger":{"offset":"-PT15M"},"acknowledged":"2026-11-16T08:00:00Z"}}`)
	putEvent(t, st, "silent", now.Add(7*time.Hour), ``)

	list, err := a.reminderPlan(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 ||
		list[0].ActionID != "event:acc/own" || list[0].At != now.Add(3*time.Hour-15*time.Minute).Unix() ||
		list[1].ActionID != "event:acc/default" || list[1].At != now.Add(5*time.Hour-30*time.Minute).Unix() {
		t.Fatalf("plan = %+v", list)
	}

	if err := st.WithTx(ctx, func(tx *sql.Tx) error { return store.SetBoolSetting(ctx, tx, "notifyEvents", false) }); err != nil {
		t.Fatal(err)
	}
	if list, err := a.reminderPlan(now); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("with reminders off: %+v, %v", list, err)
	}

	if err := st.WithTx(ctx, func(tx *sql.Tx) error { return store.SetBoolSetting(ctx, tx, "notifyEvents", true) }); err != nil {
		t.Fatal(err)
	}
	a.primary.cfg = config.Config{}
	if list, err := a.reminderPlan(now); err != nil || len(list) != 0 {
		t.Fatalf("signed out: %+v, %v", list, err)
	}
}

// 登记: 成功后轮询让位; 内容没变不重发 (强制时重发); 关掉开关发空表清掉; 原生壳拒绝时退回轮询。
func TestSyncReminders(t *testing.T) {
	a, st := reminderTestApp(t)
	ctx := context.Background()
	putEvent(t, st, "soon", time.Now().Add(3*time.Hour), `,"alerts":{"a":{"trigger":{"offset":"-PT15M"}}}`)

	var sent [][]scheduledReminder
	var fail error
	reminderHost = func(_ context.Context, list []scheduledReminder) error {
		if fail != nil {
			return fail
		}
		sent = append(sent, list)
		return nil
	}
	t.Cleanup(func() { reminderHost = nil })

	a.syncReminders(false)
	if len(sent) != 1 || len(sent[0]) != 1 || sent[0][0].ActionID != "event:acc/soon" {
		t.Fatalf("first registration = %+v", sent)
	}
	if !a.reminders.hosted.Load() {
		t.Fatal("polling must stand down once the system holds the reminders")
	}

	a.syncReminders(false)
	if len(sent) != 1 {
		t.Fatalf("unchanged plan was sent again: %d", len(sent))
	}
	a.syncReminders(true)
	if len(sent) != 2 {
		t.Fatalf("forced registration was skipped: %d", len(sent))
	}

	if err := st.WithTx(ctx, func(tx *sql.Tx) error { return store.SetBoolSetting(ctx, tx, "notifyEvents", false) }); err != nil {
		t.Fatal(err)
	}
	a.syncReminders(false)
	if len(sent) != 3 || sent[2] == nil || len(sent[2]) != 0 {
		t.Fatalf("turning reminders off must clear the registered ones: %+v", sent)
	}

	fail = errors.New("2266 unsupported host request: scheduleReminders")
	a.syncReminders(true)
	if a.reminders.hosted.Load() {
		t.Fatal("polling must take over when the native shell refuses")
	}
}

// sourceText 用源语言 (中文) 翻译, 测试的期望值按中文写。
func sourceText(source string, values ...any) string {
	return catalog.T(i18n.Source, source, values...)
}
