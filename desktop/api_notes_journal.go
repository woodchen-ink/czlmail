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
