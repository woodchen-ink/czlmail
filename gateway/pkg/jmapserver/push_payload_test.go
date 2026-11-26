package jmapserver_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

func TestEmailPushSizing(t *testing.T) {
	props := []string{"id", "from", "subject", "preview"}
	mail := func(i int, preview string) *jmapserver.NewMail {
		m := newMail(fmt.Sprintf("e%d", i), inboxID, "Bob", "bob@example.net", fmt.Sprintf("Subject %d", i), preview)
		return &m
	}

	// Small messages share one EmailPush.
	out := jmapserver.BuildEmailPushes("a1", "s9", []*jmapserver.NewMail{mail(1, "a"), mail(2, "b")}, props, 2600)
	if len(out) != 1 {
		t.Fatalf("%d pushes", len(out))
	}
	if n, ok := parseMailNotice(out[0], "s2"); !ok || n.actionID != "s2~a1/e1" || !strings.HasSuffix(n.body, "另有 1 封新邮件") {
		t.Fatalf("notice %+v", n)
	}

	// Many messages are split; every push fits and none is lost.
	var many []*jmapserver.NewMail
	for i := range 40 {
		many = append(many, mail(i, strings.Repeat("p", 200)))
	}
	out = jmapserver.BuildEmailPushes("a1", "", many, props, 2600)
	total := 0
	for _, b := range out {
		if len(b) > 2600 {
			t.Fatalf("push of %d bytes", len(b))
		}
		var p struct {
			Emails []map[string]any `json:"emails"`
			State  *string          `json:"state"`
		}
		_ = json.Unmarshal(b, &p)
		if p.State != nil {
			t.Fatal("state should be null when unknown")
		}
		total += len(p.Emails)
	}
	if len(out) < 2 || total != 40 {
		t.Fatalf("%d pushes, %d emails", len(out), total)
	}

	// A message too large alone keeps the longest prefix of properties that fits.
	out = jmapserver.BuildEmailPushes("a1", "", []*jmapserver.NewMail{mail(1, strings.Repeat("长", 2000))}, props, 2600)
	if len(out) != 1 || len(out[0]) > 2600 {
		t.Fatalf("pushes %d", len(out))
	}
	n, ok := parseMailNotice(out[0], "")
	if !ok || n.title != "Bob" || n.body != "Subject 1" || n.actionID != "a1/e1" {
		t.Fatalf("truncated notice %+v from %s", n, out[0])
	}
}
