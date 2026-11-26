package capability

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"
)

func raw(pairs ...string) map[jmap.URI]json.RawMessage {
	out := map[jmap.URI]json.RawMessage{}
	for i := 0; i < len(pairs); i += 2 {
		out[jmap.URI(pairs[i])] = json.RawMessage(pairs[i+1])
	}
	return out
}

// TestOfMailOnlyProxy 只有邮件能力的服务器 (如经 JMAP 代理接入的 Gmail) 只剩邮件与发信。
func TestOfMailOnlyProxy(t *testing.T) {
	f := Of(raw(
		"urn:ietf:params:jmap:mail", `{}`,
		"urn:ietf:params:jmap:submission", `{"maxDelayedSend":0,"submissionExtensions":{}}`,
	))
	want := Features{Submission: true}
	if f != want {
		t.Fatalf("got %+v, want %+v", f, want)
	}
}

// TestOfFullServer 公布全部能力的服务器 (Stalwart) 所有功能都在, 定时发送上限取自 submission 能力。
func TestOfFullServer(t *testing.T) {
	f := Of(raw(
		"urn:ietf:params:jmap:mail", `{}`,
		"urn:ietf:params:jmap:submission", `{"maxDelayedSend":2592000}`,
		"urn:ietf:params:jmap:vacationresponse", `{}`,
		"urn:ietf:params:jmap:sieve", `{}`,
		"urn:ietf:params:jmap:calendars", `{}`,
		"urn:ietf:params:jmap:calendars:parse", `{}`,
		"urn:ietf:params:jmap:contacts", `{}`,
		"urn:ietf:params:jmap:contacts:parse", `{}`,
		"urn:ietf:params:jmap:filenode", `{}`,
		"urn:ietf:params:jmap:principals", `{}`,
	))
	want := All()
	want.MaxDelayedSend = 2592000
	if f != want {
		t.Fatalf("got %+v, want %+v", f, want)
	}
}

// TestOfParseNeedsBase 只公布 parse 而没有日历/通讯录本身时不算能导入。
func TestOfParseNeedsBase(t *testing.T) {
	f := Of(raw("urn:ietf:params:jmap:calendars:parse", `{}`, "urn:ietf:params:jmap:contacts:parse", `{}`))
	if f.CalendarParse || f.ContactParse {
		t.Fatalf("parse without base capability: %+v", f)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	caps := raw("urn:ietf:params:jmap:mail", `{}`, "urn:ietf:params:jmap:submission", `{"maxDelayedSend":60}`, "urn:ietf:params:jmap:filenode", `{}`)
	s, err := Encode(caps)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Decode(s), Of(caps); got != want {
		t.Fatalf("round trip: got %+v, want %+v", got, want)
	}
}

// TestDecodeLegacyArray 旧版存的是 URI 数组: 功能照认, 定时发送上限读不出为 0。
func TestDecodeLegacyArray(t *testing.T) {
	f := Decode(`["urn:ietf:params:jmap:mail","urn:ietf:params:jmap:submission","urn:ietf:params:jmap:calendars"]`)
	want := Features{Submission: true, Calendars: true}
	if f != want {
		t.Fatalf("got %+v, want %+v", f, want)
	}
}

// TestDecodeUnknown 没存过或读不出的能力当作全部支持, 不因为离线把功能藏掉。
func TestDecodeUnknown(t *testing.T) {
	for _, s := range []string{"", "[]", "{}", "null", "not json"} {
		if got := Decode(s); got != All() {
			t.Errorf("Decode(%q) = %+v, want All()", s, got)
		}
	}
}

func TestUnsupportedErrorCode(t *testing.T) {
	err := Unsupported(FeatureOf("urn:ietf:params:jmap:calendars"))
	if !strings.HasPrefix(err.Error(), "2280 ") || !strings.Contains(err.Error(), "calendars") {
		t.Fatalf("error = %q", err)
	}
	var u *UnsupportedError
	if !errors.As(err, &u) {
		t.Fatal("not an UnsupportedError")
	}
}
