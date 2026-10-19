package notes

import (
	"testing"
	"time"
)

// TestPermissionChangeDrainsCalls gives disabling an unambiguous completion boundary.
func TestPermissionChangeDrainsCalls(t *testing.T) {
	s, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Configure(Config{Enabled: true, AllowRead: true, AllowWrite: true}); err != nil {
		t.Fatal(err)
	}
	release, err := s.AcquireMCP(true)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Configure(Config{Enabled: true, AllowRead: true, AllowWrite: false}) }()
	select {
	case <-done:
		t.Fatal("permission change completed before active write drained")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if release, err = s.AcquireMCP(true); err != ErrForbidden {
		if release != nil {
			release()
		}
		t.Fatal("write accepted after permission was disabled", err)
	}
}
