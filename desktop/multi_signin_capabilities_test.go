package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/capability"
	"github.com/woodchen-ink/czlmail/desktop/internal/jmaptest"
	"github.com/woodchen-ink/czlmail/desktop/internal/pim"
)

// 第 4 步 (#15) 的场景测试: 主登录是公布全部能力的服务器 (相当于 Stalwart), 附加登录只有收发邮件
// (相当于经 JMAP 代理接入的 Gmail: 没有日历、通讯录、文件、Sieve、自动回复)。

// unsupportedPrefixes 是只有邮件能力的服务器上不该出现的方法。
var unsupportedPrefixes = []string{
	"Calendar/", "CalendarEvent/", "AddressBook/", "ContactCard/", "FileNode/", "SieveScript/", "VacationResponse/", "Principal/",
}

func unsupportedCalls(srv *jmaptest.Server) []string {
	var out []string
	for _, c := range srv.Calls() {
		for _, p := range unsupportedPrefixes {
			if strings.HasPrefix(c.Method, p) {
				out = append(out, c.Method)
			}
		}
	}
	return out
}

func hasCall(srv *jmaptest.Server, prefix string) bool {
	for _, c := range srv.Calls() {
		if strings.HasPrefix(c.Method, prefix) {
			return true
		}
	}
	return false
}

func is2280(err error) bool {
	var u *capability.UnsupportedError
	return errors.As(err, &u)
}

// 同步与界面 API: 只有邮件的登录不收到任何不支持的请求, 界面只拿到支持的账号, 直接调用与 MCP 返回 2280。
func TestMultiSignInMailOnlyServer(t *testing.T) {
	ts := newTwoServers(t)
	ts.oneA.SetCaps(jmaptest.AllCaps...)
	ta := newTestApp(t)
	s2 := ts.signIn(ta)
	local2 := s2 + "~a"
	// 主登录的日历/通讯录/网盘同步发生在邮件之后; 等它跑过, 说明 PIM 同步这一轮确实执行了。
	waitFor(t, "PIM sync on the full server", func() bool { return hasCall(ts.one, "CalendarEvent/") && hasCall(ts.one, "FileNode/") })
	// 再手动同步一轮附加登录 (含重读会话), 期间也不能出现不支持的请求。
	if err := ta.a.SyncNow(local2); err != nil {
		t.Fatal(err)
	}

	// 界面 API: 每个账号带自己服务器的功能。
	accs, err := ta.a.ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	for _, acc := range accs {
		switch acc.ID {
		case "a":
			if !acc.Features.Calendars || !acc.Features.Files || !acc.Features.Sieve || !acc.Features.Vacation || acc.Features.Directory {
				t.Errorf("full account features = %+v", acc.Features)
			}
		case local2:
			if acc.Features != (capability.Features{Submission: true}) {
				t.Errorf("mail-only account features = %+v", acc.Features)
			}
		}
	}
	cals, err := ta.a.ListCalendars()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cals {
		if c.AccountID == local2 {
			t.Errorf("calendar listed for the mail-only account: %+v", c)
		}
	}

	// 直接拼请求的功能与 MCP: 2280, 而且不发请求。
	ts.two.ResetCalls()
	if _, err := ta.a.GetFilters(local2); !is2280(err) {
		t.Errorf("GetFilters = %v, want 2280", err)
	}
	if _, err := ta.a.GetVacation(local2); !is2280(err) {
		t.Errorf("GetVacation = %v, want 2280", err)
	}
	if err := ta.a.SaveVacation(local2, Vacation{Enabled: true, TextBody: "away"}); !is2280(err) {
		t.Errorf("SaveVacation = %v, want 2280", err)
	}
	if _, _, err := ta.a.mcpCalendar(local2, ""); !is2280(err) {
		t.Errorf("MCP calendar = %v, want 2280", err)
	}
	if err := ta.a.requireFiles(local2); !is2280(err) {
		t.Errorf("MCP files = %v, want 2280", err)
	}
	if _, err := ta.a.SaveEvent(EventInput{AccountID: local2, CalendarID: "x", Title: "t", Start: "2026-12-01T10:00:00", End: "2026-12-01T11:00:00"}); !is2280(err) {
		t.Errorf("SaveEvent = %v, want 2280", err)
	}
	// 主登录的服务器没有成员目录 (AllCaps 不含 principals): 补全时不查。
	_, _ = ta.a.SearchDirectory("ali")
	if got := unsupportedCalls(ts.two); len(got) > 0 {
		t.Errorf("unsupported calls sent to the mail-only server: %v", got)
	}
	if hasCall(ts.one, "Principal/") {
		t.Error("directory lookup sent to a server without principals")
	}

	// 全程 (登录、引导、手动同步) 只有邮件的服务器上一个不支持的请求都没有。
	ts.two.ResetCalls()
	if err := ta.a.SyncNow(local2); err != nil {
		t.Fatal(err)
	}
	if !hasCall(ts.two, "Email/") {
		t.Fatal("manual sync of the mail-only sign-in sent no mail requests")
	}
	if got := unsupportedCalls(ts.two); len(got) > 0 {
		t.Errorf("unsupported calls during sync: %v", got)
	}
}

// 能力消失与恢复: 服务器不再公布日历/通讯录后, 那个账号的日程、提醒、联系人、收件人补全都清掉;
// 能力恢复后重新引导回来 (服务器上的数据还在)。
func TestMultiSignInCapabilityLostAndRestored(t *testing.T) {
	ts := newTwoServers(t)
	ts.oneA.SetCaps(jmaptest.AllCaps...)
	ta := newTestApp(t)
	ts.signIn(ta)
	waitFor(t, "PIM sync on the full server", func() bool { return hasCall(ts.one, "ContactCard/") })

	cal, err := ta.a.SaveCalendar("a", "", "Work", "")
	if err != nil {
		t.Fatal(err)
	}
	soon := time.Now().Add(time.Hour).Truncate(time.Minute)
	if _, err := ta.a.SaveEvent(EventInput{AccountID: "a", CalendarID: cal, Title: "standup",
		Start: soon.Format(pim.LocalDateTime), End: soon.Add(30 * time.Minute).Format(pim.LocalDateTime), AlertMinutes: []int{10}}); err != nil {
		t.Fatal(err)
	}
	book, err := ta.a.SaveAddressBook("a", "", "Friends")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ta.a.SaveContact(ContactForm{AccountID: "a", AddressBookID: book, Kind: "individual", Given: "Bob",
		Emails: []ContactField{{Value: "bob@friends.test"}}}); err != nil {
		t.Fatal(err)
	}

	from, to := time.Now().Add(-time.Hour).Format(time.RFC3339), time.Now().Add(48*time.Hour).Format(time.RFC3339)
	events := func() int { list, _ := ta.a.ListEvents(from, to); return len(list) }
	contacts := func() int { list, _ := ta.a.ListContacts("", "", ""); return len(list) }
	recipients := func() int {
		list, _ := ta.a.SearchRecipients("bob@friends")
		return len(list)
	}
	reminders := func() int { list, _ := ta.a.reminderPlan(time.Now()); return len(list) }
	waitFor(t, "event and contact cached", func() bool { return events() == 1 && contacts() == 1 })
	if recipients() == 0 || reminders() == 0 {
		t.Fatalf("before: recipients=%d reminders=%d", recipients(), reminders())
	}

	// 管理员关掉了日历与通讯录 (服务器只剩邮件)。
	ts.oneA.SetCaps(jmaptest.Mail, jmaptest.Submission)
	if err := ta.a.SyncNow("a"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stale calendar and contacts cleared", func() bool {
		return events() == 0 && contacts() == 0 && recipients() == 0 && reminders() == 0
	})
	waitFor(t, "features updated", func() bool {
		accs, _ := ta.a.ListAccounts()
		for _, acc := range accs {
			if acc.ID == "a" {
				return !acc.Features.Calendars && !acc.Features.Contacts && !acc.Features.Files
			}
		}
		return false
	})
	if ta.events.count(EventAccountsChanged) == 0 {
		t.Error("no session:accounts-changed after the capability change")
	}
	ts.one.ResetCalls()
	if err := ta.a.SyncNow("a"); err != nil {
		t.Fatal(err)
	}
	if got := unsupportedCalls(ts.one); len(got) > 0 {
		t.Errorf("calls for capabilities the server dropped: %v", got)
	}

	// 能力恢复: 下一轮全量引导, 服务器上的日程与联系人回来。
	ts.oneA.SetCaps(jmaptest.AllCaps...)
	if err := ta.a.SyncNow("a"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "calendar and contacts back after the capability returned", func() bool { return events() == 1 && contacts() == 1 })
}
