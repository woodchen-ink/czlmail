package notes

// HasPending is a cheap scheduler hint; it never treats queued work as a completed remote commit.
func (s *Service) HasPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled {
		return false
	}
	for _, e := range s.entries {
		if e.Status == "pending" && !e.Uncertain && (e.Deleted || !s.effectivelyDeleted(e)) {
			return true
		}
	}
	return false
}
