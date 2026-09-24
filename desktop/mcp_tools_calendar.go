package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/woodchen-ink/czlmail/desktop/internal/pim"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 日历与任务工具。新建日程/任务、勾选完成直接可用;
// 修改删除已有日程与任务、回复邀请需开「允许 AI 整理邮件与日程」。
// 不提供添加参加者: 那会由服务器向他人发邀请, 等于发信。
// 时间一律是本机时间 YYYY-MM-DDTHH:MM:SS, 与界面相同, 写回时换算到事件自己的时区。

func (a *App) addCalendarTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "list_calendars", Description: "列出日历(含共享账号的日历)。isDefault 是新建日程的默认日历。", Annotations: readOnlyTool()},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			cals, err := a.ListCalendars()
			if err != nil {
				return nil, nil, err
			}
			out := make([]map[string]any, 0, len(cals))
			for _, c := range cals {
				out = append(out, map[string]any{"accountId": c.AccountID, "id": c.ID, "name": c.Name, "isDefault": c.IsDefault, "readOnly": !canWriteCalendar(c)})
			}
			return nil, map[string]any{"calendars": out}, nil
		})

	type eventsIn struct {
		From string `json:"from" jsonschema:"开始, RFC 3339 或 YYYY-MM-DD"`
		To   string `json:"to" jsonschema:"结束(不含), RFC 3339 或 YYYY-MM-DD; 最多 400 天"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_events",
		Description: "列出时间段内的日程(已展开重复日程)。修改或删除重复日程的某一次时带上该次的 recurrenceId。",
		Annotations: readOnlyTool(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in eventsIn) (*mcp.CallToolResult, any, error) {
		from, err := parseMCPTime(in.From)
		if err != nil {
			return nil, nil, err
		}
		to, err := parseMCPTime(in.To)
		if err != nil {
			return nil, nil, err
		}
		list, err := a.ListEvents(from.Format(time.RFC3339), to.Format(time.RFC3339))
		return nil, map[string]any{"events": list}, err
	})

	type getEventIn struct {
		AccountID string `json:"accountId"`
		EventID   string `json:"eventId"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "get_event", Description: "读取日程详情: 描述、重复规则、提醒、参加者及其回复状态、我的回复状态。", Annotations: readOnlyTool()},
		func(ctx context.Context, _ *mcp.CallToolRequest, in getEventIn) (*mcp.CallToolResult, any, error) {
			d, err := a.GetEvent(in.AccountID, in.EventID)
			return nil, d, err
		})

	type createEventIn struct {
		Title        string `json:"title"`
		Start        string `json:"start" jsonschema:"本机时间 YYYY-MM-DDTHH:MM:SS; 全天日程取当天零点"`
		End          string `json:"end" jsonschema:"本机时间; 全天日程为最后一天的次日零点"`
		AllDay       bool   `json:"allDay,omitempty"`
		Location     string `json:"location,omitempty"`
		Description  string `json:"description,omitempty"`
		AlertMinutes []int  `json:"alertMinutes,omitempty" jsonschema:"提前多少分钟提醒, 如 [15]"`
		AccountID    string `json:"accountId,omitempty" jsonschema:"与 calendarId 一起指定日历; 都省略时用个人默认日历"`
		CalendarID   string `json:"calendarId,omitempty" jsonschema:"见 list_calendars"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "create_event", Description: "新建日程(不邀请参加者)。", Annotations: writeTool(false, false)},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createEventIn) (*mcp.CallToolResult, any, error) {
			acc, cal, err := a.mcpCalendar(in.AccountID, in.CalendarID)
			if err != nil {
				return nil, nil, err
			}
			id, err := a.SaveEvent(EventInput{AccountID: acc, CalendarID: cal, Title: in.Title, Start: in.Start, End: in.End,
				AllDay: in.AllDay, Location: in.Location, Description: in.Description, AlertMinutes: in.AlertMinutes})
			return nil, map[string]any{"accountId": acc, "eventId": id}, err
		})

	type updateEventIn struct {
		AccountID    string  `json:"accountId"`
		EventID      string  `json:"eventId"`
		RecurrenceID string  `json:"recurrenceId,omitempty" jsonschema:"只改重复日程的这一次(取自 list_events); 省略则改整个系列"`
		Title        *string `json:"title,omitempty"`
		Start        *string `json:"start,omitempty" jsonschema:"本机时间 YYYY-MM-DDTHH:MM:SS"`
		End          *string `json:"end,omitempty" jsonschema:"本机时间"`
		AllDay       *bool   `json:"allDay,omitempty"`
		Location     *string `json:"location,omitempty"`
		Description  *string `json:"description,omitempty"`
		AlertMinutes []int   `json:"alertMinutes,omitempty" jsonschema:"替换提醒; 省略则不变"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "update_event",
		Description: "修改日程, 只改传入的字段。需要用户开启「允许 AI 整理邮件与日程」。有参加者的会议改动不会通知参加者。",
		Annotations: writeTool(true, true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in updateEventIn) (*mcp.CallToolResult, any, error) {
		if err := a.mcpRequire(store.SettingMCPAllowModify); err != nil {
			return nil, nil, err
		}
		d, err := a.GetEvent(in.AccountID, in.EventID)
		if err != nil {
			return nil, nil, err
		}
		if d.ReadOnly {
			return nil, nil, errors.New("this calendar is read-only")
		}
		ev := EventInput{
			AccountID: in.AccountID, ID: d.ID, CalendarID: d.CalendarID, Title: d.Title, Description: d.Description,
			Location: d.Location, Start: d.Start, End: d.End, AllDay: d.AllDay, TimeZone: d.TimeZone,
			Recurrence: d.Recurrence, AlertMinutes: d.AlertMinutes, VirtualLocation: d.VirtualLocation,
		}
		if in.RecurrenceID != "" {
			// 只改一次: 默认时间取这一次原本的时间, 而不是系列第一次的。
			if ev.Start, ev.End, err = occurrenceTimes(d, in.RecurrenceID); err != nil {
				return nil, nil, err
			}
			ev.Scope, ev.RecurrenceID = "this", in.RecurrenceID
		}
		setIf(&ev.Title, in.Title)
		setIf(&ev.Start, in.Start)
		setIf(&ev.End, in.End)
		setIf(&ev.AllDay, in.AllDay)
		setIf(&ev.Location, in.Location)
		setIf(&ev.Description, in.Description)
		if in.AlertMinutes != nil {
			ev.AlertMinutes = in.AlertMinutes
		}
		_, err = a.SaveEvent(ev)
		return nil, map[string]any{"ok": err == nil}, err
	})

	type deleteEventIn struct {
		AccountID    string `json:"accountId"`
		EventID      string `json:"eventId"`
		RecurrenceID string `json:"recurrenceId,omitempty" jsonschema:"只删重复日程的这一次; 省略则删除整个系列"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_event",
		Description: "删除日程或重复日程的某一次。需要用户开启「允许 AI 整理邮件与日程」。删除有参加者的会议时服务器会通知参加者。",
		Annotations: writeTool(true, true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deleteEventIn) (*mcp.CallToolResult, any, error) {
		if err := a.mcpRequire(store.SettingMCPAllowModify); err != nil {
			return nil, nil, err
		}
		scope := ""
		if in.RecurrenceID != "" {
			scope = "this"
		}
		err := a.DeleteEvent(in.AccountID, in.EventID, in.RecurrenceID, scope)
		return nil, map[string]any{"ok": err == nil}, err
	})

	type respondIn struct {
		AccountID string `json:"accountId"`
		EventID   string `json:"eventId"`
		Status    string `json:"status" jsonschema:"accepted(接受)、tentative(暂定)、declined(拒绝)"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "respond_event",
		Description: "以受邀者身份回复会议邀请, 服务器会把回复发给组织者。需要用户开启「允许 AI 整理邮件与日程」。",
		Annotations: outwardTool(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in respondIn) (*mcp.CallToolResult, any, error) {
		if err := a.mcpRequire(store.SettingMCPAllowModify); err != nil {
			return nil, nil, err
		}
		err := a.RespondEvent(in.AccountID, in.EventID, in.Status)
		return nil, map[string]any{"ok": err == nil}, err
	})

	a.addTaskTools(server)
}

func (a *App) addTaskTools(server *mcp.Server) {
	type listTasksIn struct {
		IncludeCompleted bool `json:"includeCompleted,omitempty" jsonschema:"同时列出已完成的任务"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "list_tasks", Description: "列出任务(待办), 未完成的在前、按截止时间排序。", Annotations: readOnlyTool()},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listTasksIn) (*mcp.CallToolResult, any, error) {
			list, err := a.ListTasks()
			if err != nil {
				return nil, nil, err
			}
			if !in.IncludeCompleted {
				list = slices.DeleteFunc(list, func(t TaskItem) bool { return t.Completed })
			}
			return nil, map[string]any{"tasks": list}, nil
		})

	type createTaskIn struct {
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
		Due         string `json:"due,omitempty" jsonschema:"截止时间, 本机时间 YYYY-MM-DDTHH:MM:SS; 省略表示无截止"`
		DueAllDay   bool   `json:"dueAllDay,omitempty" jsonschema:"截止到某天而不是具体时刻"`
		Priority    int    `json:"priority,omitempty" jsonschema:"0 无, 1 最高 … 9 最低(常用 1 高 / 5 中 / 9 低)"`
		AccountID   string `json:"accountId,omitempty" jsonschema:"与 calendarId 一起指定; 都省略时用个人默认日历"`
		CalendarID  string `json:"calendarId,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "create_task", Description: "新建任务(待办)。", Annotations: writeTool(false, false)},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createTaskIn) (*mcp.CallToolResult, any, error) {
			acc, cal, err := a.mcpCalendar(in.AccountID, in.CalendarID)
			if err != nil {
				return nil, nil, err
			}
			id, err := a.SaveTask(TaskInput{AccountID: acc, CalendarID: cal, Title: in.Title, Description: in.Description,
				Due: in.Due, DueAllDay: in.DueAllDay, Priority: in.Priority})
			return nil, map[string]any{"accountId": acc, "taskId": id}, err
		})

	type completeTaskIn struct {
		AccountID string `json:"accountId"`
		TaskID    string `json:"taskId"`
		Completed bool   `json:"completed" jsonschema:"true 标为完成, false 恢复为未完成"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "complete_task", Description: "把任务标为完成或未完成。", Annotations: writeTool(false, true)},
		func(ctx context.Context, _ *mcp.CallToolRequest, in completeTaskIn) (*mcp.CallToolResult, any, error) {
			err := a.SetTaskCompleted(in.AccountID, in.TaskID, in.Completed)
			return nil, map[string]any{"ok": err == nil}, err
		})

	type updateTaskIn struct {
		AccountID   string  `json:"accountId"`
		TaskID      string  `json:"taskId"`
		Title       *string `json:"title,omitempty"`
		Description *string `json:"description,omitempty"`
		Due         *string `json:"due,omitempty" jsonschema:"本机时间 YYYY-MM-DDTHH:MM:SS; 空字符串表示去掉截止时间"`
		DueAllDay   *bool   `json:"dueAllDay,omitempty"`
		Priority    *int    `json:"priority,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "update_task",
		Description: "修改任务, 只改传入的字段。需要用户开启「允许 AI 整理邮件与日程」。只是勾选完成请用 complete_task。",
		Annotations: writeTool(true, true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in updateTaskIn) (*mcp.CallToolResult, any, error) {
		if err := a.mcpRequire(store.SettingMCPAllowModify); err != nil {
			return nil, nil, err
		}
		t, err := a.findTask(in.AccountID, in.TaskID)
		if err != nil {
			return nil, nil, err
		}
		ti := TaskInput{AccountID: t.AccountID, ID: t.ID, CalendarID: t.CalendarID, Title: t.Title, Description: t.Description,
			Due: t.Due, DueAllDay: t.DueAllDay, Priority: t.Priority, Completed: t.Completed}
		setIf(&ti.Title, in.Title)
		setIf(&ti.Description, in.Description)
		setIf(&ti.Due, in.Due)
		setIf(&ti.DueAllDay, in.DueAllDay)
		setIf(&ti.Priority, in.Priority)
		_, err = a.SaveTask(ti)
		return nil, map[string]any{"ok": err == nil}, err
	})

	type deleteTaskIn struct {
		AccountID string `json:"accountId"`
		TaskID    string `json:"taskId"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_task",
		Description: "删除任务。需要用户开启「允许 AI 整理邮件与日程」。",
		Annotations: writeTool(true, true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deleteTaskIn) (*mcp.CallToolResult, any, error) {
		if err := a.mcpRequire(store.SettingMCPAllowModify); err != nil {
			return nil, nil, err
		}
		err := a.DeleteTask(in.AccountID, in.TaskID)
		return nil, map[string]any{"ok": err == nil}, err
	})
}

// mcpCalendar 解析新建日程/任务的日历: 指定了就用, 否则个人账号的默认日历(没有标默认时取第一个)。
func (a *App) mcpCalendar(accountID, calendarID string) (string, string, error) {
	if calendarID != "" {
		if accountID == "" {
			return "", "", errors.New("accountId is required together with calendarId")
		}
		return accountID, calendarID, nil
	}
	acc, err := a.mcpAccount(accountID)
	if err != nil {
		return "", "", err
	}
	cals, err := a.ListCalendars()
	if err != nil {
		return "", "", err
	}
	cal := ""
	for _, c := range cals {
		if c.AccountID == acc && canWriteCalendar(c) && (cal == "" || c.IsDefault) {
			cal = c.ID
		}
	}
	if cal == "" {
		return "", "", errors.New("no writable calendar in this account")
	}
	return acc, cal, nil
}

func canWriteCalendar(c store.Calendar) bool {
	return c.MyRights == nil || c.MyRights["mayWriteAll"] || c.MyRights["mayWriteOwn"]
}

func (a *App) findTask(accountID, id string) (*TaskItem, error) {
	list, err := a.ListTasks()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id && (accountID == "" || list[i].AccountID == accountID) {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("task %q not found", id)
}

// occurrenceTimes 是重复日程某一次原本的本机开始与结束时间。
// recurrenceId 是该次在事件自己时区里的 LocalDateTime, 时长沿用系列的。
func occurrenceTimes(d *EventDetail, recurrenceID string) (string, string, error) {
	loc := time.Local
	if d.TimeZone != "" && !d.AllDay {
		if l, err := time.LoadLocation(d.TimeZone); err == nil {
			loc = l
		}
	}
	at, err := time.ParseInLocation(pim.LocalDateTime, recurrenceID, loc)
	if err != nil {
		return "", "", fmt.Errorf("invalid recurrenceId %q", recurrenceID)
	}
	start, err1 := time.ParseInLocation(pim.LocalDateTime, d.Start, time.Local)
	end, err2 := time.ParseInLocation(pim.LocalDateTime, d.End, time.Local)
	if err1 != nil || err2 != nil {
		return "", "", errors.New("event has no valid start time")
	}
	at = at.In(time.Local)
	return at.Format(pim.LocalDateTime), at.Add(end.Sub(start)).Format(pim.LocalDateTime), nil
}

// setIf 在 v 非 nil 时覆盖 dst, 用于"只改传入字段"的补丁式工具。
func setIf[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}
