package mail

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Blob ids of stored mail: m<emailId> is the whole message, p<emailId>.<section>
// one part with its transfer encoding removed. Email ids never contain a dot.
const (
	blobPrefixMessage = 'm'
	blobPrefixPart    = 'p'
)

func blobIDMessage(emailID string) string { return string(blobPrefixMessage) + emailID }

func blobIDPart(emailID, section string) string {
	return string(blobPrefixPart) + emailID + "." + section
}

// parseBlobID splits a mail blob id; section is "" for a whole message.
func parseBlobID(blobID string) (emailID, section string, ok bool) {
	if len(blobID) < 2 {
		return "", "", false
	}
	switch blobID[0] {
	case blobPrefixMessage:
		if strings.Contains(blobID, ".") {
			return "", "", false
		}
		return blobID[1:], "", true
	case blobPrefixPart:
		id, sec, found := strings.Cut(blobID[1:], ".")
		if !found || id == "" || sec == "" {
			return "", "", false
		}
		return id, sec, true
	}
	return "", "", false
}

// blobSource serves the m and p blobs.
type blobSource struct{ s *Service }

func (b blobSource) OpenBlob(ctx context.Context, _, accountID, blobID string) (*jmapserver.Blob, error) {
	r, size, err := b.s.openBlob(ctx, accountID, blobID)
	if err != nil {
		return nil, err
	}
	return &jmapserver.Blob{Body: r, Size: size}, nil
}

// openBlob opens a mail blob of the account. It returns jmapserver.ErrBlobNotFound
// for unknown ids and *jmapserver.UpstreamError when the upstream cannot serve it.
func (s *Service) openBlob(ctx context.Context, accountID, blobID string) (io.ReadCloser, int64, error) {
	emailID, section, ok := parseBlobID(blobID)
	if !ok {
		return nil, 0, jmapserver.ErrBlobNotFound
	}
	h, uerr := s.opts.Upstreams.Lookup(accountID)
	if h == nil {
		return nil, 0, uerr
	}
	a := &account{id: accountID, up: h.Upstream, idx: h.Upstream.Index(), down: uerr}
	msgs, err := loadMessages(a.idx, []string{emailID})
	if err != nil {
		return nil, 0, err
	}
	m := msgs[emailID]
	if m == nil {
		return nil, 0, jmapserver.ErrBlobNotFound
	}
	encoding := ""
	if section != "" {
		bl, err := s.structure(ctx, a, m)
		if err != nil {
			return nil, 0, blobError(err, a)
		}
		pt := bl.byID[section]
		if pt == nil {
			return nil, 0, jmapserver.ErrBlobNotFound
		}
		encoding = pt.node.Encoding
		if m.raw != nil {
			data, _ := decodeTransfer(m.raw.sections[section], encoding)
			return io.NopCloser(strings.NewReader(string(data))), int64(len(data)), nil
		}
	}
	key := cacheKey(a.id, emailID, section)
	f, cached := s.cache.Peek(key)
	if !cached {
		if a.down != nil {
			return nil, 0, a.down
		}
		if f, err = s.openSection(ctx, a, emailID, section); err != nil {
			return nil, 0, blobError(err, a)
		}
	}
	switch strings.ToLower(encoding) {
	case "base64", "quoted-printable":
		return readCloser{Reader: transferReader(f, encoding), Closer: f}, -1, nil
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

type readCloser struct {
	io.Reader
	io.Closer
}

func blobError(err error, a *account) error {
	if messageGone(err) {
		return jmapserver.ErrBlobNotFound
	}
	var ue *upstream.Error
	if errors.As(err, &ue) {
		if ue.Kind == upstream.KindRejected {
			return jmapserver.ErrBlobNotFound
		}
		st, code := ue.Status()
		return &jmapserver.UpstreamError{Status: st, Code: code}
	}
	if a.down != nil {
		return a.down
	}
	return err
}
