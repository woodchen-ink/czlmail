package syncer

import (
	"encoding/json"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"
)

// TestSupportsFiles 只有账号公布了 FileNode 能力才算有文件存储; 会话未取得时为否。
func TestSupportsFiles(t *testing.T) {
	session := &jmap.Session{Accounts: map[jmap.ID]jmap.Account{
		"files": {RawCapabilities: map[jmap.URI]json.RawMessage{"urn:ietf:params:jmap:filenode": json.RawMessage(`{}`)}},
		"mail":  {RawCapabilities: map[jmap.URI]json.RawMessage{"urn:ietf:params:jmap:mail": json.RawMessage(`{}`)}},
	}}
	s := &Syncer{client: &jmap.Client{Session: session}}
	if !s.SupportsFiles("files") {
		t.Error("account with FileNode reported without file storage")
	}
	if s.SupportsFiles("mail") || s.SupportsFiles("unknown") {
		t.Error("account without FileNode reported with file storage")
	}
	if (&Syncer{client: &jmap.Client{}}).SupportsFiles("files") {
		t.Error("no session, but file storage reported")
	}
}
