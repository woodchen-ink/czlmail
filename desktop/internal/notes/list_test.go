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

// TestSearchSnippetAndUniqueName covers search context, folder paths and automatic naming.
func TestSearchSnippetAndUniqueName(t *testing.T) {
	s := testService(t, newRemote())
	vault := vaultID(t, s)
	folder, err := s.Create(Edit{VaultID: vault, Name: "工作"}, true)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Create(Edit{VaultID: vault, ParentID: folder.ID, Name: "未命名", Unique: true, Content: "开头\n\n这里记录 HMRC 参考税号 1234"}, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(Edit{VaultID: vault, ParentID: folder.ID, Name: "未命名", Unique: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "未命名.md" || second.Name != "未命名 2.md" {
		t.Fatalf("names: %q %q", first.Name, second.Name)
	}
	if _, err = s.Create(Edit{VaultID: vault, ParentID: folder.ID, Name: "未命名"}, false); err == nil {
		t.Fatal("non-unique create must still reject a taken name")
	}
	matches, err := s.List(vault, "", "hmrc", false)
	if err != nil || len(matches) != 1 {
		t.Fatalf("search: %v %d", err, len(matches))
	}
	if matches[0].Path != "工作" || matches[0].Snippet != "开头 这里记录 HMRC 参考税号 1234" {
		t.Fatalf("path %q snippet %q", matches[0].Path, matches[0].Snippet)
	}
	read, err := s.Read(vault, first.ID)
	if err != nil || read.Path != "工作" {
		t.Fatalf("read path: %v %q", err, read.Path)
	}
}
