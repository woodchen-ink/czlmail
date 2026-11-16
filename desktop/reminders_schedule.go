package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// 移动端的日程提醒: 程序被系统挂起后 30 秒轮询 (reminders.go) 就不走了, 所以把未来一段时间的提醒
// 预先登记成系统的本地定时通知, 由系统到点弹出。
//
//   - 每次整表替换: 原生壳清掉上一批, 登记这一批 (宿主请求 scheduleReminders)
//   - 登记时机: 会话连上、日历同步落库、保存设置、退出登录、回到前台, 进程活着时每小时再滚动一次窗口
//   - 登记成功后轮询不再弹通知, 否则程序在前台时同一条提醒会弹两次; 登记失败 (原生壳不认识这个请求等) 时轮询照常
//   - 只登记还没到点的: 同步进来时已经过点的提醒不补发
//
// 桌面端常驻托盘, 不登记 (reminderHost 为 nil)。

const (
	// scheduledReminderMax 是一次登记的条数上限。iOS 每个程序最多 64 条待触发的本地通知, 超出的被系统丢弃
	// 且不保证丢哪些; 留几条余量。
	scheduledReminderMax = 60
	// reminderHorizon 是登记的时间范围。用户可能很多天不打开程序, 范围内的提醒照样会响。
	reminderHorizon = 30 * 24 * time.Hour
	// reminderReplanEvery 是进程活着时重新登记的间隔: 窗口随时间往后挪, 被上限挡在外面的提醒逐步补进来。
	reminderReplanEvery = time.Hour
	// reminderPlanDelay 把一轮同步里接连几次变化合并成一次登记。
	reminderPlanDelay = 2 * time.Second
)

// scheduledReminder 是交给原生壳的一条定时通知。At 为触发时刻 (Unix 秒)。
type scheduledReminder struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	ActionID string `json:"actionId"`
	At       int64  `json:"at"`
}

// reminderHost 把提醒表交给原生壳整表替换 (host_mobile.go); 桌面端为 nil。
var reminderHost func(context.Context, []scheduledReminder) error

type reminderSchedule struct {
	mu    sync.Mutex
	timer *time.Timer
	force bool

	// send 串行化登记: 两次计算交错时, 旧表不能盖掉新表。
	send sync.Mutex
	sent string
	// hosted 为真表示系统手里的提醒表是最新的, 轮询不必再弹。
	hosted atomic.Bool
}

// upcomingReminders 算出 (now, now+horizon] 内的提醒, 按触发时间取最近的 limit 条。
// 规则与 checkReminders 相同; 同一次发生在同一时刻的多条提醒 (事件同属两个日历、默认提醒相同) 只留一条。
func upcomingReminders(occs []EventOccurrence, defaults defaultAlerts, now time.Time, horizon time.Duration, limit int) []scheduledReminder {
	until := now.Add(horizon)
	seen := map[string]bool{}
	out := []scheduledReminder{}
	for _, o := range occs {
		alerts := o.Alerts
		if o.UseDefaultAlerts {
			alerts = defaults.forOccurrence(o)
		}
		for _, al := range alerts {
			at := al.FireTime(o.Start, o.End)
			if !at.After(now) || at.After(until) {
				continue
			}
			key := fmt.Sprintf("%s/%s/%s/%d", o.AccountID, o.EventID, o.RecurrenceID, at.Unix())
			if seen[key] {
				continue
			}
			seen[key] = true
			sum := sha256.Sum256([]byte(key))
			title, body := reminderText(o, at)
			out = append(out, scheduledReminder{
				ID:       hex.EncodeToString(sum[:8]),
				Title:    title,
				Body:     body,
				ActionID: "event:" + o.AccountID + "/" + o.EventID,
				At:       at.Unix(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// reminderPlan 是此刻应当登记的提醒表。未登录或关掉了日程提醒时为空表 (清掉已登记的);
// 读不到缓存时返回错误, 调用方保持原样不动。
func (a *App) reminderPlan(now time.Time) ([]scheduledReminder, error) {
	a.mu.RLock()
	configured := a.cfg.Configured()
	a.mu.RUnlock()
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	if !configured || !a.settingOn("notifyEvents", true) {
		return []scheduledReminder{}, nil
	}
	// 提醒可能比事件早很多 (提前一周), 也可能在事件开始之后 (相对结束、绝对时间): 两头各放宽 reminderLookahead。
	occs, err := a.expandEvents(st, nil, now.Add(-reminderLookahead), now.Add(reminderHorizon+reminderLookahead))
	if err != nil {
		return nil, err
	}
	return upcomingReminders(occs, calendarDefaultAlerts(a.ctx, st), now, reminderHorizon, scheduledReminderMax), nil
}

// syncReminders 重新计算并登记。force 为假时内容没变就不打扰原生壳;
// 回到前台与定时滚动用 force: 系统那边的登记可能已经丢了 (被清理、重装)。
func (a *App) syncReminders(force bool) {
	if reminderHost == nil {
		return
	}
	r := &a.reminders
	r.send.Lock()
	defer r.send.Unlock()

	list, err := a.reminderPlan(time.Now())
	if err != nil {
		a.log.Debug("plan reminders", "err", err)
		return
	}
	enc, err := json.Marshal(list)
	if err != nil {
		return
	}
	if !force && r.hosted.Load() && string(enc) == r.sent {
		return
	}
	if err := reminderHost(a.ctx, list); err != nil {
		// 交不出去就退回轮询, 程序开着时至少还能提醒。
		r.hosted.Store(false)
		r.sent = ""
		a.log.Warn("schedule reminders", "err", err)
		return
	}
	r.sent = string(enc)
	r.hosted.Store(true)
}

// planRemindersSoon 稍后登记一次; 等待期间的多次调用合并。
func (a *App) planRemindersSoon(force bool) {
	if reminderHost == nil {
		return
	}
	r := &a.reminders
	r.mu.Lock()
	defer r.mu.Unlock()
	r.force = r.force || force
	if r.timer != nil {
		return
	}
	r.timer = time.AfterFunc(reminderPlanDelay, func() {
		r.mu.Lock()
		force := r.force
		r.force = false
		r.timer = nil
		r.mu.Unlock()
		a.syncReminders(force)
	})
}
