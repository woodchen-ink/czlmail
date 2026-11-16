package notes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// pushBatch bounds one round so a large migration checkpoints progress instead of running for minutes.
const pushBatch = 12

// Round summarizes one synchronization pass for the scheduler.
type Round struct {
	// Pushed counts entries uploaded in this pass.
	Pushed int
	// More: this pass made progress and uploadable work remains, so another pass may follow at once.
	More bool
	// RetryAt is the earliest time (Unix ms) a backed-off entry is due; 0 when none is waiting.
	RetryAt int64
}

// Delta is what changed remotely since a known state.
type Delta struct {
	State     string
	Changed   []Node
	Destroyed []string
}

// Changer is an optional Remote capability: incremental changes instead of a full listing.
// Any error makes the caller fall back to Snapshot.
type Changer interface {
	Changes(ctx context.Context, account, since string) (Delta, error)
}

// inspector is an optional Remote capability: read one object and the current state.
type inspector interface {
	Inspect(context.Context, string, string) (Snapshot, error)
}

// remoteView is the sync worker's picture of the account's files, kept between rounds and
// advanced by Changes. settled means every node in it has been reconciled with local entries.
type remoteView struct {
	state   string
	nodes   map[string]Node
	settled bool
}

// recentEdit: an entry edited this recently is probably still being typed; it does not by itself
// justify running the next pass at once.
const recentEdit = 3 * time.Second

// Sync runs one pass unless another is already running.
func (s *Service) Sync(ctx context.Context) error {
	_, err := s.SyncRound(ctx, false)
	return err
}

// SyncNow is the manual sync: it waits for a pass already running, forgets recorded failures so
// everything is tried again, and runs passes until nothing more moves.
func (s *Service) SyncNow(ctx context.Context) error {
	for !s.syncMu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer s.syncMu.Unlock()
	// Reset only once the running pass is over, or what it just recorded would be left out.
	if err := s.RetryAll(); err != nil {
		return err
	}
	for pass := 0; pass < 20; pass++ {
		round, err := s.syncLocked(ctx)
		if err != nil || round.Pushed == 0 {
			return err
		}
	}
	return nil
}

// SyncRound reconciles remote changes, then uploads queued local work. One failing entry is
// recorded on that entry and never stops the others; only account-wide trouble (offline, signed
// out, missing root) ends the pass. With wait, a pass already running is awaited first.
// Network requests never hold the local editing mutex.
func (s *Service) SyncRound(ctx context.Context, wait bool) (Round, error) {
	if wait {
		s.syncMu.Lock()
	} else if !s.syncMu.TryLock() {
		return Round{}, nil
	}
	defer s.syncMu.Unlock()
	return s.syncLocked(ctx)
}

// syncLocked is one pass; the caller holds syncMu.
func (s *Service) syncLocked(ctx context.Context) (Round, error) {
	s.mu.Lock()
	enabled := s.config.Enabled
	r := s.remote
	vaults := []Vault{}
	for _, v := range s.vaults {
		vaults = append(vaults, v)
	}
	s.mu.Unlock()
	if !enabled {
		return Round{}, ErrDisabled
	}
	if r == nil {
		return Round{}, ErrUnavailable
	}
	var total Round
	var first error
	for _, v := range vaults {
		round, err := s.syncVault(ctx, r, v)
		total.Pushed += round.Pushed
		total.More = total.More || round.More
		total.RetryAt = earliest(total.RetryAt, round.RetryAt)
		raced := errors.Is(err, ErrStateChanged)
		if raced {
			// Other files in the account kept changing under this pass; simply look again shortly.
			total.RetryAt = earliest(total.RetryAt, time.Now().Add(5*time.Second).UnixMilli())
			err = nil
		}
		s.mu.Lock()
		before := v
		if err != nil {
			v.Error = err.Error()
			if first == nil {
				first = err
			}
		} else {
			v.Error = ""
			if !raced {
				v.SyncedAt = nowUTC()
			}
		}
		if saveErr := s.put("vault", v.ID, v); saveErr == nil {
			s.vaults[v.ID] = v
		}
		if before.Error != v.Error || round.Pushed > 0 {
			s.notify()
		}
		s.mu.Unlock()
	}
	return total, first
}

// earliest merges two optional deadlines.
func earliest(a, b int64) int64 {
	if a == 0 || b != 0 && b < a {
		return b
	}
	return a
}

// syncVault first pulls remote state, including deletions, before considering local uploads.
func (s *Service) syncVault(ctx context.Context, r Remote, v Vault) (Round, error) {
	if s.views == nil {
		s.views = map[string]*remoteView{}
	}
	view := s.views[v.ID]
	if view == nil {
		view = &remoteView{}
		s.views[v.ID] = view
	}
	if err := s.refresh(ctx, r, v, view); err != nil {
		return Round{}, err
	}
	if _, ok := view.nodes[v.RootID]; !ok {
		delete(s.views, v.ID)
		return Round{}, ErrNotFound
	}
	if !view.settled {
		if err := s.pullAll(ctx, r, v, view); err != nil {
			return Round{}, err
		}
	}
	round, err := s.pushAll(ctx, r, v, view)
	s.mu.Lock()
	now := nowUTC()
	for _, e := range s.entries {
		if e.VaultID != v.ID || !s.queued(e) {
			continue
		}
		if e.SyncRetryAt > now {
			round.RetryAt = earliest(round.RetryAt, e.SyncRetryAt)
		} else if e.SyncError == "" && round.Pushed > 0 {
			// An existing note edited a moment ago is still being typed: it comes back through the
			// edit debounce, not through here. New files (an import, a fresh folder) carry on.
			typing := e.RemoteID != "" && !e.Directory && !e.Attachment && now-e.Modified < recentEdit.Milliseconds()
			if !typing {
				round.More = true
			}
		}
	}
	for _, f := range s.remoteFail {
		// Only deadlines still ahead: one already due was retried by this very pass.
		if f.vault == v.ID && f.kind == FailTemporary && f.retryAt > now {
			round.RetryAt = earliest(round.RetryAt, f.retryAt)
		}
	}
	s.mu.Unlock()
	return round, err
}

// refresh advances the view: incrementally when the remote supports it, otherwise by a full listing.
func (s *Service) refresh(ctx context.Context, r Remote, v Vault, view *remoteView) error {
	if c, ok := r.(Changer); ok && view.state != "" {
		d, err := c.Changes(ctx, v.AccountID, view.state)
		if err == nil {
			for _, id := range d.Destroyed {
				delete(view.nodes, id)
			}
			for _, n := range d.Changed {
				view.nodes[n.ID] = n
			}
			if d.State != view.state || len(d.Changed)+len(d.Destroyed) > 0 {
				view.settled = false
			}
			view.state = d.State
			return nil
		}
		if fatal(ctx, err) {
			return err
		}
	}
	snap, err := r.Snapshot(ctx, v.AccountID)
	if err != nil {
		return err
	}
	view.replace(snap)
	return nil
}

// replace installs a complete listing.
func (view *remoteView) replace(snap Snapshot) {
	if snap.State != view.state || view.nodes == nil {
		view.settled = false
	}
	view.state = snap.State
	view.nodes = make(map[string]Node, len(snap.Nodes))
	for _, n := range snap.Nodes {
		view.nodes[n.ID] = n
	}
}

// pullAll reconciles every remote node in the library with its local entry. A node that fails is
// remembered in remoteFail and skipped; two temporary failures in a row mean the connection is
// gone and end the pass.
func (s *Service) pullAll(ctx context.Context, r Remote, v Vault, view *remoteView) error {
	remote := view.nodes
	pending := map[string]Node{}
	for _, n := range remote {
		if descendant(remote, n.ID, v.RootID) {
			pending[n.ID] = n
		}
	}
	settled := true
	streak := 0
	now := nowUTC()
	// Parent-before-child discovery gives every remote node a stable local mapping.
	for len(pending) > 0 {
		progressed := false
		for id, n := range pending {
			s.mu.Lock()
			if !s.config.Enabled {
				s.mu.Unlock()
				return ErrDisabled
			}
			parent, ok := s.localParent(v, n.ParentID)
			f, failed := s.remoteFail[id]
			s.mu.Unlock()
			if !ok {
				continue
			}
			delete(pending, id)
			progressed = true
			if failed && sameNode(&f.node, &n) && (f.kind != FailTemporary || f.retryAt > now) {
				settled = false
				continue
			}
			pullCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			err := s.pull(pullCtx, r, v, n, parent, remote)
			cancel()
			if err == nil {
				streak = 0
				if failed {
					s.mu.Lock()
					delete(s.remoteFail, id)
					s.notify()
					s.mu.Unlock()
				}
				continue
			}
			if fatal(ctx, err) {
				return err
			}
			settled = false
			if s.pullFailed(v, n, err) == FailTemporary {
				if streak++; streak >= 2 {
					return err
				}
			} else {
				streak = 0
			}
		}
		if !progressed {
			// What is left hangs below a node that could not be pulled; it is retried with its parent.
			settled = false
			break
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A file that failed to download and has since been deleted or moved out is no longer a problem.
	for id, f := range s.remoteFail {
		if f.vault == v.ID && !descendant(remote, id, v.RootID) {
			delete(s.remoteFail, id)
		}
	}
	for _, e := range s.entries {
		if e.VaultID != v.ID {
			continue
		}
		if e.RemoteID != "" {
			if _, ok := remote[e.RemoteID]; !ok {
				e.Remote = nil
				if e.Status == "synced" {
					e.Deleted = true
					e.Status = "remote_deleted"
				} else if e.Status != "remote_deleted" {
					e.Status = "conflict"
				}
				clearFailure(&e)
				stamp(&e)
				if err := s.saveEntry(e); err != nil {
					return err
				}
			}
		}
		// Entries synced before creation times were kept learn theirs from the server once.
		if n, ok := remote[e.RemoteID]; ok && e.Created == 0 && n.Created > 0 {
			e.Created = n.Created
			if err := s.saveEntry(e); err != nil {
				return err
			}
		}
	}
	view.settled = settled
	return nil
}

// pullFailed remembers a remote node that could not be reconciled and returns the failure kind.
func (s *Service) pullFailed(v Vault, n Node, err error) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remoteFail == nil {
		s.remoteFail = map[string]remoteFailure{}
	}
	f := s.remoteFail[n.ID]
	f.vault, f.node = v.ID, n
	f.kind, f.detail = classify(err)
	f.attempts++
	f.retryAt = time.Now().Add(backoff(f.attempts)).UnixMilli()
	s.remoteFail[n.ID] = f
	s.notify()
	return f.kind
}

// pushAll uploads queued entries in dependency order: folders, then attachments, then notes.
func (s *Service) pushAll(ctx context.Context, r Remote, v Vault, view *remoteView) (Round, error) {
	s.mu.Lock()
	ids := []string{}
	now := nowUTC()
	for id, e := range s.entries {
		if e.VaultID == v.ID && s.queued(e) && e.SyncRetryAt <= now {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := s.entries[ids[i]], s.entries[ids[j]]
		if a.Directory != b.Directory {
			return a.Directory
		}
		if a.Attachment != b.Attachment {
			return a.Attachment
		}
		return a.Modified < b.Modified
	})
	s.mu.Unlock()
	var round Round
	sent, streak := 0, 0
	for _, id := range ids {
		// Only entries that actually reached the server count against the batch.
		if sent >= pushBatch {
			break
		}
		pushed, err := s.push(ctx, r, v, view, id)
		switch {
		case err == nil:
			if pushed {
				sent++
				round.Pushed++
				streak = 0
			}
		case fatal(ctx, err):
			return round, err
		case errors.Is(err, ErrConflict):
			// The remote object itself changed; the next pull decides between adopting and conflict.
			view.settled = false
		case errors.Is(err, ErrStateChanged):
			// The account stayed busy through every retry. Nothing is wrong with this entry: come
			// back shortly without counting a failure.
			round.RetryAt = earliest(round.RetryAt, time.Now().Add(5*time.Second).UnixMilli())
		default:
			sent++
			s.mu.Lock()
			e, ok := s.entries[id]
			if ok {
				fail(&e, err)
				saveErr := s.saveEntry(e)
				if saveErr != nil {
					s.mu.Unlock()
					return round, saveErr
				}
			}
			s.mu.Unlock()
			if kind, _ := classify(err); kind != FailTemporary {
				streak = 0
			} else if streak++; streak >= 2 {
				return round, err
			}
		}
	}
	return round, nil
}

// advancing reports an entry that an automatic round is still expected to upload without help:
// queued, never failed, and not stuck below a folder that cannot be created. Caller holds s.mu.
func (s *Service) advancing(e Entry) bool {
	for depth := 0; depth < 64; depth++ {
		if !s.queued(e) || e.SyncError != "" {
			return false
		}
		if e.ParentID == "" {
			return true
		}
		parent, ok := s.entries[e.ParentID]
		if !ok {
			return false
		}
		if parent.RemoteID != "" {
			return true
		}
		e = parent
	}
	return false
}

// descendant recognizes only nodes inside this vault and rejects remote cycles.
func descendant(nodes map[string]Node, id, root string) bool {
	for depth := 0; id != "" && depth < 128; depth++ {
		n, ok := nodes[id]
		if !ok {
			return false
		}
		if n.ParentID == root {
			return true
		}
		id = n.ParentID
	}
	return false
}

// localParent maps remote roots and parents while the caller holds the local lock.
func (s *Service) localParent(v Vault, remoteID string) (string, bool) {
	if remoteID == v.RootID {
		return "", true
	}
	for _, e := range s.entries {
		if e.VaultID == v.ID && e.RemoteID == remoteID {
			return e.ID, true
		}
	}
	return "", false
}

// sameNode compares real metadata and immutable content version, never wall clocks.
func sameNode(a, b *Node) bool {
	return a != nil && b != nil && a.ID == b.ID && a.ParentID == b.ParentID && a.Name == b.Name && a.BlobID == b.BlobID && a.Directory == b.Directory
}

// pull fetches changed text outside the lock and checks localRevision again before installing it.
func (s *Service) pull(ctx context.Context, r Remote, v Vault, n Node, parent string, nodes map[string]Node) error {
	deleted := false
	for id, depth := n.ID, 0; id != "" && depth < 128; depth++ {
		node, ok := nodes[id]
		if !ok {
			break
		}
		if node.Name == ".czl-trash" {
			deleted = true
			break
		}
		id = node.ParentID
	}
	s.mu.Lock()
	var e Entry
	found := false
	for _, candidate := range s.entries {
		if candidate.VaultID == v.ID && candidate.RemoteID == n.ID {
			e = clone(candidate)
			found = true
			break
		}
	}
	if found && (e.Deleted == deleted || e.Status == "pending") && (sameNode(e.Base, &n) || e.Status == "conflict" && sameNode(e.Remote, &n)) {
		s.mu.Unlock()
		return nil
	}
	revision := e.Revision
	s.mu.Unlock()
	isText := !n.Directory && strings.EqualFold(extension(n.Name), ".md")
	content := ""
	if isText {
		data, err := r.Download(ctx, v.AccountID, n.BlobID, MaxText)
		if err != nil {
			return err
		}
		content = string(data)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled {
		return ErrDisabled
	}
	if found {
		current := s.entries[e.ID]
		if current.Revision != revision {
			return nil
		}
		e = current
		if e.Status == "pending" && ownWrite(e, n, isText, content) {
			e.Base, e.Remote = &n, &n
			if isText {
				e.BaseHash = e.IntentHash
			}
			e.Uncertain, e.Intent, e.IntentHash = false, nil, ""
			wanted := e.Name
			if e.Deleted {
				wanted = e.ID + "--" + e.Name
			}
			if e.Deleted == deleted && n.Name == wanted && (deleted || e.ParentID == parent) && (!isText || e.Content == content) {
				// Nothing changed locally since that write: it is simply done.
				e.Size = n.Size
				e.Status = "synced"
				clearFailure(&e)
			}
			return s.saveEntry(e)
		}
	} else {
		e = Entry{ID: newID(), VaultID: v.ID, RemoteID: n.ID, Status: "synced"}
		for _, local := range s.entries {
			if local.VaultID != v.ID || local.RemoteID != "" || local.ParentID != parent || !strings.EqualFold(local.Name, n.Name) {
				continue
			}
			// Something trashed here before it was ever uploaded is not the counterpart of a live
			// remote file; pairing them would hide that file in the local trash.
			if !local.Uncertain && s.effectivelyDeleted(local) {
				continue
			}
			lost := local.Uncertain
			if lost {
				// A create whose response was lost: this remote file is its result when the text is
				// what was sent. Blob IDs cannot tell, the server replaces them on attach.
				local = clone(local)
				hash := local.IntentHash
				local.Uncertain, local.Intent, local.IntentHash = false, nil, ""
				if isText && !local.Directory && !local.Attachment && hash != "" && digest(content) == hash {
					e = local
					e.RemoteID = n.ID
					e.Base, e.Remote, e.BaseHash = &n, &n, hash
					if e.Content == content && e.Deleted == deleted {
						e.Status = "synced"
					}
					found = true
					break
				}
			}
			// Both sides created the same path before they ever met, e.g. a new device opened today's
			// journal before its first sync. The local create was never sent, so nothing is uncertain.
			switch {
			case local.Directory && n.Directory:
				// Folders carry no content: adopt the remote one, local children keep their parent ID.
				e = clone(local)
				e.Status = "synced"
			case lost && local.Attachment && !n.Directory && !isText && local.Name == n.Name && local.Size == n.Size:
				// The attachment this device sent; names carry a random suffix, so name and size suffice.
				e = clone(local)
				e.Status = "synced"
			case isText && !local.Directory && !local.Attachment:
				e = clone(local)
				if local.Content == content || local.Generated && local.Revision == 1 {
					// Same text, or an untouched template: nothing of the user's is lost by taking the remote.
					e.Status = "synced"
				}
				// Otherwise stays pending and becomes a conflict below; both sides remain readable.
			default:
				// Attachments cannot be merged and a folder cannot become a file: keep both, the local one renamed ("name 2").
				aside := clone(local)
				aside.Name = s.freeName(v.ID, parent, local.Name, local.Directory)
				stamp(&aside)
				if err := s.saveEntry(aside); err != nil {
					return err
				}
				continue
			}
			e.RemoteID = n.ID
			found = true
			break
		}
	}
	// The server may normalize an uploaded blob ID after it is attached to a FileNode.
	// Preserve a newer local draft when the downloaded body is exactly the acknowledged base.
	if found && e.Status == "pending" && isText && e.Base != nil && e.BaseHash != "" && digest(content) == e.BaseHash && e.Base.Name == n.Name && e.Base.ParentID == n.ParentID && e.Deleted == deleted {
		e.Base = &n
		e.Remote = &n
		return s.saveEntry(e)
	}
	if found && e.Status != "synced" && !(isText && e.Content == content && e.ParentID == parent && e.Name == n.Name && e.Deleted == deleted) {
		e.Remote = &n
		e.RemoteContent = content
		e.Status = "conflict"
		clearFailure(&e)
		stamp(&e)
		return s.saveEntry(e)
	}
	if deleted && !e.Deleted {
		e.OriginalParent = e.ParentID
	}
	e.ParentID = parent
	if !deleted || e.Name == "" {
		e.Name = n.Name
	}
	e.Directory = n.Directory
	e.Attachment = !n.Directory && !isText
	e.Type = n.Type
	e.Size = n.Size
	if e.Attachment && e.Base != nil && e.Base.BlobID != n.BlobID {
		e.BlobKey = ""
	}
	e.Content = content
	if isText {
		e.BaseHash = digest(content)
	}
	e.Base = &n
	e.Remote = &n
	e.RemoteContent = ""
	e.Status = "synced"
	clearFailure(&e)
	e.Deleted = deleted
	if e.Created == 0 {
		e.Created = n.Created
	}
	stamp(&e)
	// The installed version was written elsewhere; show when it was written, not when it arrived.
	if n.Modified > 0 {
		e.Modified = n.Modified
	}
	return s.saveEntry(e)
}

// ownWrite recognises the result of this device's own request whose response never arrived.
func ownWrite(e Entry, n Node, isText bool, content string) bool {
	i := e.Intent
	if i == nil || i.Name != n.Name || i.ParentID != n.ParentID || i.Directory != n.Directory {
		return false
	}
	return !isText || e.IntentHash != "" && digest(content) == e.IntentHash
}

// push uploads one entry. A state race (some other file in the account changed between read and
// write) is retried here against a refreshed view instead of being reported.
// It returns false with a nil error when the entry has to wait for something else.
func (s *Service) push(ctx context.Context, r Remote, v Vault, view *remoteView, id string) (bool, error) {
	// Text is small; an attachment may be tens of megabytes on a slow uplink.
	limit := 2 * time.Minute
	s.mu.Lock()
	if s.entries[id].Attachment {
		limit = 8 * time.Minute
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	for attempt := 0; ; attempt++ {
		pushed, err := s.pushOnce(ctx, r, v, view, id)
		if !errors.Is(err, ErrStateChanged) || attempt >= 3 {
			return pushed, err
		}
		if err = s.refresh(ctx, r, v, view); err != nil {
			return false, err
		}
	}
}

// pushOnce rechecks the current remote object and uses the observed state as a precondition.
func (s *Service) pushOnce(ctx context.Context, r Remote, v Vault, view *remoteView, id string) (bool, error) {
	s.mu.Lock()
	if !s.config.Enabled {
		s.mu.Unlock()
		return false, ErrDisabled
	}
	e, ok := s.entries[id]
	if !ok || e.Status != "pending" {
		s.mu.Unlock()
		return false, nil
	}
	parent := v.RootID
	// A trashed entry goes to the trash folder, wherever its local parent stands.
	if e.ParentID != "" && !e.Deleted {
		p := s.entries[e.ParentID]
		if p.RemoteID == "" {
			s.mu.Unlock()
			return false, nil
		}
		parent = p.RemoteID
	}
	if !e.Directory && !e.Attachment {
		// A note waits only for attachments that are about to be uploaded themselves; failed,
		// conflicting or remotely deleted attachments never hold a note back.
		for _, a := range s.entries {
			if a.VaultID == v.ID && a.Attachment && s.advancing(a) {
				s.mu.Unlock()
				return false, nil
			}
		}
	}
	if e.RemoteID == "" {
		// Never create over a remote file of the same name: the next pull pairs the two, which is
		// also how a create whose response was lost finds the object it made.
		for _, n := range view.nodes {
			if n.ParentID != parent || !strings.EqualFold(n.Name, e.Name) {
				continue
			}
			// The name may still be held by an entry of ours whose rename or trash has not gone
			// up yet. While that one is on its way this waits; if it is stuck, say so here too
			// instead of waiting forever in silence.
			for _, holder := range s.entries {
				if holder.VaultID == v.ID && holder.RemoteID == n.ID && !s.advancing(holder) && !(s.queued(holder) && holder.SyncError == FailTemporary) {
					s.mu.Unlock()
					return false, fmt.Errorf("%w: the name is still taken on the server by %q, which has not synchronized", ErrRejected, holder.Name)
				}
			}
			view.settled = false
			s.mu.Unlock()
			return false, nil
		}
		if e.Uncertain {
			// The pull that just ran saw no such file, so the earlier request never took effect.
			e.Uncertain, e.Intent, e.IntentHash = false, nil, ""
			if err := s.saveEntry(e); err != nil {
				s.mu.Unlock()
				return false, err
			}
			if s.localOnlyTrash(e) {
				s.mu.Unlock()
				return false, nil
			}
		}
	}
	s.mu.Unlock()
	var snap Snapshot
	var err error
	if in, ok := r.(inspector); ok {
		snap, err = in.Inspect(ctx, v.AccountID, e.RemoteID)
		if err == nil && (e.Deleted || e.RemoteID == "") && snap.State != view.state {
			// The trash folder and the "no file of this name yet" check both come from the view,
			// so the write must be conditional on exactly the state the view shows.
			return false, ErrStateChanged
		}
	} else if snap, err = r.Snapshot(ctx, v.AccountID); err == nil {
		view.replace(snap)
	}
	if err != nil {
		return false, err
	}
	if e.Deleted {
		trash := ""
		for _, n := range view.nodes {
			if n.ParentID == v.RootID && n.Name == ".czl-trash" && n.Directory {
				trash = n.ID
				break
			}
		}
		if trash == "" {
			if _, err = r.Set(ctx, v.AccountID, snap.State, "", Node{Name: ".czl-trash", ParentID: v.RootID, Directory: true}); err != nil {
				return false, err
			}
			// The state moved with that write; come back with a refreshed view.
			return false, ErrStateChanged
		}
		parent = trash
	}
	var current *Node
	for _, n := range snap.Nodes {
		if n.ID == e.RemoteID {
			copy := n
			current = &copy
		}
	}
	if e.RemoteID != "" && !sameNode(e.Base, current) {
		return false, ErrConflict
	}
	// A rename, move or trash of unchanged content keeps the blob the server already has.
	reuse := e.RemoteID != "" && e.Base != nil && e.Base.BlobID != "" && (e.Attachment || e.BaseHash != "" && digest(e.Content) == e.BaseHash)
	blob := ""
	if !e.Directory && !reuse {
		data := []byte(e.Content)
		if e.Attachment {
			data, err = s.attachmentBytes(e)
			if err != nil {
				return false, err
			}
		}
		blob, err = r.Upload(ctx, v.AccountID, data)
		if err != nil {
			return false, err
		}
	}
	n := Node{ID: e.RemoteID, ParentID: parent, Name: e.Name, Directory: e.Directory, BlobID: blob, Type: e.Type}
	if e.Deleted {
		n.Name = e.ID + "--" + e.Name
	}
	s.mu.Lock()
	if !s.config.Enabled {
		s.mu.Unlock()
		return false, ErrDisabled
	}
	latest := s.entries[id]
	if latest.Revision != e.Revision {
		s.mu.Unlock()
		return false, nil
	}
	// Persist what is about to be sent: if the response is lost, the next pull recognises the
	// result by name, place and text instead of mistaking it for someone else's change.
	latest.Intent = &n
	latest.IntentHash = ""
	if !e.Directory && !e.Attachment {
		latest.IntentHash = digest(e.Content)
	}
	latest.Uncertain = e.RemoteID == ""
	if err = s.saveEntry(latest); err != nil {
		s.mu.Unlock()
		return false, err
	}
	s.mu.Unlock()
	result, err := r.Set(ctx, v.AccountID, snap.State, e.RemoteID, n)
	s.mu.Lock()
	defer s.mu.Unlock()
	latest = s.entries[id]
	if err != nil {
		if !errors.Is(err, ErrUncertain) {
			latest.Uncertain, latest.Intent, latest.IntentHash = false, nil, ""
			if saveErr := s.saveEntry(latest); saveErr != nil {
				return false, saveErr
			}
		}
		return false, err
	}
	if reuse && result.BlobID == "" {
		result.BlobID, result.Size = e.Base.BlobID, e.Base.Size
	}
	latest.RemoteID = result.ID
	latest.Base = &result
	if !e.Directory && !e.Attachment {
		latest.BaseHash = digest(e.Content)
	}
	latest.Intent, latest.IntentHash = nil, ""
	latest.Remote = &result
	latest.Uncertain = false
	clearFailure(&latest)
	if latest.Revision == e.Revision {
		latest.Status = "synced"
	}
	view.nodes[result.ID] = result
	return true, s.saveEntry(latest)
}

// extension is deliberately case-insensitive at call sites for cross-platform Markdown names.
func extension(name string) string {
	index := strings.LastIndexByte(name, '.')
	if index < 0 {
		return ""
	}
	return name[index:]
}
