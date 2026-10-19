// Package notes owns local-first Markdown documents independently of the UI transport.
package notes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const MaxText = 4 << 20
const MaxAttachment = 25 << 20

var (
	ErrDisabled    = errors.New("2200 notes_disabled")
	ErrRevision    = errors.New("2201 note_revision_mismatch")
	ErrConflict    = errors.New("2202 note_conflict")
	ErrInvalid     = errors.New("2203 note_invalid_input")
	ErrNotFound    = errors.New("2204 note_not_found")
	ErrUnavailable = errors.New("2205 notes_unavailable")
	ErrStorage     = errors.New("2206 note_storage_failed")
	ErrForbidden   = errors.New("2207 note_write_forbidden")
	ErrUncertain   = errors.New("2208 note_remote_result_uncertain")
)

// Config is device-local; enabling notes never enables the MCP server.
type Config struct {
	Enabled    bool `json:"enabled"`
	AllowRead  bool `json:"allowRead"`
	AllowWrite bool `json:"allowWrite"`
}

// Vault binds one personal account to an existing remote directory.
type Vault struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	RootID    string `json:"rootId"`
	Name      string `json:"name"`
	Error     string `json:"error"`
	SyncedAt  int64  `json:"syncedAt"`
}

// Entry is the durable local overlay. Base tracks remote metadata at the last reconciliation.
type Entry struct {
	ID             string `json:"id"`
	VaultID        string `json:"vaultId"`
	ParentID       string `json:"parentId"`
	Name           string `json:"name"`
	Directory      bool   `json:"directory"`
	Attachment     bool   `json:"attachment"`
	Content        string `json:"content,omitempty"`
	BlobPath       string `json:"-"`
	BlobKey        string `json:"blobKey,omitempty"`
	Type           string `json:"type"`
	Size           int64  `json:"size"`
	Revision       int64  `json:"revision"`
	Modified       int64  `json:"modified"`
	Status         string `json:"status"`
	RemoteID       string `json:"remoteId"`
	Base           *Node  `json:"base,omitempty"`
	BaseHash       string `json:"baseHash,omitempty"`
	Remote         *Node  `json:"remote,omitempty"`
	RemoteContent  string `json:"remoteContent,omitempty"`
	Deleted        bool   `json:"deleted"`
	OriginalParent string `json:"originalParent,omitempty"`
	Uncertain      bool   `json:"uncertain"`
	Intent         *Node  `json:"intent,omitempty"`
}

// Node is remote metadata; content versions are immutable blob identifiers.
type Node struct {
	ID        string `json:"id"`
	ParentID  string `json:"parentId"`
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
	BlobID    string `json:"blobId"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
}

// Snapshot is a consistent account-level FileNode state.
type Snapshot struct {
	State string
	Nodes []Node
}

// Remote must provide conditional mutations. Unconditional writes are not a fallback.
type Remote interface {
	Snapshot(context.Context, string) (Snapshot, error)
	Download(context.Context, string, string, int64) ([]byte, error)
	Upload(context.Context, string, []byte) (string, error)
	Set(context.Context, string, string, string, Node) (Node, error)
}

// Diff explicitly labels cached versus live remote comparisons.
type Diff struct {
	Local         Entry  `json:"local"`
	Remote        *Node  `json:"remote"`
	RemoteContent string `json:"remoteContent"`
	RemoteVersion string `json:"remoteVersion"`
	Stale         bool   `json:"stale"`
}

// Edit is the shared UI/MCP compare-and-swap contract.
type Edit struct {
	VaultID          string `json:"vaultId"`
	ID               string `json:"id"`
	ParentID         string `json:"parentId"`
	Name             string `json:"name"`
	Content          string `json:"content"`
	ExpectedRevision int64  `json:"expectedRevision"`
	IdempotencyKey   string `json:"idempotencyKey"`
}

// validName rejects platform-specific names before they become cross-device paths.
func validName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.TrimSpace(name) != name || strings.ContainsAny(name, "/\\\x00<>:\"|?*") || strings.HasSuffix(name, ".") {
		return false
	}
	for _, c := range name {
		if c < 32 {
			return false
		}
	}
	stem := strings.ToUpper(strings.Split(name, ".")[0])
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return false
	}
	for i := 1; i <= 9; i++ {
		if stem == fmt.Sprintf("COM%d", i) || stem == fmt.Sprintf("LPT%d", i) {
			return false
		}
	}
	return len(name) <= 240
}

// stamp timestamps local metadata; timestamps never arbitrate synchronization.
func stamp(e *Entry) { e.Modified = time.Now().UnixMilli(); e.Revision++ }
