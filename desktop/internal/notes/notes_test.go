package notes

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// fakeRemote implements real state preconditions and immutable blobs for deterministic two-device tests.
type fakeRemote struct {
	mu         sync.Mutex
	state      int
	nodes      map[string]Node
	blobs      map[string][]byte
	next       int
	onDownload func()
}

func newRemote() *fakeRemote {
	return &fakeRemote{state: 1, nodes: map[string]Node{"root": {ID: "root", Name: "Notes", Directory: true}}, blobs: map[string][]byte{}}
}
func (r *fakeRemote) Snapshot(context.Context, string) (Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := Snapshot{State: fmt.Sprint(r.state), Nodes: []Node{}}
	for _, n := range r.nodes {
		out.Nodes = append(out.Nodes, n)
	}
	return out, nil
}
func (r *fakeRemote) Upload(_ context.Context, _ string, b []byte) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := digest(string(b))
	r.blobs[id] = append([]byte{}, b...)
	return id, nil
}
func (r *fakeRemote) Download(_ context.Context, _, id string, _ int64) ([]byte, error) {
	r.mu.Lock()
	b := append([]byte{}, r.blobs[id]...)
	fn := r.onDownload
	r.onDownload = nil
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
	return b, nil
}
func (r *fakeRemote) Set(_ context.Context, _, state, id string, n Node) (Node, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state != fmt.Sprint(r.state) {
		return Node{}, ErrConflict
	}
	if id == "" {
		r.next++
		id = fmt.Sprint("node", r.next)
	}
	n.ID = id
	r.nodes[id] = n
	r.state++
	return n, nil
}

// testService binds a fresh device to an existing remote library.
func testService(t *testing.T, r *fakeRemote) *Service {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "library"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.SetRemote(r)
	if _, err = s.Bind(context.Background(), "a", "root"); err != nil {
		t.Fatal(err)
	}
	return s
}
func vaultID(t *testing.T, s *Service) string {
	t.Helper()
	v, err := s.Vaults()
	if err != nil {
		t.Fatal(err)
	}
	return v[0].ID
}
func mustSync(t *testing.T, s *Service) {
	t.Helper()
	for i := 0; i < 4; i++ {
		if err := s.Sync(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

// TestTwoDevices covers automatic clean pulls, dirty conflicts and explicit comparison.
func TestTwoDevices(t *testing.T) {
	r := newRemote()
	a := testService(t, r)
	b := testService(t, r)
	av, bv := vaultID(t, a), vaultID(t, b)
	note, err := a.Create(Edit{VaultID: av, Name: "中文", Content: "first"}, false)
	if err != nil {
		t.Fatal(err)
	}
	mustSync(t, a)
	mustSync(t, b)
	list, _ := b.List(bv, "", "", false)
	if len(list) != 1 {
		t.Fatalf("got %d notes", len(list))
	}
	bn, _ := b.Read(bv, list[0].ID)
	if bn.Content != "first" {
		t.Fatal("initial pull")
	}
	an, _ := a.Read(av, note.ID)
	an, err = a.Update(Edit{VaultID: av, ID: an.ID, ExpectedRevision: an.Revision, Content: "from A"})
	if err != nil {
		t.Fatal(err)
	}
	mustSync(t, a)
	mustSync(t, b)
	bn, _ = b.Read(bv, bn.ID)
	if bn.Content != "from A" {
		t.Fatal("clean B did not pull A")
	}
	an, _ = a.Read(av, note.ID)
	_, err = a.Update(Edit{VaultID: av, ID: an.ID, ExpectedRevision: an.Revision, Content: "A again"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Update(Edit{VaultID: bv, ID: bn.ID, ExpectedRevision: bn.Revision, Content: "B local"})
	if err != nil {
		t.Fatal(err)
	}
	mustSync(t, a)
	mustSync(t, b)
	bn, _ = b.Read(bv, bn.ID)
	if bn.Status != "conflict" || bn.Content != "B local" || bn.RemoteContent != "A again" {
		t.Fatalf("lost side: %+v", bn)
	}
	d, err := b.Compare(context.Background(), bv, bn.ID)
	if err != nil || d.Stale || d.RemoteContent != "A again" {
		t.Fatalf("diff %+v %v", d, err)
	}
}

// TestPullRace proves a download cannot overwrite input accepted after the pull started.
func TestPullRace(t *testing.T) {
	r := newRemote()
	s := testService(t, r)
	v := vaultID(t, s)
	n, _ := s.Create(Edit{VaultID: v, Name: "race", Content: "old"}, false)
	mustSync(t, s)
	n, _ = s.Read(v, n.ID)
	r.mu.Lock()
	remote := r.nodes[n.RemoteID]
	r.mu.Unlock()
	blob, _ := r.Upload(context.Background(), "a", []byte("remote"))
	remote.BlobID = blob
	snap, _ := r.Snapshot(context.Background(), "a")
	r.Set(context.Background(), "a", snap.State, remote.ID, remote)
	r.onDownload = func() {
		if _, err := s.Update(Edit{VaultID: v, ID: n.ID, ExpectedRevision: n.Revision, Content: "typing"}); err != nil {
			t.Error(err)
		}
	}
	_ = s.Sync(context.Background())
	got, _ := s.Read(v, n.ID)
	if got.Content != "typing" {
		t.Fatal("download overwrote input")
	}
}

// TestPersistenceGateAndRetry verifies durable originals and idempotent creates across restart.
func TestPersistenceGateAndRetry(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := newRemote()
	s.SetRemote(r)
	v, err := s.Bind(context.Background(), "a", "root")
	if err != nil {
		t.Fatal(err)
	}
	in := Edit{VaultID: v.ID, Name: "one", Content: "untouched\n", IdempotencyKey: "request"}
	n, err := s.Create(in, false)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Create(in, false)
	if err != nil || again.ID != n.ID {
		t.Fatal("duplicate create")
	}
	if err = s.Configure(Config{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Read(v.ID, n.ID); err != ErrDisabled {
		t.Fatal("gate bypass")
	}
	s.Close()
	s, err = Open(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.GetConfig().Enabled {
		t.Fatal("lost config")
	}
	s.Configure(Config{Enabled: true, AllowRead: true})
	got, err := s.Read(v.ID, n.ID)
	if err != nil || got.Content != in.Content {
		t.Fatal("lost offline content")
	}
	if _, err = s.Update(Edit{VaultID: v.ID, ID: n.ID, Content: "stale", ExpectedRevision: 0}); err != ErrRevision {
		t.Fatal("stale write accepted")
	}
}

// TestImportAndAttachments retains original bytes and forbids escaping paths.
func TestImportAndAttachments(t *testing.T) {
	r := newRemote()
	s := testService(t, r)
	v := vaultID(t, s)
	files := []ImportedFile{{Path: "公司/资料.md", Data: []byte("![图](assets/图.png)\n")}, {Path: "公司/assets/图.png", Data: []byte("image bytes")}}
	out, err := s.Import(v, files)
	if err != nil || out.Imported != 2 {
		t.Fatal(out, err)
	}
	out, err = s.Import(v, files)
	if err != nil || out.Skipped != 2 {
		t.Fatal(out, err)
	}
	list, _ := s.List(v, "", "资料", false)
	if len(list) != 1 {
		t.Fatal(list)
	}
	data, _, err := s.Attachment(context.Background(), v, list[0].ID, "assets/图.png")
	if err != nil || string(data) != "image bytes" {
		t.Fatal("attachment", err)
	}
	if _, err = s.Import(v, []ImportedFile{{Path: "../../secret.md", Data: []byte("bad")}}); err != ErrInvalid {
		t.Fatal("traversal")
	}
	if _, _, err = s.Attachment(context.Background(), v, list[0].ID, "../../secret"); err == nil {
		t.Fatal("attachment escape")
	}
	mustSync(t, s)
}
