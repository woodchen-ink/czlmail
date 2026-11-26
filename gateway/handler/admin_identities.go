package handler

import (
	"errors"
	"net/http"

	"github.com/woodchen-ink/go-web-utils/resputil"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service/submission"
)

var errInvalidSentCopy = errors.New("invalid sent copy")

type identityInput struct {
	Email         *string               `json:"email"`
	Name          *string               `json:"name"`
	ReplyTo       *[]submission.Address `json:"replyTo"`
	Bcc           *[]submission.Address `json:"bcc"`
	TextSignature *string               `json:"textSignature"`
	HTMLSignature *string               `json:"htmlSignature"`
}

func (in *identityInput) toService() submission.IdentityInput {
	return submission.IdentityInput{Email: in.Email, Name: in.Name, ReplyTo: in.ReplyTo, Bcc: in.Bcc, TextSignature: in.TextSignature, HTMLSignature: in.HTMLSignature}
}

type identityView struct {
	ID            string               `json:"id"`
	Source        string               `json:"source"` // primary, alias, provider
	Email         string               `json:"email"`
	Name          string               `json:"name"`
	ReplyTo       []submission.Address `json:"replyTo"`
	Bcc           []submission.Address `json:"bcc"`
	TextSignature string               `json:"textSignature"`
	HTMLSignature string               `json:"htmlSignature"`
	MayDelete     bool                 `json:"mayDelete"`
	UpdatedAt     *string              `json:"updatedAt"`
}

func newIdentityView(r *model.Identity) identityView {
	v := identityView{ID: r.PublicID, Source: r.Source, Email: r.Email, Name: r.Name,
		ReplyTo: submission.DecodeAddresses(r.ReplyTo), Bcc: submission.DecodeAddresses(r.Bcc),
		TextSignature: r.TextSignature, HTMLSignature: r.HTMLSignature,
		MayDelete: r.Source == model.IdentityAlias, UpdatedAt: rfc3339(&r.UpdatedAt)}
	if v.ReplyTo == nil {
		v.ReplyTo = []submission.Address{}
	}
	if v.Bcc == nil {
		v.Bcc = []submission.Address{}
	}
	return v
}

// upstreamID checks that the actor may manage the upstream in the path and returns it.
func (h *Admin) upstreamID(r *http.Request) (string, error) {
	d, err := h.Upstreams.Get(&actor(r).User, r.PathValue("id"))
	if err != nil {
		return "", err
	}
	return d.PublicID, nil
}

// ListIdentities: GET /api/admin/upstreams/{id}/identities.
func (h *Admin) ListIdentities(w http.ResponseWriter, r *http.Request) {
	id, err := h.upstreamID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rows, err := h.Submission.Identities.List(id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]identityView, 0, len(rows))
	for i := range rows {
		out = append(out, newIdentityView(&rows[i]))
	}
	resputil.OK(w, out)
}

// CreateIdentity: POST /api/admin/upstreams/{id}/identities — an alias the
// mailbox may send as ("*@domain" for a whole domain).
func (h *Admin) CreateIdentity(w http.ResponseWriter, r *http.Request) {
	id, err := h.upstreamID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in identityInput
	if err := decodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	row, err := h.Submission.Identities.Create(id, in.toService(), true)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, newIdentityView(row))
}

// UpdateIdentity: PATCH /api/admin/upstreams/{id}/identities/{identityId}.
func (h *Admin) UpdateIdentity(w http.ResponseWriter, r *http.Request) {
	id, err := h.upstreamID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in identityInput
	if err := decodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	row, err := h.Submission.Identities.Update(id, r.PathValue("identityId"), in.toService())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, newIdentityView(row))
}

// DeleteIdentity: DELETE /api/admin/upstreams/{id}/identities/{identityId}.
func (h *Admin) DeleteIdentity(w http.ResponseWriter, r *http.Request) {
	id, err := h.upstreamID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.Submission.Identities.Delete(id, r.PathValue("identityId")); err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, struct{}{})
}

// SendTest: POST /api/admin/upstreams/{id}/send-test — sends a message to the
// mailbox itself, sees whether the server keeps a Sent copy and stores the
// matching sentCopy strategy.
func (h *Admin) SendTest(w http.ResponseWriter, r *http.Request) {
	id, err := h.upstreamID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	res, err := h.Submission.TestSentCopy(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, res)
}
