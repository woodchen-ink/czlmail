package mail

import (
	"mime"
	"strings"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
)

var bodyPartProperties = []string{"partId", "blobId", "size", "headers", "name", "type", "charset",
	"disposition", "cid", "language", "location", "subParts"}

var bodyPartDefaults = []string{"partId", "blobId", "size", "name", "type", "charset", "disposition", "cid",
	"language", "location"}

// part is a body part with the facts the JMAP properties need.
type part struct {
	node   *imapc.BodyPart
	partID string // IMAP section; "" for multipart nodes
	root   bool   // the message's own top-level part
}

func (p *part) mediaType() string { return p.node.Type + "/" + p.node.Subtype }
func (p *part) multipart() bool   { return p.node.Type == "multipart" }

// headerSection is the IMAP section holding the part's own header fields.
func (p *part) headerSection() string {
	if p.root {
		return "HEADER"
	}
	return p.node.Path + ".MIME"
}

// name is the decoded filename (RFC 2231 parameters, RFC 2047 words), or "".
func (p *part) name() string {
	for _, params := range []map[string]string{p.node.DispParams, p.node.Params} {
		for _, key := range []string{"filename", "name"} {
			if v := params[key]; v != "" {
				return decodeWords(v)
			}
			if v := rfc2231(params, key); v != "" {
				return v
			}
		}
	}
	return ""
}

// rfc2231 decodes a parameter imapc left in its starred form (key*, key*0*...).
func rfc2231(params map[string]string, key string) string {
	if v, ok := params[key+"*"]; ok {
		return decode2231(v, true)
	}
	if _, ok := params[key+"*0"]; !ok {
		if _, ok := params[key+"*0*"]; !ok {
			return ""
		}
	}
	var b strings.Builder
	encoded := false
	for i := 0; ; i++ {
		n := key + "*" + itoa(i)
		if v, ok := params[n+"*"]; ok {
			if i == 0 {
				encoded = true
			}
			b.WriteString(v)
		} else if v, ok := params[n]; ok {
			b.WriteString(v)
		} else {
			break
		}
	}
	return decode2231(b.String(), encoded)
}

func decode2231(v string, encoded bool) string {
	if !encoded {
		return validUTF8(v)
	}
	// charset'language'percent-encoded
	charset, rest, ok := strings.Cut(v, "'")
	if !ok {
		return validUTF8(v)
	}
	_, value, ok := strings.Cut(rest, "'")
	if !ok {
		return validUTF8(v)
	}
	// Let the mime package decode it through a synthetic header parameter.
	_, params, err := mime.ParseMediaType("x/x; p*=" + charset + "''" + value)
	if err != nil {
		return validUTF8(value)
	}
	return params["p"]
}

func (p *part) charset() any {
	if c := p.node.Params["charset"]; c != "" {
		return strings.ToLower(c)
	}
	if p.node.Type == "text" {
		return "us-ascii"
	}
	return nil
}

// decodedSize estimates the size after removing the transfer encoding (the
// JMAP size), from the encoded size BODYSTRUCTURE reports.
func (p *part) decodedSize() int64 {
	n := p.node.Size
	switch p.node.Encoding {
	case "base64":
		lines := n / 78 // 76 characters and CRLF
		return max((n-lines*2)*3/4, 0)
	case "quoted-printable":
		return n * 9 / 10
	}
	return n
}

// parts lists the message's body parts in the RFC 8621 sense: the tree, and the
// text / html / attachment lists of §4.1.4.
type bodyLists struct {
	tree        *part
	byID        map[string]*part
	textBody    []*part
	htmlBody    []*part
	attachments []*part
	children    map[*part][]*part
}

func buildBody(root *imapc.BodyPart) *bodyLists {
	bl := &bodyLists{byID: map[string]*part{}}
	if root == nil {
		root = &imapc.BodyPart{Path: "1", Type: "text", Subtype: "plain"}
	}
	var wrap func(n *imapc.BodyPart, isRoot bool) *part
	children := map[*part][]*part{}
	wrap = func(n *imapc.BodyPart, isRoot bool) *part {
		p := &part{node: n, root: isRoot}
		if n.Type != "multipart" {
			p.partID = n.Path
			bl.byID[p.partID] = p
		} else {
			for _, c := range n.Parts {
				if c != nil {
					children[p] = append(children[p], wrap(c, false))
				}
			}
		}
		return p
	}
	bl.tree = wrap(root, true)
	bl.children = children
	text, html := []*part{}, []*part{}
	bl.parse([]*part{bl.tree}, "mixed", false, &text, &html)
	bl.textBody, bl.htmlBody = text, html
	return bl
}

// parseStructure of RFC 8621 §4.1.4. tb / hb point at the lists being
// collected; a level sets its copy to nil to stop collecting into one (the
// JavaScript's "textBody = null"), which its caller does not see.
func (bl *bodyLists) parse(parts []*part, multipartType string, inAlternative bool, tb, hb *[]*part) {
	textLen, htmlLen := -1, -1
	if tb != nil {
		textLen = len(*tb)
	}
	if hb != nil {
		htmlLen = len(*hb)
	}
	for i, p := range parts {
		isMultipart := p.multipart()
		mt := p.mediaType()
		isMedia := p.node.Type == "image" || p.node.Type == "audio" || p.node.Type == "video"
		isInline := p.node.Disposition != "attachment" &&
			(mt == "text/plain" || mt == "text/html" || isMedia) &&
			(i == 0 || (multipartType != "related" && (isMedia || p.name() == "")))
		switch {
		case isMultipart:
			sub := p.node.Subtype
			bl.parse(bl.children[p], sub, inAlternative || sub == "alternative", tb, hb)
		case isInline:
			if multipartType == "alternative" {
				switch mt {
				case "text/plain":
					if tb != nil {
						*tb = append(*tb, p)
					}
				case "text/html":
					if hb != nil {
						*hb = append(*hb, p)
					}
				default:
					bl.attachments = append(bl.attachments, p)
				}
				continue
			} else if inAlternative {
				if mt == "text/plain" {
					hb = nil
				}
				if mt == "text/html" {
					tb = nil
				}
			}
			if tb != nil {
				*tb = append(*tb, p)
			}
			if hb != nil {
				*hb = append(*hb, p)
			}
			if (tb == nil || hb == nil) && isMedia {
				bl.attachments = append(bl.attachments, p)
			}
		default:
			bl.attachments = append(bl.attachments, p)
		}
	}
	if multipartType == "alternative" && tb != nil && hb != nil {
		// Found HTML but no text alternative (or the reverse): use the other.
		if textLen == len(*tb) && htmlLen != len(*hb) {
			*tb = append(*tb, (*hb)[htmlLen:]...)
		}
		if htmlLen == len(*hb) && textLen != len(*tb) {
			*hb = append(*hb, (*tb)[textLen:]...)
		}
	}
}

// hasText reports a text/* leaf (bodyValues are only for text parts).
func (p *part) isText() bool { return p.node.Type == "text" && !p.multipart() }
