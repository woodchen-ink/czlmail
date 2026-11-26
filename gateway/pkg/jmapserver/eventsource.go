package jmapserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxPing caps the heartbeat interval a client may ask for.
const maxPing = 300

type stateChange struct {
	Type    string  `json:"@type"`
	Changed Changes `json:"changed"`
}

// EventSource serves GET /jmap/eventsource?types=&closeafter=&ping= (RFC 8620 §7.3).
func (s *Server) EventSource(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	types := parseTypes(q.Get("types"))
	closeAfter := q.Get("closeafter")
	if closeAfter != "" && closeAfter != "state" && closeAfter != "no" {
		writeProblem(w, problemBadQuery("closeafter must be state or no"))
		return
	}
	ping, err := parsePing(q.Get("ping"))
	if err != nil {
		writeProblem(w, problemBadQuery(err.Error()))
		return
	}

	rc := http.NewResponseController(w)
	sub := s.bus.subscribe(userID, types)
	defer s.bus.unsubscribe(sub)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-store")
	h.Set("X-Accel-Buffering", "no") // nginx: do not buffer the stream
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		s.log.Error("eventsource cannot flush", "trace_id", s.traceID(r.Context()), "err", err)
		return
	}

	var tick <-chan time.Time
	if ping > 0 {
		t := time.NewTicker(time.Duration(ping) * time.Second)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.done:
			return
		case <-tick:
			if writeEvent(w, rc, "ping", fmt.Sprintf(`{"interval":%d}`, ping)) != nil {
				return
			}
		case <-sub.notify:
			c := sub.take()
			if len(c) == 0 {
				continue
			}
			data, _ := json.Marshal(stateChange{Type: "StateChange", Changed: c})
			if writeEvent(w, rc, "state", string(data)) != nil || closeAfter == "state" {
				return
			}
		}
	}
}

func writeEvent(w http.ResponseWriter, rc *http.ResponseController, event, data string) error {
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	return rc.Flush()
}

// parseTypes reads the comma-separated type list; "*" or empty means all types.
func parseTypes(raw string) map[string]bool {
	if raw == "" {
		return nil
	}
	types := map[string]bool{}
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t == "*" {
			return nil
		}
		if t != "" {
			types[t] = true
		}
	}
	return types
}

func parsePing(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("ping must be a non-negative integer")
	}
	return min(n, maxPing), nil
}
