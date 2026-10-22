package main

import (
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/notes"
)

// 日记设置随 AI/MCP 设置一起经网盘同步 (aiSyncedSettings), 换设备不用重配。
const (
	settingJournalFolder   = "notesJournalFolder"
	settingJournalName     = "notesJournalName"
	settingJournalTemplate = "notesJournalTemplate"
)

// JournalConfig 是日记的位置与模板。Folder/Name 用 Obsidian 的日期写法 (YYYY MM DD dddd…)。
type JournalConfig struct {
	Folder string `json:"folder"`
	Name   string `json:"name"`
	// Template 是笔记库内的模板笔记路径, 空则用内置模板。
	Template string `json:"template"`
}

var defaultJournal = JournalConfig{Folder: "日记/YYYY/MM", Name: "YYYY-MM-DD-dddd"}

// journalTemplates 是内置模板, 按界面语言选。
var journalTemplates = map[string]string{
	"zh": "## 今日主题\n\n- \n\n## 今日待办事项\n\n- [ ] \n\n## 情绪/身体状态\n\n- \n\n## 笔记/灵感\n\n- \n\n## 今日总结\n\n- \n",
	"en": "## Highlights\n\n- \n\n## To do\n\n- [ ] \n\n## Mood & health\n\n- \n\n## Notes & ideas\n\n- \n\n## Summary\n\n- \n",
}

// GetJournalConfig 读日记设置, 未设置的项取默认值。
func (a *App) GetJournalConfig() JournalConfig {
	cfg := defaultJournal
	st, err := a.currentStore()
	if err != nil {
		return cfg
	}
	for key, field := range map[string]*string{settingJournalFolder: &cfg.Folder, settingJournalName: &cfg.Name, settingJournalTemplate: &cfg.Template} {
		if v, err := st.StringSetting(a.ctx, key); err == nil && (v != "" || key == settingJournalTemplate) {
			*field = v
		}
	}
	return cfg
}

// SetJournalConfig 校验后保存日记设置, 空的位置与文件名恢复默认。
func (a *App) SetJournalConfig(cfg JournalConfig) (JournalConfig, error) {
	cfg.Folder = strings.Trim(strings.TrimSpace(cfg.Folder), "/")
	cfg.Name = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(cfg.Name), ".md"))
	cfg.Template = strings.Trim(strings.TrimSpace(cfg.Template), "/")
	if cfg.Name == "" {
		cfg.Name = defaultJournal.Name
	}
	if _, _, err := journalPath(cfg, time.Now(), "zh"); err != nil {
		return cfg, err
	}
	st, err := a.currentStore()
	if err != nil {
		return cfg, err
	}
	for key, value := range map[string]string{settingJournalFolder: cfg.Folder, settingJournalName: cfg.Name, settingJournalTemplate: cfg.Template} {
		if err := st.SetStringSetting(a.ctx, key, value); err != nil {
			return cfg, err
		}
	}
	a.syncAISettingsSoon()
	return cfg, nil
}

// OpenJournal 打开某天 (本地日期 YYYY-MM-DD) 的日记, 没有就按模板新建; 已有的日记原样返回。
func (a *App) OpenJournal(vault, date, lang string) (notes.Entry, error) {
	s, err := a.noteService()
	if err != nil {
		return notes.Entry{}, err
	}
	now := time.Now()
	day, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		return notes.Entry{}, notes.ErrInvalid
	}
	// 模板里的 {{time}} 取当前钟点, 日期取所选那天。
	at := time.Date(day.Year(), day.Month(), day.Day(), now.Hour(), now.Minute(), now.Second(), 0, time.Local)
	cfg := a.GetJournalConfig()
	folders, name, err := journalPath(cfg, at, lang)
	if err != nil {
		return notes.Entry{}, err
	}
	template := journalTemplates["en"]
	if strings.HasPrefix(lang, "zh") {
		template = journalTemplates["zh"]
	}
	if cfg.Template != "" {
		path := cfg.Template
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			path += ".md"
		}
		tpl, err := s.ReadPath(vault, path)
		if err != nil {
			return notes.Entry{}, err
		}
		template = tpl.Content
	}
	e, created, err := s.EnsureNote(vault, folders, name, notes.ExpandTemplate(template, at, name, lang))
	if created {
		a.wakeNotes()
	}
	return e, err
}

// journalPath 把设置展开成目录链与文件名。
func journalPath(cfg JournalConfig, at time.Time, lang string) ([]string, string, error) {
	folders := []string{}
	for _, part := range strings.Split(notes.FormatDate(cfg.Folder, at, lang), "/") {
		if part = strings.TrimSpace(part); part != "" {
			folders = append(folders, part)
		}
	}
	name := strings.TrimSpace(notes.FormatDate(cfg.Name, at, lang))
	if name == "" || strings.ContainsAny(name, "/\\") {
		return nil, "", notes.ErrInvalid
	}
	return folders, name, nil
}

// NoteDayItem 是日历某天下列出的一篇笔记。
type NoteDayItem struct {
	VaultID string `json:"vaultId"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	// Journal 表示这篇是当天的日记 (按日记设置推出的路径)。
	Journal bool `json:"journal"`
}

// NoteDay 是日历一天的笔记, 日记排在最前。
type NoteDay struct {
	Day   string        `json:"day"`
	Notes []NoteDayItem `json:"notes"`
}

// maxNotesPerDay 限制每天列出的笔记数, 悬浮列表不必列全。
const maxNotesPerDay = 12

// ListNotesByDay 列出 [from, to) 每天的日记以及当天新建或修改过的笔记, 日期为本地 YYYY-MM-DD。
// 笔记模块关闭或尚未初始化时返回空列表而不是报错: 日历不应因此弹错误。
func (a *App) ListNotesByDay(from, to, lang string) ([]NoteDay, error) {
	start, err := time.ParseInLocation("2006-01-02", from, time.Local)
	if err != nil {
		return nil, notes.ErrInvalid
	}
	end, err := time.ParseInLocation("2006-01-02", to, time.Local)
	if err != nil || !end.After(start) || end.Sub(start) > 62*24*time.Hour {
		return nil, notes.ErrInvalid
	}
	s, err := a.noteService()
	if err != nil || !s.GetConfig().Enabled {
		return []NoteDay{}, nil
	}
	vaults, err := s.Vaults()
	if err != nil {
		return []NoteDay{}, nil
	}
	cfg := a.GetJournalConfig()
	days := map[string]*NoteDay{}
	order := []string{}
	add := func(day string, item NoteDayItem) {
		d := days[day]
		if d == nil {
			d = &NoteDay{Day: day}
			days[day] = d
			order = append(order, day)
		}
		for _, existing := range d.Notes {
			if existing.ID == item.ID {
				return
			}
		}
		if len(d.Notes) < maxNotesPerDay {
			d.Notes = append(d.Notes, item)
		}
	}
	for _, v := range vaults {
		for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
			folders, name, err := journalPath(cfg, day, lang)
			if err != nil {
				break
			}
			path := strings.Join(append(folders, name+".md"), "/")
			if e, err := s.ReadPath(v.ID, path); err == nil {
				add(day.Format("2006-01-02"), NoteDayItem{VaultID: v.ID, ID: e.ID, Name: e.Name, Path: strings.Join(folders, "/"), Journal: true})
			}
		}
		touched, err := s.Touched(v.ID, start.UnixMilli(), end.UnixMilli())
		if err != nil {
			continue
		}
		for _, e := range touched {
			item := NoteDayItem{VaultID: v.ID, ID: e.ID, Name: e.Name, Path: e.Path}
			for _, at := range []int64{e.Created, e.Modified} {
				if t := time.UnixMilli(at); at > 0 && !t.Before(start) && t.Before(end) {
					add(t.Format("2006-01-02"), item)
				}
			}
		}
	}
	out := make([]NoteDay, 0, len(order))
	for _, day := range order {
		out = append(out, *days[day])
	}
	return out, nil
}
