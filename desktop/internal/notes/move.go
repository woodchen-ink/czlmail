package notes

// safeMove checks only descendant synchronization state, never document text or references.
// The caller holds s.mu; stable parent IDs preserve the moved directory's subtree.
func (s *Service) safeMove(root Entry) error {
	if !root.Directory {
		return nil
	}
	for _, e := range s.entries {
		if e.VaultID != root.VaultID || e.Status != "conflict" && !e.Uncertain {
			continue
		}
		for id, depth := e.ParentID, 0; id != "" && depth <= 64; depth++ {
			if id == root.ID {
				return ErrConflict
			}
			ancestor, ok := s.entries[id]
			if !ok || ancestor.VaultID != root.VaultID {
				break
			}
			id = ancestor.ParentID
		}
	}
	return nil
}
