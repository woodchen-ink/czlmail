package main

import (
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

func TestMergeSettings(t *testing.T) {
	ts := func(v string, at int64) store.TimedSetting { return store.TimedSetting{Value: v, UpdatedAt: at} }
	local := map[string]store.TimedSetting{
		"aiModel":                 ts("local-newer", 200),
		"aiBaseUrl":               ts("local-older", 100),
		"aiTranslateLang":         ts("only-local", 100),
		store.SettingMCPEnabled:   ts("true", 100),
		aiSyncAPIKey:              ts("", 300), // 本地清除了 Key
		store.SettingMCPAllowSend: ts("false", 50),
	}
	remote := map[string]store.TimedSetting{
		"aiModel":                 ts("remote-older", 100),
		"aiBaseUrl":               ts("remote-newer", 200),
		"aiEnabled":               ts("true", 10),
		store.SettingMCPEnabled:   ts("true", 300), // 值相同, 只是时间较新
		aiSyncAPIKey:              ts("sk-old", 100),
		store.SettingMCPAllowSend: ts("false", 50),
		"futureSetting":           ts("x", 1),
	}

	merged, changes, remoteChanged := mergeSettings(local, remote)
	if !remoteChanged {
		t.Error("remote should be updated (aiModel newer, aiTranslateLang missing, key cleared)")
	}
	want := map[string]string{
		"aiModel": "local-newer", "aiBaseUrl": "remote-newer", "aiTranslateLang": "only-local",
		"aiEnabled": "true", aiSyncAPIKey: "", "futureSetting": "x",
	}
	for k, v := range want {
		if merged[k].Value != v {
			t.Errorf("merged[%s] = %q, want %q", k, merged[k].Value, v)
		}
	}
	if len(changes) != 2 || changes["aiBaseUrl"].Value != "remote-newer" || changes["aiEnabled"].Value != "true" {
		t.Errorf("local changes = %v", changes)
	}

	// 应用变更后再与合并结果合并, 应当稳定。
	for k, v := range changes {
		local[k] = v
	}
	if _, ch, rc := mergeSettings(local, merged); rc || len(ch) != 0 {
		t.Errorf("merge not stable: remoteChanged=%v changes=%v", rc, ch)
	}
}
