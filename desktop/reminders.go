package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/pim"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/notify"
)

// 日程提醒: 按事件的 alerts 在到点时弹系统通知。
//
// 规则与 Bulwark 一致:
//   - 事件 useDefaultAlerts 为真时用所属日历的默认提醒(全天与定时事件各一套)
//   - 支持相对开始、相对结束与绝对时间三种触发
//   - 已确认(acknowledged)的提醒不再触发
//   - 错过超过 10 分钟的提醒不补发(睡眠唤醒、应用刚启动时不会一口气弹出一堆旧提醒)
//
// 只在应用运行时生效(关窗后留在托盘即可)。已提醒记录只存内存。

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

func (a *App) runReminders(ctx context.Context) {
	state := &reminderState{fired: map[string]time.Time{}}
	t := time.NewTicker(reminderTick)
	defer t.Stop()
	for {
		a.checkReminders(state, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// checkReminders 触发时刻落在 (now-reminderStale, now] 且尚未提醒过的全部提醒。
func (a *App) checkReminders(state *reminderState, now time.Time) {
	if !a.settingOn("notifyEvents", true) {
		return
	}
	st, err := a.currentStore()
	if err != nil {
		return
	}
	occs, err := a.expandEvents(st, nil, now.Add(-reminderLookahead), now.Add(reminderLookahead))
	if err != nil {
		return
	}
	defaults := calendarDefaultAlerts(a.ctx, st)

	state.mu.Lock()
	defer state.mu.Unlock()
	for key, at := range state.fired {
		if now.Sub(at) > 2*reminderLookahead {
			delete(state.fired, key)
		}
	}

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

	var when string
	switch {
	case o.AllDay:
		when = o.Start.Format("1月2日") + " 全天"
	case sameDay(o.Start, time.Now()):
		when = "今天 " + o.Start.Local().Format("15:04")
	default:
		when = o.Start.Local().Format("1月2日 15:04")
	}
	body := when
	if loc := strings.TrimSpace(o.Location); loc != "" {
		body += " · " + loc
	}
	title := o.Title
	if title == "" {
		title = "(无标题日程)"
	}
	_ = n.Notify(ctx, notify.Notification{
		Title:    title,
		Body:     body,
		ActionID: "event:" + o.AccountID + "/" + o.EventID,
	})
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Local().Date()
	by, bm, bd := b.Local().Date()
	return ay == by && am == bm && ad == bd
}
