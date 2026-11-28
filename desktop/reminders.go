package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/accountid"
	"github.com/woodchen-ink/czlmail/desktop/internal/pim"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/notify"
)

// 日程提醒: 按事件的 alerts 在到点时弹系统通知。任务的到期提醒在 task_reminders.go, 共用这里的轮询。
//
// 规则与 Bulwark 一致:
//   - 事件 useDefaultAlerts 为真时用所属日历的默认提醒(全天与定时事件各一套)
//   - 支持相对开始、相对结束与绝对时间三种触发
//   - 已确认(acknowledged)的提醒不再触发
//   - 错过超过 10 分钟的提醒不补发(睡眠唤醒、应用刚启动时不会一口气弹出一堆旧提醒)
//
// 只在应用运行时生效(关窗后留在托盘即可)。已提醒记录只存内存。
// 移动端程序会被系统挂起, 另把未来的提醒登记成系统的定时通知 (reminders_schedule.go), 登记成功后这里不再弹。

const (
	reminderTick = 30 * time.Second
	// reminderStale 之前的提醒视为过期不补发。
	reminderStale = 10 * time.Minute
	// reminderLookahead 覆盖最长的常见提前量(一周)再多一天。
	reminderLookahead = 8 * 24 * time.Hour
)

// EventOpenEvent 用户点了日程提醒, 负载为 "accountID/eventID"。
const EventOpenEvent = "calendar:open"

type reminderState struct {
	mu    sync.Mutex
	fired map[string]time.Time
}

// runReminders 在启动时开始, 跟随应用运行 (不跟随某个登录的会话): 提醒只读本机缓存,
// 哪个登录离线都照样按缓存提醒; 只有退出了的登录不提醒 (reminderAccounts)。
func (a *App) runReminders(ctx context.Context) {
	state := &reminderState{fired: map[string]time.Time{}}
	a.planRemindersSoon(true)
	planned := time.Now()
	t := time.NewTicker(reminderTick)
	defer t.Stop()
	for {
		if !a.reminders.hosted.Load() {
			a.checkReminders(state, time.Now())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if time.Since(planned) >= reminderReplanEvery {
				planned = time.Now()
				a.planRemindersSoon(true)
			}
		}
	}
}

// checkReminders 触发时刻落在 (now-reminderStale, now] 且尚未提醒过的全部提醒。
func (a *App) checkReminders(state *reminderState, now time.Time) {
	events, tasks := a.settingOn("notifyEvents", true), a.settingOn("notifyTasks", true)
	if !events && !tasks {
		return
	}
	st, err := a.currentStore()
	if err != nil {
		return
	}
	accounts := a.reminderAccounts(st)
	if len(accounts) == 0 {
		return
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	for key, at := range state.fired {
		if now.Sub(at) > 2*reminderLookahead {
			delete(state.fired, key)
		}
	}
	if tasks {
		a.checkTaskReminders(st, accounts, state, now)
	}
	if !events {
		return
	}
	occs, err := a.expandEvents(st, accounts, now.Add(-reminderLookahead), now.Add(reminderLookahead))
	if err != nil {
		return
	}
	defaults := calendarDefaultAlerts(a.ctx, st)

	for _, o := range occs {
		alerts := o.Alerts
		if o.UseDefaultAlerts {
			alerts = defaults.forOccurrence(o)
		}
		for i, al := range alerts {
			at := al.FireTime(o.Start, o.End)
			if at.After(now) || now.Sub(at) > reminderStale {
				continue
			}
			key := fmt.Sprintf("%s/%s/%s/%d/%d", o.AccountID, o.EventID, o.RecurrenceID, i, at.Unix())
			if _, done := state.fired[key]; done {
				continue
			}
			state.fired[key] = now
			a.notifyEvent(o)
		}
	}
}

// reminderAccounts 返回要提醒的账号: 所属登录已配置、没退出、没被移除的账号, 含离线的登录 (按缓存提醒)。
// 所有登录的日历都提醒 (#15); 没有可提醒的账号时为空。
func (a *App) reminderAccounts(st *store.Store) []string {
	accounts, err := st.Accounts(a.ctx)
	if err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []string
	for _, acc := range accounts {
		prefix, _ := accountid.Split(acc.ID)
		si := a.signInLocked(prefix)
		if si == nil || si.removed || si.signedOut || !si.cfg.Configured() {
			continue
		}
		out = append(out, acc.ID)
	}
	return out
}

// defaultAlerts 是 "账号/日历" → 默认提醒。
type defaultAlerts map[string]struct{ withTime, withoutTime []pim.Alert }

func calendarDefaultAlerts(ctx context.Context, st *store.Store) defaultAlerts {
	out := defaultAlerts{}
	cals, err := st.Calendars(ctx)
	if err != nil {
		return out
	}
	for _, c := range cals {
		out[c.AccountID+"/"+c.ID] = struct{ withTime, withoutTime []pim.Alert }{
			withTime:    pim.ParseAlerts([]byte(c.DefaultAlertsWithTime)),
			withoutTime: pim.ParseAlerts([]byte(c.DefaultAlertsWithoutTime)),
		}
	}
	return out
}

func (d defaultAlerts) forOccurrence(o EventOccurrence) []pim.Alert {
	var out []pim.Alert
	for _, id := range o.CalendarIDs {
		def := d[o.AccountID+"/"+id]
		if o.AllDay {
			out = append(out, def.withoutTime...)
		} else {
			out = append(out, def.withTime...)
		}
	}
	return out
}

func (a *App) notifyEvent(o EventOccurrence) {
	a.mu.RLock()
	n, ctx := a.notifier, a.ctx
	a.mu.RUnlock()
	if n == nil || ctx == nil {
		return
	}

	title, body := reminderText(o, time.Now(), a.t)
	_ = n.Notify(ctx, notify.Notification{
		Title:    title,
		Body:     body,
		ActionID: "event:" + o.AccountID + "/" + o.EventID,
	})
}

// translator 按界面语言翻译后端文案 (a.t), 测试里换成固定语言。
type translator func(source string, values ...any) string

// reminderText 是提醒通知的标题与正文。shown 为通知弹出的时刻: 事件就在那一天时写「今天」。
// 日期版式也走词典: 原文「1月2日」本身就是 Go 的时间版式, 各语言的译文同样写成 Go 版式。
func reminderText(o EventOccurrence, shown time.Time, t translator) (title, body string) {
	layout := t("1月2日")
	switch {
	case o.AllDay:
		body = t("{0} 全天", o.Start.Format(layout))
	case sameDay(o.Start, shown):
		body = t("今天 {0}", o.Start.Local().Format("15:04"))
	default:
		body = o.Start.Local().Format(layout + " 15:04")
	}
	if loc := strings.TrimSpace(o.Location); loc != "" {
		body += " · " + loc
	}
	title = o.Title
	if title == "" {
		title = t("(无标题日程)")
	}
	return title, body
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Local().Date()
	by, bm, bd := b.Local().Date()
	return ay == by && am == bm && ad == bd
}
