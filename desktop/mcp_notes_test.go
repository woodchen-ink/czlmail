package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/notes"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// TestNoteMCPGates exercises schema generation and stale-client permissions with no real account.
func TestNoteMCPGates(t *testing.T) {
	ctx := context.Background()
	service, err := notes.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	cfg := config.Config{SessionEndpoint: "https://invalid.example/jmap", Username: "test", AuthMethod: config.AuthPassword}
	sum := sha256.Sum256([]byte(cfg.SessionEndpoint + "\x00" + cfg.Username))
	key := hex.EncodeToString(sum[:])
	a := &App{ctx: ctx, cfg: cfg, store: &store.Store{}}
	a.notes.services = map[string]*notes.Service{key: service}
	service.Configure(notes.Config{Enabled: true, AllowRead: true, AllowWrite: true})
	ct, st := mcp.NewInMemoryTransports()
	serverSession, err := a.newMCPServer().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "notes-test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	found := map[string]bool{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		if tool.Annotations == nil {
			t.Fatal("missing annotations", tool.Name)
		}
		found[tool.Name] = true
	}
	for _, name := range []string{"list_note_vaults", "list_notes", "search_notes", "read_note", "create_note", "update_note", "create_note_folder", "move_note_item", "trash_note_item", "restore_note_item", "add_note_attachment", "read_note_attachment", "get_note_diff", "resolve_note_conflict", "import_note_files", "get_note_operation", "bind_note_vault"} {
		if !found[name] {
			t.Error("missing", name)
		}
	}
	service.Configure(notes.Config{Enabled: false, AllowRead: true, AllowWrite: true})
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_note_vaults", Arguments: map[string]any{}})
	if err == nil && !result.IsError {
		t.Fatal("stale tool bypassed disabled module")
	}
	service.Configure(notes.Config{Enabled: true, AllowRead: true, AllowWrite: false})
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "create_note", Arguments: map[string]any{"vaultId": "missing", "name": "test", "idempotencyKey": "one"}})
	if err == nil && !result.IsError {
		t.Fatal("stale tool bypassed write permission")
	}
}
