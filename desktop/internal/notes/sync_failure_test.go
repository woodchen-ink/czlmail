package notes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// flakyRemote wraps the deterministic fake with the failures a real server and network produce.
type flakyRemote struct {
	*fakeRemote
	rejectName   string // Set refuses this name, like a server that dislikes the object
	setErr       error  // returned by the next Set
	applyLost    bool   // with setErr: the write took effect anyway and the server renamed the blob
	uploadErr    error  // returned by every Upload while set
	downloadFail string // Download of the blob holding this text fails
	sets         map[string]int
	uploads      int
	snapshots    int
	incremental  bool // offer Changes
	history      map[string]map[string]Node
}

func (r *flakyRemote) Snapshot(ctx context.Context, a string) (Snapshot, error) {
	r.snapshots++
	snap, err := r.fakeRemote.Snapshot(ctx, a)
	r.remember(snap)
	return snap, err
}

func (r *flakyRemote) remember(snap Snapshot) {
	if r.history == nil {
		r.history = map[string]map[string]Node{}
	}
	nodes := map[string]Node{}
	for _, n := range snap.Nodes {
		nodes[n.ID] = n
	}
	r.history[snap.State] = nodes
}

func (r *flakyRemote) Upload(ctx context.Context, a string, b []byte) (string, error) {
	if r.uploadErr != nil {
		return "", r.uploadErr
	}
	r.uploads++
	return r.fakeRemote.Upload(ctx, a, b)
}

func (r *flakyRemote) Download(ctx context.Context, a, id string, limit int64) ([]byte, error) {
	b, err := r.fakeRemote.Download(ctx, a, id, limit)
	if r.downloadFail != "" && string(b) == r.downloadFail {
		return nil, ErrUnavailable
	}
	return b, err
}

func (r *flakyRemote) Set(ctx context.Context, a, state, id string, n Node) (Node, error) {
	if r.sets == nil {
		r.sets = map[string]int{}
	}
	r.sets[n.Name]++
	if r.rejectName != "" && n.Name == r.rejectName {
		return Node{}, fmt.Errorf("%w: invalidProperties: Invalid property. (type)", ErrRejected)
	}
	if r.setErr != nil {
		err := r.setErr
		r.setErr = nil
		if r.applyLost {
			if n.BlobID != "" {
				// The server stores the blob under its own identifier once it is attached.
				r.mu.Lock()
				r.blobs["kept-"+n.BlobID] = r.blobs[n.BlobID]
				r.mu.Unlock()
				n.BlobID = "kept-" + n.BlobID
			}
			r.fakeRemote.Set(ctx, a, state, id, n)
		}
		return Node{}, err
	}
	return r.fakeRemote.Set(ctx, a, state, id, n)
}

// incrementalRemote adds Changes; flakyRemote alone exercises the full-listing fallback.
type incrementalRemote struct{ *flakyRemote }

func (r incrementalRemote) Changes(ctx context.Context, a, since string) (Delta, error) {
	old, ok := r.history[since]
	if !ok {
		return Delta{}, ErrUnavailable
	}
	snap, _ := r.fakeRemote.Snapshot(ctx, a)
	r.remember(snap)
	d := Delta{State: snap.State}
	now := r.history[snap.State]
	for id, n := range now {
		if before, ok := old[id]; !ok || before != n {
			d.Changed = append(d.Changed, n)
		}
	}
	for id := range old {
		if _, ok := now[id]; !ok {
			d.Destroyed = append(d.Destroyed, id)
		}
	}
	return d, nil
}

// Inspect reads one object and the state, as the real transport does before each write.
func (r incrementalRemote) Inspect(ctx context.Context, a, id string) (Snapshot, error) {
	snap, err := r.fakeRemote.Snapshot(ctx, a)
	out := Snapshot{State: snap.State}
	for _, n := range snap.Nodes {
		if n.ID == id {
			out.Nodes = append(out.Nodes, n)
		}
	}
	return out, err
}

func flakyService(t *testing.T) (*Service, *flakyRemote, string) {
	t.Helper()
	r := &flakyRemote{fakeRemote: newRemote()}
	s := testService(t, r.fakeRemote)
	s.SetRemote(r)
	return s, r, vaultID(t, s)
}

func mustCreate(t *testing.T, s *Service, v, parent, name, content string, directory bool) Entry {
	t.Helper()
	e, err := s.Create(Edit{VaultID: v, ParentID: parent, Name: name, Content: content}, directory)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func mustRead(t *testing.T, s *Service, v, id string) Entry {
	t.Helper()
	e, err := s.Read(v, id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// rounds runs several passes the way the scheduler would, ignoring backoff deadlines.
func rounds(t *testing.T, s *Service, n int) error {
	t.Helper()
	var last error
	for i := 0; i < n; i++ {
		_, last = s.SyncRound(context.Background(), false)
	}
	return last
}

func remoteNames(r *fakeRemote) map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]int{}
	for _, n := range r.nodes {
		out[n.Name]++
	}
	return out
}

// A write the server refuses is pinned on that entry: the rest of the library still uploads, the
// entry is listed as a problem, and it is not retried until something changes.
func TestRejectedEntryDoesNotBlockOthers(t *testing.T) {
	s, r, v := flakyService(t)
	r.rejectName = "bad.md"
	bad := mustCreate(t, s, v, "", "bad", "x", false)
	good := mustCreate(t, s, v, "", "good", "y", false)
	if err := rounds(t, s, 4); err != nil {
		t.Fatalf("a rejected entry failed the whole pass: %v", err)
	}
	if got := mustRead(t, s, v, good.ID); got.Status != "synced" {
		t.Fatalf("good note blocked: %s", got.Status)
	}
	got := mustRead(t, s, v, bad.ID)
	if got.Status != "pending" || got.SyncError != FailRejected || !strings.Contains(got.SyncDetail, "invalidProperties") {
		t.Fatalf("failure not recorded: %+v", got)
	}
	if r.sets["bad.md"] != 1 {
		t.Fatalf("refused write retried %d times", r.sets["bad.md"])
	}
	problems, _ := s.Problems(v)
	if len(problems) != 1 || problems[0].ID != bad.ID || problems[0].Kind != FailRejected {
		t.Fatalf("problems: %+v", problems)
	}
	vaults, _ := s.Vaults()
	if vaults[0].Error != "" || vaults[0].Problems != 1 {
		t.Fatalf("vault: %+v", vaults[0])
	}
	// Editing the note or asking for a retry queues it again.
	r.rejectName = ""
	if err := s.Retry(v, bad.ID); err != nil {
		t.Fatal(err)
	}
	rounds(t, s, 2)
	if got = mustRead(t, s, v, bad.ID); got.Status != "synced" || got.SyncError != "" {
		t.Fatalf("retry did not recover: %+v", got)
	}
	if problems, _ = s.Problems(v); len(problems) != 0 {
		t.Fatalf("stale problems: %+v", problems)
	}
}

// A create whose response was lost and that never reached the server is simply sent again.
func TestLostCreateNotApplied(t *testing.T) {
	s, r, v := flakyService(t)
	r.setErr = ErrUncertain
	e := mustCreate(t, s, v, "", "n", "x", false)
	later := mustCreate(t, s, v, "", "later", "y", false)
	rounds(t, s, 1)
	if got := mustRead(t, s, v, later.ID); got.Status != "synced" {
		t.Fatalf("lost create blocked a later note: %s", got.Status)
	}
	s.RetryAll() // skip the backoff
	rounds(t, s, 2)
	got := mustRead(t, s, v, e.ID)
	if got.Status != "synced" || got.Uncertain || got.RemoteID == "" {
		t.Fatalf("not recovered: %+v", got)
	}
	if names := remoteNames(r.fakeRemote); names["n.md"] != 1 {
		t.Fatalf("remote copies: %v", names)
	}
}

// A create that did take effect is adopted by its text, even though the server renamed the blob,
// and text typed since then is kept and uploaded, not reported as a conflict.
func TestLostCreateApplied(t *testing.T) {
	s, r, v := flakyService(t)
	r.setErr, r.applyLost = ErrUncertain, true
	e := mustCreate(t, s, v, "", "n", "sent", false)
	rounds(t, s, 1)
	cur := mustRead(t, s, v, e.ID)
	if !cur.Uncertain {
		t.Fatal("expected an unverified create")
	}
	if _, err := s.Update(Edit{VaultID: v, ID: e.ID, ExpectedRevision: cur.Revision, Content: "typed later"}); err != nil {
		t.Fatal(err)
	}
	rounds(t, s, 3)
	got := mustRead(t, s, v, e.ID)
	if got.Status != "synced" || got.Uncertain || got.Content != "typed later" {
		t.Fatalf("not adopted: %+v", got)
	}
	if names := remoteNames(r.fakeRemote); names["n.md"] != 1 {
		t.Fatalf("duplicate created: %v", names)
	}
}

// The same for an update: this device's own lost write is not somebody else's change.
func TestLostUpdateApplied(t *testing.T) {
	s, r, v := flakyService(t)
	e := mustCreate(t, s, v, "", "n", "one", false)
	rounds(t, s, 2)
	cur := mustRead(t, s, v, e.ID)
	cur, _ = s.Update(Edit{VaultID: v, ID: e.ID, ExpectedRevision: cur.Revision, Content: "two"})
	r.setErr, r.applyLost = ErrUncertain, true
	rounds(t, s, 1)
	if _, err := s.Update(Edit{VaultID: v, ID: e.ID, ExpectedRevision: cur.Revision, Content: "three"}); err != nil {
		t.Fatal(err)
	}
	rounds(t, s, 3)
	got := mustRead(t, s, v, e.ID)
	if got.Status != "synced" || got.Content != "three" {
		t.Fatalf("own write became %s: %+v", got.Status, got)
	}
}

// An attachment that is not going to upload (deleted remotely here) holds no note back.
func TestStuckAttachmentDoesNotBlockNotes(t *testing.T) {
	s, r, v := flakyService(t)
	n := mustCreate(t, s, v, "", "n", "x", false)
	att, err := s.AddAttachment(v, n.ID, n.Revision, "a.png", []byte("img"))
	if err != nil {
		t.Fatal(err)
	}
	rounds(t, s, 4)
	a := mustRead(t, s, v, att.Entry.ID)
	r.mu.Lock()
	delete(r.nodes, a.RemoteID)
	r.state++
	r.mu.Unlock()
	rounds(t, s, 2)
	if a = mustRead(t, s, v, att.Entry.ID); a.Status != "remote_deleted" {
		t.Fatalf("attachment: %s", a.Status)
	}
	cur := mustRead(t, s, v, n.ID)
	if _, err = s.Update(Edit{VaultID: v, ID: n.ID, ExpectedRevision: cur.Revision, Content: "edited"}); err != nil {
		t.Fatal(err)
	}
	round, err := s.SyncRound(context.Background(), false)
	if err != nil || round.Pushed != 1 {
		t.Fatalf("round %+v %v", round, err)
	}
	if cur = mustRead(t, s, v, n.ID); cur.Status != "synced" {
		t.Fatalf("note still %s", cur.Status)
	}
}

// Another file changing in the account between read and write is retried inside the pass.
func TestStateRaceIsNotAConflict(t *testing.T) {
	s, r, v := flakyService(t)
	e := mustCreate(t, s, v, "", "n", "x", false)
	r.setErr = ErrStateChanged
	if err := rounds(t, s, 1); err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, s, v, e.ID)
	if got.Status != "synced" || got.SyncError != "" {
		t.Fatalf("race surfaced: %+v", got)
	}
	if vaults, _ := s.Vaults(); vaults[0].Error != "" {
		t.Fatalf("vault error %q", vaults[0].Error)
	}
}

// A temporary failure backs off, stays out of the problem list at first, and ends the pass once it
// repeats (the connection is gone) without marking the rest as failed.
func TestTemporaryFailureBacksOff(t *testing.T) {
	s, r, v := flakyService(t)
	a := mustCreate(t, s, v, "", "a", "x", false)
	b := mustCreate(t, s, v, "", "b", "y", false)
	c := mustCreate(t, s, v, "", "c", "z", false)
	r.uploadErr = ErrUnavailable
	round, err := s.SyncRound(context.Background(), false)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected the pass to stop: %v", err)
	}
	if round.RetryAt == 0 {
		t.Fatal("no retry scheduled")
	}
	failed := 0
	for _, id := range []string{a.ID, b.ID, c.ID} {
		if e := mustRead(t, s, v, id); e.SyncError == FailTemporary {
			failed++
			if e.SyncAttempts != 1 || e.SyncRetryAt == 0 {
				t.Fatalf("backoff not recorded: %+v", e)
			}
		}
	}
	if failed != 2 {
		t.Fatalf("%d entries marked, want the two that were tried", failed)
	}
	if problems, _ := s.Problems(v); len(problems) != 0 {
		t.Fatalf("a first hiccup was reported: %+v", problems)
	}
	r.uploadErr = nil
	// Backed-off entries are left alone until due; the untried one goes up.
	rounds(t, s, 1)
	synced := 0
	for _, id := range []string{a.ID, b.ID, c.ID} {
		if mustRead(t, s, v, id).Status == "synced" {
			synced++
		}
	}
	if synced != 1 {
		t.Fatalf("%d synced before the backoff ended", synced)
	}
	s.RetryAll()
	rounds(t, s, 1)
	for _, id := range []string{a.ID, b.ID, c.ID} {
		if e := mustRead(t, s, v, id); e.Status != "synced" || e.SyncError != "" {
			t.Fatalf("not recovered: %+v", e)
		}
	}
}

// Children of a folder that cannot be created wait without being reported; a pass that moves
// nothing does not ask to be run again.
func TestChildrenWaitForFailedFolder(t *testing.T) {
	s, r, v := flakyService(t)
	r.rejectName = "box"
	folder := mustCreate(t, s, v, "", "box", "", true)
	child := mustCreate(t, s, v, folder.ID, "inside", "x", false)
	other := mustCreate(t, s, v, "", "other", "y", false)
	rounds(t, s, 2)
	if got := mustRead(t, s, v, other.ID); got.Status != "synced" {
		t.Fatalf("unrelated note blocked: %s", got.Status)
	}
	if got := mustRead(t, s, v, child.ID); got.Status != "pending" || got.SyncError != "" {
		t.Fatalf("child: %+v", got)
	}
	problems, _ := s.Problems(v)
	if len(problems) != 1 || problems[0].ID != folder.ID {
		t.Fatalf("problems: %+v", problems)
	}
	round, err := s.SyncRound(context.Background(), false)
	if err != nil || round.More || round.Pushed != 0 {
		t.Fatalf("idle pass asked for more: %+v %v", round, err)
	}
}

// A note trashed before it was ever uploaded stays local; restoring it uploads it.
func TestTrashedBeforeUploadStaysLocal(t *testing.T) {
	s, r, v := flakyService(t)
	e := mustCreate(t, s, v, "", "draft", "x", false)
	if _, err := s.Trash(v, e.ID, e.Revision, false); err != nil {
		t.Fatal(err)
	}
	round, err := s.SyncRound(context.Background(), false)
	if err != nil || round.Pushed != 0 || round.More {
		t.Fatalf("round %+v %v", round, err)
	}
	if names := remoteNames(r.fakeRemote); len(names) != 1 {
		t.Fatalf("something was uploaded: %v", names)
	}
	trash, _ := s.List(v, "", "", true)
	if len(trash) != 1 {
		t.Fatalf("local trash: %+v", trash)
	}
	cur := mustRead(t, s, v, e.ID)
	if _, err = s.Trash(v, e.ID, cur.Revision, true); err != nil {
		t.Fatal(err)
	}
	rounds(t, s, 1)
	if got := mustRead(t, s, v, e.ID); got.Status != "synced" {
		t.Fatalf("restored note: %s", got.Status)
	}
}

// Renaming or trashing unchanged content does not upload the text again.
func TestRenameKeepsRemoteBlob(t *testing.T) {
	s, r, v := flakyService(t)
	e := mustCreate(t, s, v, "", "n", "body", false)
	rounds(t, s, 1)
	uploads := r.uploads
	cur := mustRead(t, s, v, e.ID)
	blob := cur.Base.BlobID
	cur, err := s.Move(Edit{VaultID: v, ID: e.ID, ExpectedRevision: cur.Revision, Name: "renamed.md"})
	if err != nil {
		t.Fatal(err)
	}
	rounds(t, s, 1)
	cur = mustRead(t, s, v, e.ID)
	if cur.Status != "synced" || r.uploads != uploads || cur.Base.BlobID != blob {
		t.Fatalf("rename re-uploaded: uploads %d->%d, %+v", uploads, r.uploads, cur.Base)
	}
	if _, err = s.Trash(v, e.ID, cur.Revision, false); err != nil {
		t.Fatal(err)
	}
	rounds(t, s, 2)
	if r.uploads != uploads {
		t.Fatal("trash re-uploaded the text")
	}
	if names := remoteNames(r.fakeRemote); names[e.ID+"--renamed.md"] != 1 {
		t.Fatalf("not in remote trash: %v", names)
	}
}

// One remote file that cannot be downloaded is reported; the others still arrive.
func TestPullFailureIsIsolated(t *testing.T) {
	r := &flakyRemote{fakeRemote: newRemote()}
	a := testService(t, r.fakeRemote)
	av := vaultID(t, a)
	mustCreate(t, a, av, "", "broken", "cannot fetch", false)
	mustCreate(t, a, av, "", "fine", "ok", false)
	mustSync(t, a)
	b := testService(t, r.fakeRemote)
	b.SetRemote(r)
	bv := vaultID(t, b)
	r.downloadFail = "cannot fetch"
	if err := rounds(t, b, 1); err != nil {
		t.Fatalf("one bad download failed the pass: %v", err)
	}
	list, _ := b.List(bv, "", "", false)
	if len(list) != 1 || list[0].Name != "fine.md" {
		t.Fatalf("pulled: %+v", list)
	}
	b.RetryAll()
	rounds(t, b, 1)
	r.downloadFail = ""
	b.RetryAll()
	rounds(t, b, 1)
	if list, _ = b.List(bv, "", "", false); len(list) != 2 {
		t.Fatalf("after recovery: %+v", list)
	}
	if problems, _ := b.Problems(bv); len(problems) != 0 {
		t.Fatalf("stale problems: %+v", problems)
	}
}

// Text typed on a revision that a pull replaced becomes a conflict holding both sides.
func TestStashTurnsStaleInputIntoConflict(t *testing.T) {
	r := newRemote()
	a := testService(t, r)
	b := testService(t, r)
	av, bv := vaultID(t, a), vaultID(t, b)
	note := mustCreate(t, a, av, "", "n", "first", false)
	mustSync(t, a)
	mustSync(t, b)
	list, _ := b.List(bv, "", "", false)
	stale := mustRead(t, b, bv, list[0].ID)
	an := mustRead(t, a, av, note.ID)
	if _, err := a.Update(Edit{VaultID: av, ID: an.ID, ExpectedRevision: an.Revision, Content: "from A"}); err != nil {
		t.Fatal(err)
	}
	mustSync(t, a)
	mustSync(t, b)
	if _, err := b.Update(Edit{VaultID: bv, ID: stale.ID, ExpectedRevision: stale.Revision, Content: "typed on B"}); err != ErrRevision {
		t.Fatalf("stale save: %v", err)
	}
	got, err := b.Stash(Edit{VaultID: bv, ID: stale.ID, Content: "typed on B"})
	if err != nil || got.Status != "conflict" || got.Content != "typed on B" || got.RemoteContent != "from A" {
		t.Fatalf("stash: %+v %v", got, err)
	}
	mustSync(t, b)
	if got = mustRead(t, b, bv, stale.ID); got.Status != "conflict" || got.Content != "typed on B" {
		t.Fatalf("sync disturbed the conflict: %+v", got)
	}
	d, err := b.Compare(context.Background(), bv, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Resolve(context.Background(), bv, stale.ID, got.Revision, d.RemoteVersion, "local", ""); err != nil {
		t.Fatal(err)
	}
	mustSync(t, b)
	mustSync(t, a)
	if an = mustRead(t, a, av, note.ID); an.Content != "typed on B" {
		t.Fatalf("resolution did not reach A: %q", an.Content)
	}
}

// With Changes available the account is listed once; later passes only ask what moved, and
// changes made on another device still arrive.
func TestIncrementalRounds(t *testing.T) {
	base := &flakyRemote{fakeRemote: newRemote()}
	a := testService(t, base.fakeRemote)
	av := vaultID(t, a)
	b := testService(t, base.fakeRemote)
	b.SetRemote(incrementalRemote{base})
	bv := vaultID(t, b)
	note := mustCreate(t, a, av, "", "n", "one", false)
	mustSync(t, a)
	rounds(t, b, 3)
	if base.snapshots != 1 {
		t.Fatalf("%d full listings, want 1", base.snapshots)
	}
	list, _ := b.List(bv, "", "", false)
	if len(list) != 1 {
		t.Fatalf("initial pull: %+v", list)
	}
	an := mustRead(t, a, av, note.ID)
	if _, err := a.Update(Edit{VaultID: av, ID: an.ID, ExpectedRevision: an.Revision, Content: "two"}); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, a, av, "", "second", "new", false)
	mustSync(t, a)
	rounds(t, b, 1)
	if got := mustRead(t, b, bv, list[0].ID); got.Content != "two" {
		t.Fatalf("update not pulled: %q", got.Content)
	}
	if list, _ = b.List(bv, "", "", false); len(list) != 2 {
		t.Fatalf("new note not pulled: %+v", list)
	}
	// Local work is uploaded and a remote deletion is noticed, still without listing everything.
	mine := mustCreate(t, b, bv, "", "mine", "b", false)
	rounds(t, b, 1)
	if got := mustRead(t, b, bv, mine.ID); got.Status != "synced" {
		t.Fatalf("upload: %s", got.Status)
	}
	an = mustRead(t, a, av, note.ID)
	base.mu.Lock()
	delete(base.nodes, an.RemoteID)
	base.state++
	base.mu.Unlock()
	rounds(t, b, 1)
	if got := mustRead(t, b, bv, list[0].ID); !got.Deleted {
		t.Fatalf("remote deletion missed: %+v", got)
	}
	if base.snapshots != 1 {
		t.Fatalf("%d full listings after incremental passes", base.snapshots)
	}
}

// Trashing through the real transport shape (Inspect + incremental view): the trash folder is
// created on demand and the entry lands in it within the same pass.
func TestTrashWithInspector(t *testing.T) {
	base := &flakyRemote{fakeRemote: newRemote()}
	s := testService(t, base.fakeRemote)
	s.SetRemote(incrementalRemote{base})
	v := vaultID(t, s)
	e := mustCreate(t, s, v, "", "gone", "x", false)
	rounds(t, s, 1)
	cur := mustRead(t, s, v, e.ID)
	if _, err := s.Trash(v, e.ID, cur.Revision, false); err != nil {
		t.Fatal(err)
	}
	if err := rounds(t, s, 1); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, s, v, e.ID); got.Status != "synced" || got.SyncError != "" {
		t.Fatalf("trash not uploaded: %+v", got)
	}
	names := remoteNames(base.fakeRemote)
	if names[".czl-trash"] != 1 || names[e.ID+"--gone.md"] != 1 {
		t.Fatalf("remote: %v", names)
	}
	rounds(t, s, 2)
	if got := mustRead(t, s, v, e.ID); !got.Deleted || got.Status != "synced" {
		t.Fatalf("later passes disturbed it: %+v", got)
	}
}

// A note trashed before upload must not swallow a live remote note of the same name: the remote
// one arrives as its own entry and the name can be used again.
func TestLocalTrashDoesNotPairWithRemote(t *testing.T) {
	r := newRemote()
	a := testService(t, r)
	b := testService(t, r)
	av, bv := vaultID(t, a), vaultID(t, b)
	mine := mustCreate(t, b, bv, "", "day", "B draft", false)
	if _, err := b.Trash(bv, mine.ID, mine.Revision, false); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, a, av, "", "day", "from A", false)
	mustSync(t, a)
	mustSync(t, b)
	list, _ := b.List(bv, "", "", false)
	if len(list) != 1 || list[0].ID == mine.ID || list[0].Status != "synced" {
		t.Fatalf("remote note hidden: %+v", list)
	}
	if got := mustRead(t, b, bv, list[0].ID); got.Content != "from A" {
		t.Fatalf("content %q", got.Content)
	}
	if got := mustRead(t, b, bv, mine.ID); got.Status == "conflict" || got.RemoteID != "" || got.Content != "B draft" {
		t.Fatalf("trashed draft was paired: %+v", got)
	}
	if problems, _ := b.Problems(bv); len(problems) != 0 {
		t.Fatalf("problems: %+v", problems)
	}
}

// A download failure for a file that is then deleted remotely is forgotten: no ghost problem and
// no retry deadline left in the past to spin the scheduler.
func TestStalePullFailureIsDropped(t *testing.T) {
	r := &flakyRemote{fakeRemote: newRemote()}
	a := testService(t, r.fakeRemote)
	av := vaultID(t, a)
	broken := mustCreate(t, a, av, "", "broken", "cannot fetch", false)
	mustSync(t, a)
	b := testService(t, r.fakeRemote)
	b.SetRemote(r)
	r.downloadFail = "cannot fetch"
	rounds(t, b, 1)
	r.mu.Lock()
	delete(r.nodes, mustRead(t, a, av, broken.ID).RemoteID)
	r.state++
	r.mu.Unlock()
	round, err := b.SyncRound(context.Background(), false)
	if err != nil || round.RetryAt != 0 {
		t.Fatalf("stale failure still schedules retries: %+v %v", round, err)
	}
	b.mu.Lock()
	left := len(b.remoteFail)
	b.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d stale failures kept", left)
	}
}

// A new note reusing the name of one whose rename is stuck is reported, not left waiting silently,
// and the rest of the library keeps moving.
func TestNameHeldByStuckEntryIsReported(t *testing.T) {
	s, r, v := flakyService(t)
	old := mustCreate(t, s, v, "", "foo", "old", false)
	rounds(t, s, 1)
	cur := mustRead(t, s, v, old.ID)
	if _, err := s.Move(Edit{VaultID: v, ID: old.ID, ExpectedRevision: cur.Revision, Name: "bar.md"}); err != nil {
		t.Fatal(err)
	}
	r.rejectName = "bar.md"
	fresh := mustCreate(t, s, v, "", "foo", "new", false)
	other := mustCreate(t, s, v, "", "other", "x", false)
	rounds(t, s, 3)
	if got := mustRead(t, s, v, other.ID); got.Status != "synced" {
		t.Fatalf("unrelated note blocked: %s", got.Status)
	}
	problems, _ := s.Problems(v)
	ids := map[string]bool{}
	for _, p := range problems {
		ids[p.ID] = true
	}
	if !ids[old.ID] || !ids[fresh.ID] || len(problems) != 2 {
		t.Fatalf("problems: %+v", problems)
	}
	// Once the rename goes through, the new note follows.
	r.rejectName = ""
	if err := s.SyncNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, s, v, fresh.ID); got.Status != "synced" {
		t.Fatalf("new note still %s: %+v", got.Status, got)
	}
	if names := remoteNames(r.fakeRemote); names["foo.md"] != 1 || names["bar.md"] != 1 {
		t.Fatalf("remote: %v", names)
	}
}

// The manual sync retries what failed and carries a batch larger than one pass to the end.
func TestSyncNowFinishesTheQueue(t *testing.T) {
	s, _, v := flakyService(t)
	for i := 0; i < 30; i++ {
		mustCreate(t, s, v, "", fmt.Sprintf("n%02d", i), "x", false)
	}
	if err := s.SyncNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List(v, "", "", false)
	for _, e := range list {
		if e.Status != "synced" {
			t.Fatalf("%s left %s", e.Name, e.Status)
		}
	}
	// The background pass also keeps going after an import instead of waiting a minute.
	for i := 0; i < 20; i++ {
		mustCreate(t, s, v, "", fmt.Sprintf("m%02d", i), "x", false)
	}
	round, err := s.SyncRound(context.Background(), false)
	if err != nil || round.Pushed != pushBatch || !round.More {
		t.Fatalf("round %+v %v", round, err)
	}
}
