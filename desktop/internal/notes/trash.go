package notes

// trashRoot shows one recoverable item per deleted subtree, not a flattened list of its children.
func (s *Service) trashRoot(e Entry) bool {
	for depth := 0; e.ParentID != "" && depth < 128; depth++ {
		parent, ok := s.entries[e.ParentID]
		if !ok {
			return false
		}
		if parent.Name == ".czl-trash" {
			return true
		}
		if parent.Deleted {
			return false
		}
		e = parent
	}
	return true
}

// effectivelyDeleted derives visibility from ancestor markers; caller holds s.mu.
func (s *Service) effectivelyDeleted(e Entry) bool {
	for depth := 0; depth < 128; depth++ {
		if e.Deleted || e.Name == ".czl-trash" {
			return true
		}
		if e.ParentID == "" {
			return false
		}
		parent, ok := s.entries[e.ParentID]
		if !ok || parent.VaultID != e.VaultID {
			return true
		}
		e = parent
	}
	return true
}
