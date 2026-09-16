package main

import (
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

func TestMergeTemplates(t *testing.T) {
	now := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	iso := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	local := []store.SyncedTemplate{
		{Template: store.Template{ID: "a", Name: "本地较新", UpdatedAt: now.Add(-time.Hour).Unix()}},
		{Template: store.Template{ID: "b", Name: "本地较旧", UpdatedAt: now.Add(-3 * time.Hour).Unix()}},
		{Template: store.Template{ID: "c", Name: "只在本地", UpdatedAt: now.Unix()}},
		{Template: store.Template{ID: "d", Name: "本地已删", UpdatedAt: now.Add(-time.Hour).Unix()}, DeletedAt: now.Add(-time.Hour).Unix()},
	}
	remote := []templateEntry{
		{ID: "a", Name: "远端较旧", UpdatedAt: iso(-2 * time.Hour)},
		{ID: "b", Name: "远端较新", UpdatedAt: iso(-2 * time.Hour)},
		{ID: "d", Name: "远端未删", UpdatedAt: iso(-2 * time.Hour)},
		{ID: "e", Name: "只在远端", UpdatedAt: iso(-5 * time.Hour)},
	}

	merged, changes, remoteChanged := mergeTemplates(local, remote, now)
	if !remoteChanged {
		t.Error("remote should be updated (a newer locally, c missing, d deleted)")
	}
	names := map[string]string{}
	for _, e := range merged {
		names[e.ID] = e.Name
		if e.ID == "d" && e.DeletedAt == "" {
			t.Error("tombstone for d must propagate")
		}
	}
	want := map[string]string{"a": "本地较新", "b": "远端较新", "c": "只在本地", "d": "本地已删", "e": "只在远端"}
	for id, n := range want {
		if names[id] != n {
			t.Errorf("merged[%s] = %q, want %q", id, names[id], n)
		}
	}
	changed := map[string]bool{}
	for _, c := range changes {
		changed[c.ID] = true
	}
	if !changed["b"] || !changed["e"] || changed["a"] || changed["c"] || changed["d"] {
		t.Errorf("local changes = %v", changed)
	}

	// 合并结果再作为远端合并一次, 应当稳定, 不再触发上传。
	var after []store.SyncedTemplate
	for _, e := range merged {
		after = append(after, entryToTemplate(e))
	}
	if _, ch, rc := mergeTemplates(after, merged, now); rc || len(ch) != 0 {
		t.Errorf("merge not idempotent: remoteChanged=%v changes=%d", rc, len(ch))
	}
}
