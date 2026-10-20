package notes

import (
	"errors"
	"testing"
)

// createMoveEntry fails immediately so a later move test never depends on a missing fixture.
func createMoveEntry(t *testing.T, s *Service, vault, parent, name, content string, directory bool) Entry {
	t.Helper()
	e, err := s.Create(Edit{VaultID: vault, ParentID: parent, Name: name, Content: content}, directory)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// TestMoveNonEmptyDirectory keeps descendant IDs and parent chains through synchronization.
func TestMoveNonEmptyDirectory(t *testing.T) {
	r := newRemote()
	s := testService(t, r)
	v := vaultID(t, s)
	a := createMoveEntry(t, s, v, "", "A", "", true)
	b := createMoveEntry(t, s, v, "", "B", "", true)
	child := createMoveEntry(t, s, v, a.ID, "child", "", true)
	n := createMoveEntry(t, s, v, child.ID, "plain", "plain text", false)
	mustSync(t, s)
	a, _ = s.Read(v, a.ID)
	moved, err := s.Move(Edit{VaultID: v, ID: a.ID, ExpectedRevision: a.Revision, ParentID: b.ID, Name: a.Name})
	if err != nil {
		t.Fatal(err)
	}
	if moved.ParentID != b.ID {
		t.Fatal("root did not move")
	}
	got, _ := s.Read(v, n.ID)
	if got.ParentID != child.ID || got.Content != n.Content {
		t.Fatal("descendant identity or content changed")
	}
	mustSync(t, s)
	peer := testService(t, r)
	mustSync(t, peer)
	pv := vaultID(t, peer)
	roots, _ := peer.List(pv, "", "", false)
	if len(roots) != 1 || roots[0].Name != "B" {
		t.Fatalf("unexpected peer roots: %+v", roots)
	}
	children, _ := peer.List(pv, roots[0].ID, "", false)
	if len(children) != 1 || children[0].Name != "A" {
		t.Fatalf("missing moved subtree: %+v", children)
	}
	moved, _ = s.Read(v, moved.ID)
	if _, err = s.Move(Edit{VaultID: v, ID: moved.ID, ExpectedRevision: moved.Revision, ParentID: "", Name: moved.Name}); err != nil {
		t.Fatalf("move back to root: %v", err)
	}
}

// TestMovePreservesReferenceGuard rejects potential link breakage without changing metadata.
func TestMovePreservesReferenceGuard(t *testing.T) {
	for _, content := range []string{"![image](assets/image.png)", "![[image.png]]", "[image]: assets/image.png"} {
		t.Run(content, func(t *testing.T) {
			s := testService(t, newRemote())
			v := vaultID(t, s)
			a := createMoveEntry(t, s, v, "", "A", "", true)
			b := createMoveEntry(t, s, v, "", "B", "", true)
			createMoveEntry(t, s, v, a.ID, "linked", content, false)
			_, err := s.Move(Edit{VaultID: v, ID: a.ID, ExpectedRevision: a.Revision, ParentID: b.ID, Name: a.Name})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("expected reference protection, got %v", err)
			}
			got, _ := s.Read(v, a.ID)
			if got.ParentID != a.ParentID || got.Revision != a.Revision {
				t.Fatal("rejected move changed metadata")
			}
		})
	}
}

// TestMoveIncomingEncodedReference protects links even when a filename is percent encoded.
func TestMoveIncomingEncodedReference(t *testing.T) {
	s := testService(t, newRemote())
	v := vaultID(t, s)
	a := createMoveEntry(t, s, v, "", "A", "", true)
	b := createMoveEntry(t, s, v, "", "B", "", true)
	createMoveEntry(t, s, v, a.ID, "space name", "plain", false)
	createMoveEntry(t, s, v, "", "incoming", "[open](space%20name.md)", false)
	_, err := s.Move(Edit{VaultID: v, ID: a.ID, ExpectedRevision: a.Revision, ParentID: b.ID, Name: a.Name})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected reference protection, got %v", err)
	}
}

// TestMoveSelfReferenceCannotBeRenamed protects ordinary and bare wiki self-links.
func TestMoveSelfReferenceCannotBeRenamed(t *testing.T) {
	for _, content := range []string{"[self](A.md)", "[[A]]", "[self]: A.md"} {
		t.Run(content, func(t *testing.T) {
			s := testService(t, newRemote())
			v := vaultID(t, s)
			n := createMoveEntry(t, s, v, "", "A", content, false)
			_, err := s.Move(Edit{VaultID: v, ID: n.ID, ExpectedRevision: n.Revision, Name: "B.md"})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("self-reference rename accepted: %v", err)
			}
		})
	}
}

// TestMoveRejectsStaleCyclesAndDeletedAncestors covers server-side boundaries, not only tree checks.
func TestMoveRejectsStaleCyclesAndDeletedAncestors(t *testing.T) {
	s := testService(t, newRemote())
	v := vaultID(t, s)
	a := createMoveEntry(t, s, v, "", "A", "", true)
	child := createMoveEntry(t, s, v, a.ID, "child", "", true)
	_, err := s.Move(Edit{VaultID: v, ID: a.ID, ExpectedRevision: a.Revision - 1, Name: a.Name})
	if !errors.Is(err, ErrRevision) {
		t.Fatalf("stale revision accepted: %v", err)
	}
	_, err = s.Move(Edit{VaultID: v, ID: a.ID, ExpectedRevision: a.Revision, ParentID: child.ID, Name: a.Name})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("cycle accepted: %v", err)
	}
	if _, err = s.Trash(v, a.ID, a.Revision, false); err != nil {
		t.Fatal(err)
	}
	_, err = s.Move(Edit{VaultID: v, ID: child.ID, ExpectedRevision: child.Revision, Name: child.Name})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted ancestor accepted: %v", err)
	}
}
