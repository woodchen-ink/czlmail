package notes

import "testing"

// TestDirectoryTrashKeepsSubtree proves one marker hides and restores all descendants without deleting bytes.
func TestDirectoryTrashKeepsSubtree(t *testing.T) {
	r := newRemote()
	s := testService(t, r)
	v := vaultID(t, s)
	folder, err := s.Create(Edit{VaultID: v, Name: "Parent"}, true)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := s.Create(Edit{VaultID: v, ParentID: folder.ID, Name: "Child"}, true)
	if err != nil {
		t.Fatal(err)
	}
	note, err := s.Create(Edit{VaultID: v, ParentID: nested.ID, Name: "Original", Content: "do not lose"}, false)
	if err != nil {
		t.Fatal(err)
	}
	mustSync(t, s)
	folder, _ = s.Read(v, folder.ID)
	folder, err = s.Trash(v, folder.ID, folder.Revision, false)
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.List(v, "", "lose", false)
	if err != nil || len(items) != 0 {
		t.Fatal("trashed subtree visible", items, err)
	}
	child, err := s.Read(v, note.ID)
	if err != nil || !child.Deleted || child.Content != "do not lose" {
		t.Fatal("subtree content lost", err)
	}
	if _, err = s.Update(Edit{VaultID: v, ID: child.ID, ExpectedRevision: child.Revision, Content: "bad"}); err != ErrInvalid {
		t.Fatal("trashed child edited", err)
	}
	mustSync(t, s)
	other := testService(t, r)
	otherVault := vaultID(t, other)
	mustSync(t, other)
	visible, _ := other.List(otherVault, "", "lose", false)
	if len(visible) != 0 {
		t.Fatal("other device exposes deleted subtree")
	}
	folder, _ = s.Read(v, folder.ID)
	folder, err = s.Trash(v, folder.ID, folder.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	mustSync(t, s)
	mustSync(t, other)
	child, _ = s.Read(v, note.ID)
	if child.Deleted || child.Content != "do not lose" {
		t.Fatal("restore lost subtree")
	}
	visible, _ = other.List(otherVault, "", "lose", false)
	if len(visible) != 1 {
		t.Fatal("other device failed to restore", visible)
	}
}
