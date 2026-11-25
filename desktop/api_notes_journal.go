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

// defaultJournalName 是日记文件名的默认写法, 各语言相同 (星期按语言展开)。
const defaultJournalName = "YYYY-MM-DD-dddd"

// legacyJournalFolder 是早期不分语言的默认目录。库里已有「日记」目录时继续沿用, 旧日记不会从日历里消失。
const legacyJournalFolder = "日记/YYYY/MM"

// journalFolderNames 是默认日记目录名, 按界面语言选, 未列出的语言用英文。
var journalFolderNames = map[string]string{"zh": "日记", "en": "Journal", "cs": "Deník", "ru": "Дневник"}

// journalTemplates 是内置模板, 按界面语言选, 未列出的语言用英文。
var journalTemplates = map[string]string{
	"zh": "## 今日主题\n\n- \n\n## 今日待办事项\n\n- [ ] \n\n## 情绪/身体状态\n\n- \n\n## 笔记/灵感\n\n- \n\n## 今日总结\n\n- \n",
	"en": "## Highlights\n\n- \n\n## To do\n\n- [ ] \n\n## Mood & health\n\n- \n\n## Notes & ideas\n\n- \n\n## Summary\n\n- \n",
	"cs": "## Hlavní téma dne\n\n- \n\n## Úkoly\n\n- [ ] \n\n## Nálada a zdraví\n\n- \n\n## Poznámky a nápady\n\n- \n\n## Shrnutí dne\n\n- \n",
	"ru": "## Главное за день\n\n- \n\n## Задачи\n\n- [ ] \n\n## Настроение и самочувствие\n\n- \n\n## Заметки и идеи\n\n- \n\n## Итоги дня\n\n- \n",
}

// langBase 把界面语言 (zh-CN、cs…) 归成两字母代码。
func langBase(lang string) string {
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(lang)), "-")
	return base
}

// localized 取某语言的值, 没有就用英文。
func localized(values map[string]string, lang string) string {
	if v, ok := values[langBase(lang)]; ok {
		return v
	}
	return values["en"]
}

// GetJournalConfig 读日记设置, 未设置的项取默认值; 默认目录按界面语言 lang 命名。
// 目录存成空字符串表示笔记根目录, 只有从未设置过才取默认值。
func (a *App) GetJournalConfig(lang string) JournalConfig {
	cfg, _ := a.journalConfig(lang)
	return cfg
}

// journalConfig 读日记设置, folderSet 表示目录已存进设置 (否则是按语言推出的默认值)。
func (a *App) journalConfig(lang string) (cfg JournalConfig, folderSet bool) {
	cfg = JournalConfig{Name: defaultJournalName}
	if st, err := a.currentStore(); err == nil {
		if v, err := st.StringSetting(a.ctx, settingJournalFolder); err == nil {
			cfg.Folder, folderSet = v, true
		}
		if v, err := st.StringSetting(a.ctx, settingJournalName); err == nil && v != "" {
			cfg.Name = v
		}
		if v, err := st.StringSetting(a.ctx, settingJournalTemplate); err == nil {
			cfg.Template = v
		}
	}
	if !folderSet {
		cfg.Folder = a.defaultJournalFolder(lang)
	}
	return cfg, folderSet
}

// defaultJournalFolder 是未设置时的日记目录: 库里已有旧默认的「日记」目录就沿用, 否则按界面语言命名。
func (a *App) defaultJournalFolder(lang string) string {
	if s, err := a.noteService(); err == nil {
		if vaults, err := s.Vaults(); err == nil {
			legacyRoot, _, _ := strings.Cut(legacyJournalFolder, "/")
			for _, v := range vaults {
				if s.HasFolder(v.ID, legacyRoot) {
					return legacyJournalFolder
				}
			}
		}
	}
	return localized(journalFolderNames, lang) + "/YYYY/MM"
}

// rememberJournalFolder 第一次打开日记时把默认目录存进设置, 之后换界面语言或在别的设备上日记仍在同一处。
func (a *App) rememberJournalFolder(folder string) {
	st, err := a.currentStore()
	if err != nil {
		return
	}
	if err := st.SetStringSetting(a.ctx, settingJournalFolder, folder); err != nil {
		a.log.Warn("remember journal folder", "err", err)
		return
	}
	a.syncAISettingsSoon()
}

// SetJournalConfig 校验后保存日记设置。空的文件名恢复默认, 空的位置表示笔记根目录。
func (a *App) SetJournalConfig(cfg JournalConfig) (JournalConfig, error) {
	cfg.Folder = strings.Trim(strings.TrimSpace(cfg.Folder), "/")
	cfg.Name = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(cfg.Name), ".md"))
	cfg.Template = strings.Trim(strings.TrimSpace(cfg.Template), "/")
	if cfg.Name == "" {
		cfg.Name = defaultJournalName
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
	cfg, folderSet := a.journalConfig(lang)
	folders, name, err := journalPath(cfg, at, lang)
	if err != nil {
		return notes.Entry{}, err
	}
	template := localized(journalTemplates, lang)
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
	if err == nil && !folderSet {
		a.rememberJournalFolder(cfg.Folder)
	}
	if created {
		a.wakeNotes(true)
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
	cfg := a.GetJournalConfig(lang)
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
