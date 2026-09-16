// Package pim 解析日历事件(JSCalendar)与联系人(JSContact), 并把重复事件展开成具体的发生次。
//
// 与 jmapx 分开: jmapx 只管协议往返, 这里只管对象语义, 不碰网络与数据库, 便于单测。
package pim

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Windows 没有系统 IANA 时区库, 事件的 timeZone 必须能解析。

	"github.com/teambition/rrule-go"
)

// LocalDateTime 是 JSCalendar 的无时区时间格式。
const LocalDateTime = "2006-01-02T15:04:05"

// Event 是 JSCalendar Event 中本客户端用到的字段。其余字段保留在原始 JSON 里。
type Event struct {
	Type            string              `json:"@type"`
	ID              string              `json:"id"`
	UID             string              `json:"uid"`
	CalendarIDs     map[string]bool     `json:"calendarIds"`
	Title           string              `json:"title"`
	Description     string              `json:"description"`
	Start           string              `json:"start"`
	TimeZone        *string             `json:"timeZone"`
	Duration        string              `json:"duration"`
	ShowWithoutTime bool                `json:"showWithoutTime"`
	Status          string              `json:"status"`
	Updated         string              `json:"updated"`
	Locations       map[string]location `json:"locations"`
	RecurrenceRules []recurrenceRule    `json:"recurrenceRules"`
	// RecurrenceRule 是 JSCalendar 2.0 草案的单数写法, Stalwart 只认这个。
	RecurrenceRule      *recurrenceRule            `json:"recurrenceRule"`
	RecurrenceOverrides map[string]json.RawMessage `json:"recurrenceOverrides"`
	Alerts              map[string]alert           `json:"alerts"`
	// UseDefaultAlerts 为真时忽略 Alerts, 改用所属日历的默认提醒。
	UseDefaultAlerts bool `json:"useDefaultAlerts"`

	// 任务(VTODO)专有字段。Stalwart 把任务也放在 CalendarEvent 里。
	Due             *string `json:"due"`
	Progress        *string `json:"progress"`
	PercentComplete *int    `json:"percentComplete"`
	Priority        int     `json:"priority"`
}

// IsTask 判断对象是任务而不是事件。CalDAV 客户端(如 Thunderbird)建的任务可能没有
// @type=Task, 按任务专有字段识别(RFC 8984 §5.2 这些字段只属于 Task)。
func (e *Event) IsTask() bool {
	if strings.EqualFold(e.Type, "task") {
		return true
	}
	return e.Type != "Event" && ((e.Progress != nil && *e.Progress != "") || (e.Due != nil && *e.Due != "") || e.PercentComplete != nil)
}

type location struct {
	Name string `json:"name"`
}

type alert struct {
	Trigger struct {
		Type       string `json:"@type"`
		Offset     string `json:"offset"`
		RelativeTo string `json:"relativeTo"`
		When       string `json:"when"`
	} `json:"trigger"`
	Acknowledged string `json:"acknowledged"`
}

// Alert 是解析后的提醒。
type Alert struct {
	// Offset 相对开始(或 RelativeToEnd 时相对结束)的偏移, 负数表示提前。
	Offset        time.Duration
	RelativeToEnd bool
	// At 非零时为绝对时间触发。
	At time.Time
}

// FireTime 返回提醒在某次发生上的触发时刻。
func (a Alert) FireTime(start, end time.Time) time.Time {
	if !a.At.IsZero() {
		return a.At
	}
	if a.RelativeToEnd {
		return end.Add(a.Offset)
	}
	return start.Add(a.Offset)
}

// ParseAlerts 解析 alerts 映射(事件的 alerts 或日历的 defaultAlertsWithTime)。
// 已确认(acknowledged)的提醒不再触发。
func ParseAlerts(raw []byte) []Alert {
	var m map[string]alert
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return convertAlerts(m)
}

func convertAlerts(m map[string]alert) []Alert {
	var out []Alert
	for _, a := range m {
		if a.Acknowledged != "" {
			continue
		}
		switch a.Trigger.Type {
		case "AbsoluteTrigger":
			if t, err := time.Parse(time.RFC3339, a.Trigger.When); err == nil {
				out = append(out, Alert{At: t})
			}
		case "", "OffsetTrigger":
			neg := strings.HasPrefix(a.Trigger.Offset, "-")
			d, err := ParseDuration(strings.TrimLeft(a.Trigger.Offset, "+-"))
			if err != nil {
				continue
			}
			if neg {
				d = -d
			}
			out = append(out, Alert{Offset: d, RelativeToEnd: a.Trigger.RelativeTo == "end"})
		}
	}
	return out
}

// NDay 是重复规则里的星期项。
type NDay = nDay

type nDay struct {
	Day         string `json:"day"`
	NthOfPeriod int    `json:"nthOfPeriod"`
}

// RecurrenceRule 是 JSCalendar 的重复规则。
type RecurrenceRule = recurrenceRule

type recurrenceRule struct {
	Frequency      string   `json:"frequency"`
	Interval       int      `json:"interval"`
	RScale         string   `json:"rscale"`
	FirstDayOfWeek string   `json:"firstDayOfWeek"`
	ByDay          []nDay   `json:"byDay"`
	ByMonthDay     []int    `json:"byMonthDay"`
	ByMonth        []string `json:"byMonth"`
	ByYearDay      []int    `json:"byYearDay"`
	ByWeekNo       []int    `json:"byWeekNo"`
	ByHour         []int    `json:"byHour"`
	ByMinute       []int    `json:"byMinute"`
	BySecond       []int    `json:"bySecond"`
	BySetPosition  []int    `json:"bySetPosition"`
	Count          int      `json:"count"`
	Until          string   `json:"until"`
}

// ParseEvent 解析事件。
func ParseEvent(raw []byte) (*Event, error) {
	var e Event
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, fmt.Errorf("decode event: %w", err)
	}
	return &e, nil
}

// Location 返回第一个地点名称。
func (e *Event) Location() string {
	keys := make([]string, 0, len(e.Locations))
	for k := range e.Locations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if n := strings.TrimSpace(e.Locations[k].Name); n != "" {
			return n
		}
	}
	return ""
}

// CalendarIDList 返回所属日历 id。
func (e *Event) CalendarIDList() []string {
	out := make([]string, 0, len(e.CalendarIDs))
	for id, on := range e.CalendarIDs {
		if on {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Loc 返回事件时区。没有时区(浮动时间)或是全天事件时按本机时区显示:
// 全天事件的"9 月 16 日"在任何时区都应当是 9 月 16 日, 不能换算。
func (e *Event) Loc() *time.Location {
	if e.ShowWithoutTime || e.TimeZone == nil || *e.TimeZone == "" {
		return time.Local
	}
	if loc, err := time.LoadLocation(*e.TimeZone); err == nil {
		return loc
	}
	return time.Local
}

// StartTime 返回首次发生的开始时间。
func (e *Event) StartTime() (time.Time, error) {
	return parseLocal(e.Start, e.Loc())
}

// DurationValue 返回持续时间。全天事件缺省一天, 其余缺省为零(RFC 8984 的默认值)。
func (e *Event) DurationValue() time.Duration {
	if d, err := ParseDuration(e.Duration); err == nil && e.Duration != "" {
		return d
	}
	if e.ShowWithoutTime {
		return 24 * time.Hour
	}
	return 0
}

// Rules 合并单数与复数两种写法的重复规则。
func (e *Event) Rules() []recurrenceRule {
	rules := append([]recurrenceRule{}, e.RecurrenceRules...)
	if e.RecurrenceRule != nil {
		rules = append(rules, *e.RecurrenceRule)
	}
	return rules
}

// IsRecurring 报告事件是否重复。
func (e *Event) IsRecurring() bool {
	return len(e.Rules()) > 0 || len(e.RecurrenceOverrides) > 0
}

// Span 返回用于范围索引的 [start, end] UTC 秒。无限重复的事件 end 为 nil。
func (e *Event) Span() (start int64, end *int64, err error) {
	st, err := e.StartTime()
	if err != nil {
		return 0, nil, err
	}
	dur := e.DurationValue()
	last := st
	if rules := e.Rules(); len(rules) > 0 {
		open := false
		for _, rule := range rules {
			if rule.Count == 0 && rule.Until == "" {
				open = true
				break
			}
			r, err := e.rrule(rule, st)
			if err != nil {
				open = true
				break
			}
			if all := r.All(); len(all) > 0 && all[len(all)-1].After(last) {
				last = all[len(all)-1]
			}
		}
		if open {
			return st.Unix(), nil, nil
		}
	}
	// 覆盖项里可能把某次挪到更晚, 或追加了规则之外的日期。
	for key := range e.RecurrenceOverrides {
		if t, err := parseLocal(key, e.Loc()); err == nil && t.After(last) {
			last = t
		}
	}
	endAt := last.Add(dur).Unix()
	return st.Unix(), &endAt, nil
}

// Occurrence 是事件的一次具体发生。
type Occurrence struct {
	EventID string `json:"eventId"`
	// RecurrenceID 是该次在重复序列里的原始开始时间(LocalDateTime), 非重复事件为空。
	// 修改或删除"仅此一次"时以它为 recurrenceOverrides 的键。
	RecurrenceID string    `json:"recurrenceId"`
	Title        string    `json:"title"`
	Location     string    `json:"location"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	AllDay       bool      `json:"allDay"`
	CalendarIDs  []string  `json:"calendarIds"`
	Recurring    bool      `json:"recurring"`
	Status       string    `json:"status"`
	// Alerts 是该次的提醒; UseDefaultAlerts 为真时调用方应改用日历默认提醒。
	Alerts           []Alert `json:"-"`
	UseDefaultAlerts bool    `json:"-"`
}

// maxOccurrences 防止一条"每秒重复"的畸形规则在展开时耗尽内存。
const maxOccurrences = 5000

// Expand 返回与 [from, to) 有交集的全部发生次, 按开始时间排序。
func (e *Event) Expand(from, to time.Time) ([]Occurrence, error) {
	st, err := e.StartTime()
	if err != nil {
		return nil, err
	}
	dur := e.DurationValue()
	loc := e.Loc()

	base := Occurrence{
		EventID:          e.ID,
		Title:            e.Title,
		Location:         e.Location(),
		AllDay:           e.ShowWithoutTime,
		CalendarIDs:      e.CalendarIDList(),
		Recurring:        e.IsRecurring(),
		Status:           e.Status,
		Alerts:           convertAlerts(e.Alerts),
		UseDefaultAlerts: e.UseDefaultAlerts,
	}

	if !e.IsRecurring() {
		occ := base
		occ.Start, occ.End = st, st.Add(dur)
		if overlaps(occ, from, to) {
			return []Occurrence{occ}, nil
		}
		return nil, nil
	}

	// 先收集规则生成的原始开始时间(以 LocalDateTime 为键去重), 再叠加覆盖项。
	starts := map[string]time.Time{e.Start: st}
	for _, rule := range e.Rules() {
		r, err := e.rrule(rule, st)
		if err != nil {
			continue
		}
		// 向前多取一个持续时间: 开始于区间之前、结束于区间之内的也要算。
		for i, t := range r.Between(from.Add(-dur-time.Second), to, true) {
			if i >= maxOccurrences {
				break
			}
			starts[t.In(loc).Format(LocalDateTime)] = t
		}
	}
	for key := range e.RecurrenceOverrides {
		if _, ok := starts[key]; ok {
			continue
		}
		if t, err := parseLocal(key, loc); err == nil {
			starts[key] = t
		}
	}

	var out []Occurrence
	for key, t := range starts {
		occ := base
		occ.RecurrenceID = key
		occ.Start, occ.End = t, t.Add(dur)
		if patch, ok := e.RecurrenceOverrides[key]; ok {
			if !applyOverride(&occ, patch, loc) {
				continue
			}
		}
		if overlaps(occ, from, to) {
			out = append(out, occ)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

// applyOverride 应用单次覆盖, 返回 false 表示该次被排除。
// 只处理顶层的常用属性; 深层 JSON Pointer 补丁(如参与人状态)不影响日程显示。
func applyOverride(occ *Occurrence, patch json.RawMessage, loc *time.Location) bool {
	var p map[string]json.RawMessage
	if json.Unmarshal(patch, &p) != nil {
		return true
	}
	var excluded bool
	if v, ok := p["excluded"]; ok && json.Unmarshal(v, &excluded) == nil && excluded {
		return false
	}
	dur := occ.End.Sub(occ.Start)
	var s string
	if v, ok := p["title"]; ok && json.Unmarshal(v, &s) == nil {
		occ.Title = s
	}
	if v, ok := p["duration"]; ok && json.Unmarshal(v, &s) == nil {
		if d, err := ParseDuration(s); err == nil {
			dur = d
		}
	}
	if v, ok := p["start"]; ok && json.Unmarshal(v, &s) == nil {
		if t, err := parseLocal(s, loc); err == nil {
			occ.Start = t
		}
	}
	occ.End = occ.Start.Add(dur)
	return true
}

func overlaps(o Occurrence, from, to time.Time) bool {
	end := o.End
	if !end.After(o.Start) {
		// 零时长事件按一个时刻处理。
		end = o.Start.Add(time.Second)
	}
	return o.Start.Before(to) && end.After(from)
}

var weekdays = map[string]rrule.Weekday{
	"mo": rrule.MO, "tu": rrule.TU, "we": rrule.WE, "th": rrule.TH,
	"fr": rrule.FR, "sa": rrule.SA, "su": rrule.SU,
}

var frequencies = map[string]rrule.Frequency{
	"yearly": rrule.YEARLY, "monthly": rrule.MONTHLY, "weekly": rrule.WEEKLY, "daily": rrule.DAILY,
	"hourly": rrule.HOURLY, "minutely": rrule.MINUTELY, "secondly": rrule.SECONDLY,
}

var errUnsupportedRule = errors.New("unsupported recurrence rule")

func (e *Event) rrule(rule recurrenceRule, start time.Time) (*rrule.RRule, error) {
	freq, ok := frequencies[strings.ToLower(rule.Frequency)]
	// 农历等非公历 rscale 无法用公历规则展开, 按不重复处理而不是算出错误的日期。
	if !ok || (rule.RScale != "" && !strings.EqualFold(rule.RScale, "gregorian")) {
		return nil, errUnsupportedRule
	}
	opt := rrule.ROption{
		Freq:       freq,
		Dtstart:    start,
		Interval:   rule.Interval,
		Count:      rule.Count,
		Bymonthday: rule.ByMonthDay,
		Byyearday:  rule.ByYearDay,
		Byweekno:   rule.ByWeekNo,
		Byhour:     rule.ByHour,
		Byminute:   rule.ByMinute,
		Bysecond:   rule.BySecond,
		Bysetpos:   rule.BySetPosition,
	}
	if wk, ok := weekdays[strings.ToLower(rule.FirstDayOfWeek)]; ok {
		opt.Wkst = wk
	}
	for _, d := range rule.ByDay {
		wd, ok := weekdays[strings.ToLower(d.Day)]
		if !ok {
			continue
		}
		if d.NthOfPeriod != 0 {
			wd = wd.Nth(d.NthOfPeriod)
		}
		opt.Byweekday = append(opt.Byweekday, wd)
	}
	for _, m := range rule.ByMonth {
		if n, err := strconv.Atoi(strings.TrimSuffix(m, "L")); err == nil {
			opt.Bymonth = append(opt.Bymonth, n)
		}
	}
	if rule.Until != "" {
		if t, err := parseLocal(rule.Until, start.Location()); err == nil {
			opt.Until = t
		}
	}
	return rrule.NewRRule(opt)
}

// parseLocal 解析 LocalDateTime; 带 Z 或偏移的 UTCDateTime 也接受。
func parseLocal(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(loc), nil
	}
	if len(s) == len("2006-01-02") {
		s += "T00:00:00"
	}
	return time.ParseInLocation(LocalDateTime, s, loc)
}

var durationPattern = regexp.MustCompile(`^P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:\.\d+)?)S)?)?$`)

// ParseDuration 解析 ISO 8601 持续时间(JSCalendar 只用到 W/D/H/M/S)。
func ParseDuration(s string) (time.Duration, error) {
	m := durationPattern.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
	if m == nil || s == "P" || s == "PT" {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	num := func(i int) float64 {
		if m[i] == "" {
			return 0
		}
		f, _ := strconv.ParseFloat(m[i], 64)
		return f
	}
	d := time.Duration(num(1)*7*24)*time.Hour +
		time.Duration(num(2)*24)*time.Hour +
		time.Duration(num(3))*time.Hour +
		time.Duration(num(4))*time.Minute +
		time.Duration(num(5)*float64(time.Second))
	return d, nil
}

// FormatDuration 生成 ISO 8601 持续时间, 用于写回服务器。
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "PT0S"
	}
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	sec := d / time.Second

	var b strings.Builder
	b.WriteString("P")
	if days > 0 {
		fmt.Fprintf(&b, "%dD", days)
	}
	if h > 0 || m > 0 || sec > 0 {
		b.WriteString("T")
		if h > 0 {
			fmt.Fprintf(&b, "%dH", h)
		}
		if m > 0 {
			fmt.Fprintf(&b, "%dM", m)
		}
		if sec > 0 {
			fmt.Fprintf(&b, "%dS", sec)
		}
	}
	return b.String()
}

// ParseLocalIn 解析 LocalDateTime(或带偏移的 UTCDateTime)。
func ParseLocalIn(s string, loc *time.Location) (time.Time, error) {
	return parseLocal(s, loc)
}
