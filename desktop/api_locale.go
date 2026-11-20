package main

// 后端自己展示给用户的文案 (系统通知、托盘菜单、文件对话框) 按界面语言选。
// 界面的 lib/i18n 管其余全部文案; 这里只放界面无法代劳的部分。
// 加一种语言: 在 uiTexts 里加一项 (key 与前端 lib/i18n 的语言 id 相同), 测试会检查每个字段都填了。

// defaultUILocale 是界面还没告知语言时的回落, 与前端 lib/i18n 的默认一致。
const defaultUILocale = "zh-CN"

// uiText 是一种语言的后端文案。带 %s / %d 的按 fmt 格式化; DateLayout 是 time 的版式。
type uiText struct {
	UpdateTitle string // %s = 版本号
	UpdateBody  string

	NewMailTitle string
	NewMailCount string // %d = 封数
	NoSubject    string

	SignInTitle string
	SignInBody  string // %s = 用户名

	NoEventTitle   string
	DateLayout     string // 提醒里的日期, 如 1月2日
	ReminderAllDay string // %s = 日期
	ReminderToday  string // %s = 钟点

	TrayOpen   string
	TrayQuit   string
	TrayUnread string // %d = 未读数

	SaveAttachment  string
	AddAttachments  string
	UploadFiles     string
	UploadFolder    string
	ExportEmail     string
	EmailFiles      string
	ImportEmail     string
	EmailOrZipFiles string
	ImportCalendar  string
	ImportContacts  string
	ImportNoteVault string
	ExportNoteVault string
	ImportTemplates string
	TemplateFiles   string
}

var uiTexts = map[string]uiText{
	"zh-CN": {
		UpdateTitle: "CZL Mail %s 已发布", UpdateBody: "打开「设置 → 关于与更新」一键更新",
		NewMailTitle: "新邮件", NewMailCount: "收到 %d 封新邮件", NoSubject: "(无主题)",
		SignInTitle: "登录已失效", SignInBody: "%s 需要重新登录后才能继续收发邮件",
		NoEventTitle: "(无标题日程)", DateLayout: "1月2日", ReminderAllDay: "%s 全天", ReminderToday: "今天 %s",
		TrayOpen: "打开 CZL Mail", TrayQuit: "退出 CZL Mail", TrayUnread: "CZL Mail — %d 封未读",
		SaveAttachment: "保存附件", AddAttachments: "添加附件", UploadFiles: "上传文件", UploadFolder: "上传文件夹",
		ExportEmail: "导出邮件", EmailFiles: "邮件 (*.eml)", ImportEmail: "导入邮件", EmailOrZipFiles: "邮件或压缩包 (*.eml, *.zip)",
		ImportCalendar: "导入日历", ImportContacts: "导入联系人",
		ImportNoteVault: "导入 Markdown 笔记库", ExportNoteVault: "导出笔记库",
		ImportTemplates: "导入邮件模板", TemplateFiles: "模板文件 (*.json)",
	},
	"en": {
		UpdateTitle: "CZL Mail %s is available", UpdateBody: "Open Settings → About and updates to install it",
		NewMailTitle: "New mail", NewMailCount: "%d new emails", NoSubject: "(No subject)",
		SignInTitle: "Signed out", SignInBody: "Sign in again to keep sending and receiving mail as %s",
		NoEventTitle: "(Untitled event)", DateLayout: "Jan 2", ReminderAllDay: "%s, all day", ReminderToday: "Today %s",
		TrayOpen: "Open CZL Mail", TrayQuit: "Quit CZL Mail", TrayUnread: "CZL Mail — %d unread",
		SaveAttachment: "Save attachment", AddAttachments: "Add attachments", UploadFiles: "Upload files", UploadFolder: "Upload folder",
		ExportEmail: "Export email", EmailFiles: "Email (*.eml)", ImportEmail: "Import email", EmailOrZipFiles: "Email or archive (*.eml, *.zip)",
		ImportCalendar: "Import calendar", ImportContacts: "Import contacts",
		ImportNoteVault: "Import Markdown vault", ExportNoteVault: "Export vault",
		ImportTemplates: "Import email templates", TemplateFiles: "Template file (*.json)",
	},
	"cs": {
		UpdateTitle: "Byla vydána verze CZL Mail %s", UpdateBody: "Aktualizovat můžete v Nastavení → O aplikaci a aktualizace",
		NewMailTitle: "Nová pošta", NewMailCount: "Nové zprávy: %d", NoSubject: "(Bez předmětu)",
		SignInTitle: "Přihlášení vypršelo", SignInBody: "Účet %s se musí znovu přihlásit, aby mohl dál přijímat a odesílat poštu",
		NoEventTitle: "(Událost bez názvu)", DateLayout: "2. 1.", ReminderAllDay: "%s, celý den", ReminderToday: "Dnes %s",
		TrayOpen: "Otevřít CZL Mail", TrayQuit: "Ukončit CZL Mail", TrayUnread: "CZL Mail — nepřečtené: %d",
		SaveAttachment: "Uložit přílohu", AddAttachments: "Přidat přílohy", UploadFiles: "Nahrát soubory", UploadFolder: "Nahrát složku",
		ExportEmail: "Exportovat zprávu", EmailFiles: "Zpráva (*.eml)", ImportEmail: "Importovat zprávy", EmailOrZipFiles: "Zprávy nebo archiv (*.eml, *.zip)",
		ImportCalendar: "Importovat kalendář", ImportContacts: "Importovat kontakty",
		ImportNoteVault: "Importovat trezor Markdown", ExportNoteVault: "Exportovat trezor",
		ImportTemplates: "Importovat šablony zpráv", TemplateFiles: "Soubor šablon (*.json)",
	},
}

// SetUILocale 由界面在启动与切换语言时告知当前语言; 没有对应文案的语言按默认处理。
// 语言变了时刷新已经显示出来的托盘文字, 并按新文案重新登记移动端的定时提醒。
func (a *App) SetUILocale(locale string) {
	if _, ok := uiTexts[locale]; !ok {
		locale = defaultUILocale
	}
	a.mu.Lock()
	changed := locale != a.uiLocale && !(a.uiLocale == "" && locale == defaultUILocale)
	a.uiLocale = locale
	a.mu.Unlock()
	if changed {
		a.setTrayMenuText(a.text())
		a.redrawTrayUnread()
		a.planRemindersSoon(false)
	}
}

// text 返回界面当前语言的后端文案。
func (a *App) text() uiText {
	a.mu.RLock()
	locale := a.uiLocale
	a.mu.RUnlock()
	if t, ok := uiTexts[locale]; ok {
		return t
	}
	return uiTexts[defaultUILocale]
}
