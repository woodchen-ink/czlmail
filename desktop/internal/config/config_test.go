package config

import "testing"

// TestBuildInstanceKeepsDoubleClickIsolated protects the daily profile when no shell environment exists.
func TestBuildInstanceKeepsDoubleClickIsolated(t *testing.T) {
	old := defaultInstance
	t.Cleanup(func() { defaultInstance = old })
	t.Setenv("CZLMAIL_INSTANCE", "")
	defaultInstance = "notes-test"
	if DirName() != "czlmail-notes-test" || InstanceID() != "net.czl.mail.notes-test" {
		t.Fatal("build default lost isolation")
	}
	t.Setenv("CZLMAIL_INSTANCE", "other-test")
	if DirName() != "czlmail-other-test" {
		t.Fatal("explicit instance override ignored")
	}
	t.Setenv("CZLMAIL_INSTANCE", "")
	defaultInstance = ""
	if DirName() != "czlmail" || InstanceID() != "net.czl.mail" {
		t.Fatal("daily profile changed")
	}
}
