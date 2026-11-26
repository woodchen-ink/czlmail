package jmapserver

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"
)

// Blob is a downloadable blob opened by a BlobSource.
type Blob struct {
	Body io.ReadCloser
	// Size in bytes, or -1 when unknown.
	Size int64
	// ModTime enables conditional requests when the Body is seekable; optional.
	ModTime time.Time
}

// BlobSource opens blobs whose id starts with its prefix. It returns
// ErrBlobNotFound for unknown ids and *UpstreamError when the upstream holding
// the blob cannot serve it.
type BlobSource interface {
	OpenBlob(ctx context.Context, userID, accountID, blobID string) (*Blob, error)
}

// RegisterBlobSource routes downloads of blob ids starting with prefix to src.
// The 'u' prefix belongs to uploads.
func (s *Server) RegisterBlobSource(prefix byte, src BlobSource) {
	if prefix == UploadPrefix {
		panic("jmapserver: blob prefix 'u' is reserved for uploads")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobSrc[prefix] = src
}

type uploadResponse struct {
	AccountID string `json:"accountId"`
	BlobID    string `json:"blobId"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
}

// Upload serves POST /jmap/upload/{accountId}/ (RFC 8620 §6.1).
func (s *Server) Upload(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	accountID := r.PathValue("accountId")
	if !s.hasAccount(w, r, userID, accountID) {
		return
	}
	if r.ContentLength > MaxSizeUpload {
		writeProblem(w, problemUploadTooLarge())
		return
	}
	if !s.upl.acquire(userID) {
		writeProblem(w, problemLimit("maxConcurrentUpload"))
		return
	}
	defer s.upl.release(userID)

	ctype := mediaType(r.Header.Get("Content-Type"))
	id, meta, err := s.uploads.save(UploadMeta{UserID: userID, AccountID: accountID, Type: ctype}, r.Body)
	switch {
	case errors.Is(err, errUploadTooLarge):
		writeProblem(w, problemUploadTooLarge())
		return
	case err != nil:
		if r.Context().Err() == nil {
			s.log.Error("save upload", "trace_id", s.traceID(r.Context()), "user", userID, "err", err)
			writeProblem(w, problemInternal())
		}
		return
	}
	// 200 rather than RFC 8620's 201, like Stalwart: go-jmap's Upload only accepts 200.
	writeJSON(w, http.StatusOK, uploadResponse{AccountID: accountID, BlobID: id, Type: meta.Type, Size: meta.Size})
}

// Download serves GET /jmap/download/{accountId}/{blobId}/{name}?type= (RFC 8620 §6.2).
func (s *Server) Download(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	accountID, blobID, name := r.PathValue("accountId"), r.PathValue("blobId"), r.PathValue("name")
	if !s.hasAccount(w, r, userID, accountID) {
		return
	}
	blob, err := s.openBlob(r.Context(), userID, accountID, blobID)
	if err != nil {
		if u, ok := errors.AsType[*UpstreamError](err); ok {
			writeProblem(w, problemUpstreamUnavailable(u))
			return
		}
		if errors.Is(err, ErrBlobNotFound) {
			writeProblem(w, problemBlobNotFound())
			return
		}
		s.log.Error("open blob", "trace_id", s.traceID(r.Context()), "user", userID, "blob", blobID, "err", err)
		writeProblem(w, problemInternal())
		return
	}
	defer blob.Body.Close()

	h := w.Header()
	h.Set("Content-Type", mediaType(r.URL.Query().Get("type")))
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	h.Set("Cache-Control", "private, max-age=31536000, immutable") // a blob id never changes content
	if rs, ok := blob.Body.(io.ReadSeeker); ok {
		http.ServeContent(w, r, "", blob.ModTime, rs) // Range support
		return
	}
	if blob.Size >= 0 {
		h.Set("Content-Length", strconv.FormatInt(blob.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, blob.Body)
}

func (s *Server) openBlob(ctx context.Context, userID, accountID, blobID string) (*Blob, error) {
	if blobID == "" {
		return nil, ErrBlobNotFound
	}
	if blobID[0] == UploadPrefix {
		f, _, err := s.uploads.Open(userID, accountID, blobID)
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}
		return &Blob{Body: f, Size: info.Size(), ModTime: info.ModTime()}, nil
	}
	s.mu.RLock()
	src, ok := s.blobSrc[blobID[0]]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrBlobNotFound
	}
	return src.OpenBlob(ctx, userID, accountID, blobID)
}

// hasAccount writes 404 and returns false when the user has no such account.
func (s *Server) hasAccount(w http.ResponseWriter, r *http.Request, userID, accountID string) bool {
	sess, err := s.loadSession(r.Context(), userID)
	if err != nil {
		s.log.Error("load session", "trace_id", s.traceID(r.Context()), "user", userID, "err", err)
		writeProblem(w, problemInternal())
		return false
	}
	if _, ok := sess.Accounts[accountID]; !ok {
		writeProblem(w, problemAccountNotFound())
		return false
	}
	return true
}

// mediaType normalises a client-supplied media type, falling back to octet-stream.
func mediaType(t string) string {
	if t == "" {
		return "application/octet-stream"
	}
	mt, params, err := mime.ParseMediaType(t)
	if err != nil {
		return "application/octet-stream"
	}
	return mime.FormatMediaType(mt, params)
}
