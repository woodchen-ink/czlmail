package oauth

import (
	"encoding/base64"
	"testing"

	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream/microsoft"
)

func idTok(claims string) *Token {
	enc := base64.RawURLEncoding.EncodeToString
	return &Token{IDToken: enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(claims)) + "."}
}

func TestMicrosoftServers(t *testing.T) {
	p := All()["microsoft"]
	_, smtp := p.Servers(p, idTok(`{"tid":"`+microsoft.PersonalTenant+`","oid":"o1"}`))
	if smtp.Host != "smtp-mail.outlook.com" || smtp.Port != 587 || smtp.TLS != "starttls" {
		t.Errorf("personal: %+v", smtp)
	}
	imap, smtp := p.Servers(p, idTok(`{"tid":"72f988bf-0000-0000-0000-000000000000"}`))
	if smtp.Host != "smtp.office365.com" || imap.Host != "outlook.office365.com" {
		t.Errorf("work: %+v %+v", imap, smtp)
	}
	p.SMTP = upstream.Endpoint{Host: "127.0.0.1", Port: 2525}
	if _, smtp := p.Servers(p, idTok(`{"tid":"`+microsoft.PersonalTenant+`"}`)); smtp.Host != "127.0.0.1" {
		t.Errorf("explicit server replaced: %+v", smtp)
	}
}

func TestTokenAccount(t *testing.T) {
	a := idTok(`{"oid":"00000000-0000-0000-1234-56789abcdef0","tid":"t","email":"me@outlook.test","preferred_username":"me@outlook.test","sub":"s"}`).AccountFromIDToken()
	if a == nil || a.OID != "00000000-0000-0000-1234-56789abcdef0" || a.Email != "me@outlook.test" || a.PreferredUsername != "me@outlook.test" {
		t.Fatalf("%+v", a)
	}
	if (&Token{}).AccountFromIDToken() != nil {
		t.Error("account without an id_token")
	}
	var none *TokenAccount
	if none.String() != "-" {
		t.Error(none.String())
	}
}
