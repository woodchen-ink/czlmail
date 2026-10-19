package notes

import (
	"context"
	"testing"
)

// TestListIncludesAttachments guards the tree and MCP listing against hiding synced binary files.
func TestListIncludesAttachments(t *testing.T) {
	r := newRemote()
	s := testService(t, r)
	vault := vaultID(t, s)
	_, err := s.Import(vault, []ImportedFile{
		{Path: "attachments/image.png", Data: []byte("image bytes")},
		{Path: "attachments/file.pdf", Data: []byte("%PDF-1.7\n")},
		{Path: "attachments/video.mp4", Data: []byte("video bytes")},
		{Path: "note.md", Data: []byte("# Note")},
	})
	if err != nil {
		t.Fatal(err)
	}
	mustSync(t, s)
	second := testService(t, r)
	if err = second.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	v := vaultID(t, second)
	roots, err := second.List(v, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	var parent string
	for _, e := range roots {
		if e.Name == "attachments" {
			parent = e.ID
		}
	}
	if parent == "" {
		t.Fatal("attachment directory missing")
	}
	files, err := second.List(v, parent, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("want 3 attachments, got %d", len(files))
	}
	for _, e := range files {
		if !e.Attachment {
			t.Fatalf("file misclassified: %s", e.Name)
		}
	}
	matches, err := second.List(v, "", "file.pdf", false)
	if err != nil || len(matches) != 1 {
		t.Fatalf("attachment search: %v, %d", err, len(matches))
	}
}
