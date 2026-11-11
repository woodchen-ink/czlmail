package notes

import (
	"sort"
	"testing"
)

// childNames lists the live names under parent (root when parent is "").
func childNames(t *testing.T, s *Service, vault, parent string) map[string]Entry {
	t.Helper()
	list, err := s.List(vault, parent, "", false)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Entry{}
	for _, e := range list {
		full, err := s.Read(vault, e.ID)
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name] = full
	}
	return out
}

func keys(m map[string]Entry) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestBothCreatedSamePath: a new device creates paths that already exist remotely before its first sync
// (today's journal and its folders, a note, an attachment). Sync must not stall, and nothing written by
// the user may be lost.
func TestBothCreatedSamePath(t *testing.T) {
	r := newRemote()
	a := testService(t, r)
	av := vaultID(t, a)
	diary, _, err := a.EnsureNote(av, []string{"journal", "11"}, "today", "# template")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Update(Edit{VaultID: av, ID: diary.ID, ExpectedRevision: diary.Revision, Content: "A diary"}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Create(Edit{VaultID: av, Name: "same", Content: "A text"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Import(av, []ImportedFile{{Path: "pic.png", Data: []byte("A image")}}); err != nil {
		t.Fatal(err)
	}
	mustSync(t, a)

	b := testService(t, r)
	bv := vaultID(t, b)
	if _, _, err = b.EnsureNote(bv, []string{"journal", "11"}, "today", "# template"); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Create(Edit{VaultID: bv, Name: "same", Content: "B text"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Import(bv, []ImportedFile{{Path: "pic.png", Data: []byte("B image")}}); err != nil {
		t.Fatal(err)
	}
	mustSync(t, b) // fails with ErrUncertain when the collision blocks sync

	root := childNames(t, b, bv, "")
	if got := keys(root); len(got) != 4 || got[0] != "journal" || got[1] != "pic 2.png" || got[2] != "pic.png" || got[3] != "same.md" {
		t.Fatalf("root after merge: %v", got)
	}
	// Folders merge; the untouched template yields to the remote diary.
	months := childNames(t, b, bv, root["journal"].ID)
	days := childNames(t, b, bv, months["11"].ID)
	if len(months) != 1 || len(days) != 1 {
		t.Fatalf("journal tree: %v %v", keys(months), keys(days))
	}
	if d := days["today.md"]; d.Status != "synced" || d.Content != "A diary" {
		t.Fatalf("diary: %+v", d)
	}
	// Different user text on both sides is a conflict with both versions readable.
	if n := root["same.md"]; n.Status != "conflict" || n.Content != "B text" || n.RemoteContent != "A text" {
		t.Fatalf("same.md: %+v", n)
	}

	// The renamed local attachment reaches the other device.
	mustSync(t, a)
	if got := keys(childNames(t, a, av, "")); len(got) != 4 || got[1] != "pic 2.png" {
		t.Fatalf("A root: %v", got)
	}
}

// TestEditedJournalCollision: a journal the user already wrote in on the new device is never replaced.
func TestEditedJournalCollision(t *testing.T) {
	r := newRemote()
	a := testService(t, r)
	av := vaultID(t, a)
	if _, _, err := a.EnsureNote(av, nil, "today", "# template"); err != nil {
		t.Fatal(err)
	}
	mustSync(t, a)

	b := testService(t, r)
	bv := vaultID(t, b)
	local, _, err := b.EnsureNote(bv, nil, "today", "# template")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Update(Edit{VaultID: bv, ID: local.ID, ExpectedRevision: local.Revision, Content: "# template\nB wrote this"}); err != nil {
		t.Fatal(err)
	}
	mustSync(t, b)
	n := childNames(t, b, bv, "")["today.md"]
	if n.Status != "conflict" || n.Content != "# template\nB wrote this" || n.RemoteContent != "# template" {
		t.Fatalf("edited journal: %+v", n)
	}
}
