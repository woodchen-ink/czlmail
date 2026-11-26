package jmaptest

import (
	"fmt"
	"net/http"
)

// stream 是一条 EventSource 连接。
type stream struct {
	user *User
	ch   chan []byte
	done chan struct{}
}

// send 不阻塞: 客户端处理慢时丢掉这次推送 (下一次 StateChange 带着最新的全部 state)。调用方持有 s.mu。
func (st *stream) send(payload []byte) {
	select {
	case st.ch <- payload:
	default:
	}
}

// dropStreamsLocked 断开满足条件的推送连接。调用方持有 s.mu。
func (s *Server) dropStreamsLocked(match func(*stream) bool) {
	for st := range s.streams {
		if match(st) {
			close(st.done)
			delete(s.streams, st)
		}
	}
}

// StreamCount 返回某用户当前的推送连接数 (用户为 nil 时是全部), 供测试等客户端连上。
func (s *Server) StreamCount(u *User) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for st := range s.streams {
		if u == nil || st.user == u {
			n++
		}
	}
	return n
}

func (s *Server) handleEventSource(w http.ResponseWriter, r *http.Request, u *User) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	st := &stream{user: u, ch: make(chan []byte, 16), done: make(chan struct{})}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		http.Error(w, "closed", http.StatusServiceUnavailable)
		return
	}
	s.streams[st] = struct{}{}
	// 连上就推一次当前全部 state (与 Stalwart 一致): 客户端引导完到推送连上之间的变化不会漏掉。
	for _, g := range u.grants {
		st.send(statePayloadLocked(g.acc))
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if _, ok := s.streams[st]; ok {
			delete(s.streams, st)
		}
		s.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-st.done:
			return
		case payload := <-st.ch:
			if _, err := fmt.Fprintf(w, "event: state\ndata: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
