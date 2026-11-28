package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/pim"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/notify"
)

// 任务到期提醒: 未完成、有截止时间的任务到点弹通知 (设置 → 通知 →「任务到期提醒」, 默认开)。
//
// 任务自带提醒 (alerts, 别的客户端设的) 时按它们算, 相对开始与相对结束都按截止时间算 (任务没有时长, 截止即结束);
// 没有时在截止时刻提醒, 全天任务在当天 9:00; 重复的任务每次截止各提醒一次。与日程提醒共用轮询 (reminders.go) 与移动端的定时通知登记 (reminders_schedule.go)。
// 点提醒 (ActionID "task:<账号>/<id>") 进打开请求队列 (notifications.go), 界面切到待办并打开这个任务。

// allDayTaskHour 是全天任务的提醒时刻 (本机时间)。
const allDayTaskHour = 9

// taskReminder 是一条任务提醒。
type taskReminder struct {
	AccountID string
	TaskID    string
	Title     string
	Due       time.Time
	AllDay    bool
	At        time.Time
}

// key 区分同一任务的多条提醒、重复任务的各次与改过截止时间后的新提醒。
func (r taskReminder) key() string {
	return fmt.Sprintf("task/%s/%s/%d", r.AccountID, r.TaskID, r.At.Unix())
}

// taskDueReminders 列出这些账号里触发时刻落在 [from, to] 的任务提醒, 按时间排序。
func (a *App) taskDueReminders(st *store.Store, accounts []string, from, to time.Time) ([]taskReminder, error) {
	raws, err := st.DueObjects(a.ctx)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, acc := range accounts {
		allowed[acc] = true
	}
	var out []taskReminder
	for _, r := range raws {
		if !allowed[r.AccountID] {
			continue
		}
		e, err := pim.ParseEvent([]byte(r.Raw))
		if err != nil || !e.IsTask() || e.Done() {
			continue
		}
		// 重复的任务每次截止各提醒一次; 提醒可能比截止早 (提前一周) 或晚, 截止时间的范围两头放宽。
		dues, err := e.DueTimes(from.Add(-reminderLookahead), to.Add(reminderLookahead))
		if err != nil {
			continue
		}
		for _, due := range dues {
			base := taskReminder{AccountID: r.AccountID, TaskID: e.ID, Title: e.Title, Due: due, AllDay: e.ShowWithoutTime}
			for _, at := range taskFireTimes(e.HasAlerts(), e.AlertList(), due, e.ShowWithoutTime) {
				if at.Before(from) || at.After(to) {
					continue
				}
				rem := base
				rem.At = at
				out = append(out, rem)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return out[i].key() < out[j].key()
	})
	return out, nil
}

// taskFireTimes 是一个任务的提醒时刻: 自带提醒按截止时间算, 没有提醒定义时截止当刻 (全天任务当天 9:00)。
// 有定义但都已确认 (在别的客户端里关掉了) 时不提醒, 不能当成没有提醒而改用默认的。
func taskFireTimes(defined bool, alerts []pim.Alert, due time.Time, allDay bool) []time.Time {
	if !defined {
		if allDay {
			y, m, d := due.Date()
			return []time.Time{time.Date(y, m, d, allDayTaskHour, 0, 0, 0, time.Local)}
		}
		return []time.Time{due}
	}
	out := make([]time.Time, 0, len(alerts))
	for _, al := range alerts {
		out = append(out, al.FireTime(due, due))
	}
	return out
}

// taskReminderText 是任务提醒的标题与正文。shown 为通知弹出的时刻: 截止在那一天时写「今天」。
func taskReminderText(r taskReminder, shown time.Time, t translator) (title, body string) {
	title = r.Title
	if title == "" {
		title = t("(无标题任务)")
	}
	switch {
	case r.AllDay && sameDay(r.Due, shown):
		body = t("今天到期")
	case r.AllDay:
		body = t("{0} 到期", r.Due.Format(t("1月2日")))
	case sameDay(r.Due, shown):
		body = t("今天 {0} 到期", r.Due.Local().Format("15:04"))
	default:
		body = t("{0} 到期", r.Due.Local().Format(t("1月2日")+" 15:04"))
	}
	return title, body
}

func (a *App) notifyTask(r taskReminder) {
	a.mu.RLock()
	n, ctx := a.notifier, a.ctx
	a.mu.RUnlock()
	if n == nil || ctx == nil {
		return
	}
	title, body := taskReminderText(r, time.Now(), a.t)
	_ = n.Notify(ctx, notify.Notification{Title: title, Body: body, ActionID: "task:" + r.AccountID + "/" + r.TaskID})
}

// checkTaskReminders 弹出触发时刻落在 (now-reminderStale, now] 且没弹过的任务提醒。调用方持有 state.mu。
func (a *App) checkTaskReminders(st *store.Store, accounts []string, state *reminderState, now time.Time) {
	list, err := a.taskDueReminders(st, accounts, now.Add(-reminderStale), now)
	if err != nil {
		return
	}
	for _, r := range list {
		key := r.key()
		if _, done := state.fired[key]; done {
			continue
		}
		state.fired[key] = now
		a.notifyTask(r)
	}
}

// scheduledTaskReminders 把任务提醒换成交给原生壳的定时通知。
func scheduledTaskReminders(list []taskReminder, t translator) []scheduledReminder {
	out := make([]scheduledReminder, 0, len(list))
	seen := map[string]bool{}
	for _, r := range list {
		// 同一任务的两条提醒落在同一时刻只登记一条: 系统按 id 只留一条, 白占一个名额。
		if seen[r.key()] {
			continue
		}
		seen[r.key()] = true
		title, body := taskReminderText(r, r.At, t)
		out = append(out, scheduledReminder{
			ID:       reminderID(r.key()),
			Title:    title,
			Body:     body,
			ActionID: "task:" + r.AccountID + "/" + r.TaskID,
			At:       r.At.Unix(),
		})
	}
	return out
}

// startOfLocalDay 是 t 所在那天的本机零点。
func startOfLocalDay(t time.Time) time.Time {
	y, m, d := t.Local().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}
