package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/auth"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 日历、通讯录、文件的写操作端到端: 在真实服务器上建、改、删, 全部用测试专用的名字并清理。
func TestIntegrationPIMWrites(t *testing.T) {
	host, user, pass := os.Getenv("CZLMAIL_TEST_HOST"), os.Getenv("CZLMAIL_TEST_USER"), os.Getenv("CZLMAIL_TEST_PASS")
	if host == "" || user == "" || pass == "" {
		t.Skip("set CZLMAIL_TEST_HOST / _USER / _PASS to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	endpoint, err := auth.SessionEndpoint(host)
	if err != nil {
		t.Fatal(err)
	}
	client := &jmap.Client{SessionEndpoint: endpoint, HttpClient: basicAuthClient(user, pass)}
	if err := client.Authenticate(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "it.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := &App{ctx: ctx, log: log, store: st, client: client, sync: syncer.New(client, st, log)}
	acc := string(client.Session.PrimaryAccounts[jmap.URI("urn:ietf:params:jmap:calendars")])
	if err := a.sync.SyncAccount(ctx, acc); err != nil {
		t.Fatal(err)
	}

	t.Run("calendar", func(t *testing.T) {
		cals, _ := a.ListCalendars()
		calID := ""
		for _, c := range cals {
			if c.AccountID == acc && c.IsDefault {
				calID = c.ID
			}
		}
		if calID == "" {
			t.Skip("no default calendar")
		}
		id, err := a.SaveEvent(EventInput{
			AccountID: acc, CalendarID: calID, Title: "czlmail-test 周会", Location: "会议室",
			Start: "2031-03-03T10:00:00", End: "2031-03-03T11:00:00", TimeZone: "Asia/Shanghai",
			Recurrence:   &Recurrence{Frequency: "weekly", ByDay: []string{"mo"}, Count: 4},
			AlertMinutes: []int{15},
		})
		if err != nil || id == "" {
			t.Fatalf("create: %q %v", id, err)
		}
		defer a.DeleteEvent(acc, id, "", "all")

		occ, err := a.ListEvents("2031-03-01T00:00:00+08:00", "2031-04-01T00:00:00+08:00")
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, o := range occ {
			if o.EventID == id {
				n++
			}
		}
		if n != 4 {
			t.Fatalf("expected 4 occurrences, got %d", n)
		}

		if err := a.DeleteEvent(acc, id, "2031-03-10T10:00:00", "this"); err != nil {
			t.Fatalf("delete this: %v", err)
		}
		if _, err := a.SaveEvent(EventInput{
			AccountID: acc, ID: id, Title: "czlmail-test 改期", Start: "2031-03-18T15:00:00", End: "2031-03-18T16:00:00",
			TimeZone: "Asia/Shanghai", Scope: "this", RecurrenceID: "2031-03-17T10:00:00",
		}); err != nil {
			t.Fatalf("edit this: %v", err)
		}
		occ, _ = a.ListEvents("2031-03-01T00:00:00+08:00", "2031-04-01T00:00:00+08:00")
		var titles []string
		for _, o := range occ {
			if o.EventID == id {
				titles = append(titles, o.Start.Format("01-02 15:04")+" "+o.Title)
			}
		}
		t.Logf("occurrences: %v", titles)
		if len(titles) != 3 || !strings.Contains(strings.Join(titles, ","), "03-18 15:00 czlmail-test 改期") {
			t.Fatalf("unexpected occurrences %v", titles)
		}

		d, err := a.GetEvent(acc, id)
		if err != nil || d.Location != "会议室" || len(d.AlertMinutes) != 1 || d.AlertMinutes[0] != 15 || d.Recurrence == nil {
			t.Fatalf("detail: %+v %v", d, err)
		}
	})

	t.Run("calendar-extras", func(t *testing.T) {
		calID, err := a.SaveCalendar(acc, "", "czlmail-test 日历", "#5E7E80")
		if err != nil || calID == "" {
			t.Fatalf("create calendar: %q %v", calID, err)
		}
		defer a.DeleteCalendar(acc, calID)

		taskID, err := a.SaveTask(TaskInput{AccountID: acc, CalendarID: calID, Title: "czlmail-test 任务", Due: "2031-04-01T18:00:00", Priority: 1})
		if err != nil || taskID == "" {
			t.Fatalf("create task: %q %v", taskID, err)
		}
		if err := a.SetTaskCompleted(acc, taskID, true); err != nil {
			t.Fatalf("complete task: %v", err)
		}
		tasks, _ := a.ListTasks()
		found := false
		for _, tk := range tasks {
			if tk.ID == taskID {
				found = true
				if !tk.Completed || tk.Due != "2031-04-01T18:00:00" {
					t.Errorf("task = %+v", tk)
				}
			}
		}
		if !found {
			t.Fatal("task not listed")
		}
		occ, _ := a.ListEvents("2031-03-25T00:00:00+08:00", "2031-04-05T00:00:00+08:00")
		for _, o := range occ {
			if o.EventID == taskID {
				t.Error("task must not appear as an event")
			}
		}

		evID, err := a.SaveEvent(EventInput{AccountID: acc, CalendarID: calID, Title: "czlmail-test 线上会",
			Start: "2031-04-02T10:00:00", End: "2031-04-02T11:00:00", VirtualLocation: "https://meet.example.com/abc",
			AlertMinutes: []int{10, 60}})
		if err != nil {
			t.Fatalf("create event: %v", err)
		}
		d, err := a.GetEvent(acc, evID)
		if err != nil || d.VirtualLocation != "https://meet.example.com/abc" || len(d.AlertMinutes) != 2 || !d.IsOrganizer {
			t.Fatalf("detail: %+v %v", d, err)
		}

		// 参加者结构: 用 sendSchedulingMessages=false 提交, 只验证服务器接受, 不真的发邀请。
		parts, org, err := a.buildParticipants(acc, []Attendee{{Name: "测试", Email: "czlmail-test@example.com"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.sync.SetPIM(ctx, acc, "CalendarEvent", nil,
			map[string]map[string]any{evID: {"participants": parts, "organizerCalendarAddress": org}}, nil,
			map[string]any{"sendSchedulingMessages": false}); err != nil {
			t.Fatalf("participants rejected: %v", err)
		}
		d, _ = a.GetEvent(acc, evID)
		if len(d.Participants) != 2 || !d.IsOrganizer {
			t.Fatalf("participants: %+v", d)
		}
	})

	t.Run("contacts", func(t *testing.T) {
		books, _ := a.ListAddressBooks()
		bookID := ""
		for _, b := range books {
			if b.AccountID == acc && b.IsDefault {
				bookID = b.ID
			}
		}
		if bookID == "" {
			t.Skip("no default address book")
		}
		id, err := a.SaveContact(ContactForm{
			AccountID: acc, AddressBookID: bookID, Surname: "czlmail-test 张", Given: "三", Organization: "测试公司",
			Department: "研发部", Emails: []ContactField{{Value: "czlmail-test@example.com", Context: "work"}},
			Phones:        []ContactField{{Value: "+86 138 0000 0000", Feature: "mobile"}},
			Addresses:     []ContactAddress{{Street: "人民路 1 号", Locality: "上海", Country: "中国", Context: "work"}},
			Anniversaries: []ContactDate{{Kind: "birth", Date: "--05-20"}},
			Keywords:      []string{"客户"},
			Photo:         "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
		})
		if err != nil || id == "" {
			t.Fatalf("create: %q %v", id, err)
		}
		defer a.DeleteContacts(acc, []string{id})

		d, err := a.GetContact(acc, id)
		if err != nil || d.DisplayName != "czlmail-test 张三" || len(d.Emails) != 1 || d.Department != "研发部" ||
			len(d.Addresses) != 1 || d.Addresses[0].Locality != "上海" || len(d.Anniversaries) != 1 || d.Anniversaries[0].Date != "--05-20" ||
			d.PhotoURL == "" || len(d.Keywords) != 1 || d.Phones[0].Feature != "mobile" {
			t.Fatalf("detail: %+v %v", d, err)
		}
		d.Emails = append(d.Emails, ContactField{Value: "second@example.com"})
		d.Organization, d.JobTitle = "", "工程师"
		d.PhotoChanged, d.Photo = true, ""
		if _, err := a.SaveContact(*d); err != nil {
			t.Fatalf("update: %v", err)
		}
		d, _ = a.GetContact(acc, id)
		if len(d.Emails) != 2 || d.Organization != "" || d.JobTitle != "工程师" || d.PhotoURL != "" {
			t.Fatalf("after update: %+v", d)
		}

		gid, err := a.SaveContact(ContactForm{AccountID: acc, AddressBookID: bookID, Kind: "group", Given: "czlmail-test 群组", MemberIDs: []string{id}})
		if err != nil {
			t.Fatalf("create group: %v", err)
		}
		defer a.DeleteContacts(acc, []string{gid})
		g, _ := a.GetContact(acc, gid)
		if g.Kind != "group" || len(g.MemberIDs) != 1 || g.MemberIDs[0] != id {
			t.Fatalf("group: %+v", g)
		}
		if r, _ := a.SearchRecipients("czlmail-test"); len(r) == 0 {
			t.Error("recipient search should find the new contact")
		}
	})

	t.Run("files", func(t *testing.T) {
		name := "czlmail-test-" + time.Now().Format("150405")
		if err := a.CreateFolder(acc, "", name); err != nil {
			t.Fatalf("create folder: %v", err)
		}
		n, err := st.FileNodeByName(ctx, acc, "", name)
		if err != nil || !n.IsDirectory {
			t.Fatalf("folder not synced: %+v %v", n, err)
		}
		defer a.DeleteFiles(acc, []string{n.ID})
		if err := a.CreateFolder(acc, n.ID, "child"); err != nil {
			t.Fatalf("create child: %v", err)
		}
		if err := a.RenameFile(acc, n.ID, name+"-renamed"); err != nil {
			t.Fatalf("rename: %v", err)
		}
		kids, _ := a.ListFiles(acc, n.ID)
		path, _ := a.FilePath(acc, kids[0].ID)
		if len(kids) != 1 || len(path) != 2 || path[0].Name != name+"-renamed" {
			t.Fatalf("children %+v path %+v", kids, path)
		}

		// 复制、副本、新建文本文件、上传本地目录。
		if err := a.CreateTextFile(acc, n.ID, "note"); err != nil {
			t.Fatalf("create text file: %v", err)
		}
		if err := a.DuplicateFile(acc, kids[0].ID); err != nil {
			t.Fatalf("duplicate: %v", err)
		}
		local := t.TempDir()
		_ = os.MkdirAll(filepath.Join(local, "sub"), 0o755)
		_ = os.WriteFile(filepath.Join(local, "sub", "a.txt"), []byte("hello"), 0o644)
		if cnt, err := a.UploadPaths(acc, n.ID, []string{filepath.Join(local, "sub")}); err != nil || cnt != 1 {
			t.Fatalf("upload paths: %d %v", cnt, err)
		}
		if err := a.CopyFiles(acc, []string{n.ID}, n.ID); err == nil {
			t.Error("copying a folder into itself must fail")
		}
		kids, _ = a.ListFiles(acc, n.ID)
		var names []string
		for _, k := range kids {
			names = append(names, k.Name)
		}
		t.Logf("children: %v", names)
		if len(kids) != 4 {
			t.Fatalf("expected child, child (2), note.txt, sub; got %v", names)
		}
		if err := a.SetFileFavorite(acc, n.ID, true); err != nil {
			t.Fatal(err)
		}
		if favs, _ := a.FileFavorites(); len(favs) != 1 {
			t.Errorf("favorites = %d", len(favs))
		}
	})
}
