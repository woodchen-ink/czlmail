// Package capability 把账号公布的 JMAP 能力归纳成界面与同步关心的功能开关。
//
// 只看账号级能力 (Session.accounts[].accountCapabilities): go-jmap 解析会话时丢掉它不认识的会话级能力,
// 日历、通讯录、网盘、Sieve 在 Session.Capabilities 里永远查不到。只认服务器公布的能力, 不按服务器品牌或域名猜。
package capability

import (
	"encoding/json"
	"fmt"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
)

// 能力 URI。日历、通讯录、网盘等与 jmapx 共用同一组常量。
const (
	MailURI       jmap.URI = "urn:ietf:params:jmap:mail"
	SubmissionURI jmap.URI = "urn:ietf:params:jmap:submission"
	VacationURI   jmap.URI = "urn:ietf:params:jmap:vacationresponse"
)

// Features 是一个账号能用的功能。
//
// 已读回执与 S/MIME 不在这里: 回执是一封普通邮件 (依赖 Submission), 签名与验签在本机完成, 都不需要服务器另外支持。
type Features struct {
	// Submission 能发信 (含已读回执); 没有时也没有发件身份。
	Submission bool `json:"submission"`
	// MaxDelayedSend 是允许的最长定时发送秒数, 0 表示不支持定时发送。
	MaxDelayedSend int  `json:"maxDelayedSend"`
	Calendars      bool `json:"calendars"`
	Contacts       bool `json:"contacts"`
	Files          bool `json:"files"`
	Sieve          bool `json:"sieve"`
	Vacation       bool `json:"vacation"`
	// Directory 是同邮局成员目录 (Principals)。
	Directory bool `json:"directory"`
	// CalendarParse / ContactParse 是导入 .ics / .vcf 与解析邮件里日历邀请用的服务端解析。
	CalendarParse bool `json:"calendarParse"`
	ContactParse  bool `json:"contactParse"`
}

// All 是能力未知 (从没连上过这个账号) 时的取值: 全部当作支持, 不因为离线把功能藏掉。
func All() Features {
	return Features{Submission: true, Calendars: true, Contacts: true, Files: true, Sieve: true, Vacation: true,
		Directory: true, CalendarParse: true, ContactParse: true}
}

// Of 从账号的原始能力表推出功能。
func Of(caps map[jmap.URI]json.RawMessage) Features {
	has := func(uri jmap.URI) bool { _, ok := caps[uri]; return ok }
	f := Features{
		Submission:    has(SubmissionURI),
		Calendars:     has(jmapx.CalendarsURI),
		Contacts:      has(jmapx.ContactsURI),
		Files:         has(jmapx.FileNodeURI),
		Sieve:         has(jmapx.SieveURI),
		Vacation:      has(VacationURI),
		Directory:     has(jmapx.PrincipalsURI),
		CalendarParse: has(jmapx.CalendarsURI) && has(jmapx.CalendarsParseURI),
		ContactParse:  has(jmapx.ContactsURI) && has(jmapx.ContactsParseURI),
	}
	if f.Submission {
		f.MaxDelayedSend = MaxDelayedSend(caps[SubmissionURI])
	}
	return f
}

// MaxDelayedSend 从 submission 能力对象 (RFC 8621 §1.3.2) 读出最长定时发送秒数, 读不出时为 0。
func MaxDelayedSend(raw json.RawMessage) int {
	var c struct {
		MaxDelayedSend int `json:"maxDelayedSend"`
	}
	if json.Unmarshal(raw, &c) != nil || c.MaxDelayedSend < 0 {
		return 0
	}
	return c.MaxDelayedSend
}

// Encode 把原始能力表编码后存进缓存库, 离线时据此还原上次已知的功能。
func Encode(caps map[jmap.URI]json.RawMessage) (string, error) {
	if caps == nil {
		caps = map[jmap.URI]json.RawMessage{}
	}
	b, err := json.Marshal(caps)
	return string(b), err
}

// Decode 还原缓存库里存的能力。
//
// 认两种写法: 现在的 URI → 能力对象, 与旧版只存的 URI 数组 (没有能力对象, 定时发送上限读不出, 连上后即被重写)。
// 空值或读不出时返回 All: 一个真实会话里的账号至少有邮件能力, 什么都没有只说明不知道。
func Decode(stored string) Features {
	var caps map[jmap.URI]json.RawMessage
	if json.Unmarshal([]byte(stored), &caps) != nil {
		var list []jmap.URI
		if json.Unmarshal([]byte(stored), &list) != nil {
			return All()
		}
		caps = make(map[jmap.URI]json.RawMessage, len(list))
		for _, uri := range list {
			caps[uri] = json.RawMessage(`{}`)
		}
	}
	if len(caps) == 0 {
		return All()
	}
	return Of(caps)
}

// 功能名, 用在报错里。
const (
	FeatureSubmission = "sending mail"
	FeatureCalendars  = "calendars"
	FeatureContacts   = "contacts"
	FeatureFiles      = "file storage"
	FeatureSieve      = "mail filters"
	FeatureVacation   = "vacation replies"
	FeatureParse      = "importing calendar or contact files"
)

// FeatureOf 返回某能力对应的功能名。
func FeatureOf(uri jmap.URI) string {
	switch uri {
	case SubmissionURI:
		return FeatureSubmission
	case jmapx.CalendarsURI:
		return FeatureCalendars
	case jmapx.ContactsURI:
		return FeatureContacts
	case jmapx.FileNodeURI:
		return FeatureFiles
	case jmapx.SieveURI:
		return FeatureSieve
	case VacationURI:
		return FeatureVacation
	case jmapx.CalendarsParseURI, jmapx.ContactsParseURI:
		return FeatureParse
	}
	return string(uri)
}

// UnsupportedError 表示账号所在的服务器没有公布这项功能需要的能力。
type UnsupportedError struct {
	Feature string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("2280 the mail server of this account does not support %s", e.Feature)
}

// Unsupported 构造 UnsupportedError。
func Unsupported(feature string) error {
	return &UnsupportedError{Feature: feature}
}
