package jmapserver

// Subscribers is the number of open EventSource connections of the user.
func Subscribers(s *Server, userID string) int {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	if hub := s.bus.users[userID]; hub != nil {
		return len(hub.subs)
	}
	return 0
}

// Push internals for tests.
var (
	EncryptPushWith   = encryptPushWith
	BuildEmailPushes  = buildEmailPushes
	CheckPushURL      = checkPushURL
	PublicAddr        = publicAddr
	ErrPrivateAddress = errPrivateAddress
)

// NewPushClient is the push delivery client.
var NewPushClient = newPushClient
