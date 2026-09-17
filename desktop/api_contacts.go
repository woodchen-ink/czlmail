package main

import (
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/pim"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 通讯录。表单字段与 Bulwark 的联系人表单对齐(RFC 9553 JSContact)。
// 保存时只替换表单管理的属性, 其余字段(服务端扩展、vCard 转来的未知属性)原样保留。

// ListAddressBooks 列出全部账号的通讯录。
func (a *App) ListAddressBooks() ([]store.AddressBook, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	return st.AddressBooks(a.ctx)
}

// maxContacts 是列表一次取回的上限。个人通讯录很少超过几千人, 前端再按字母分组。
const maxContacts = 5000

// ListContacts 列出联系人。accountID / bookID 为空表示全部。
func (a *App) ListContacts(accountID, bookID, query string) ([]store.ContactSummary, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	return st.Contacts(a.ctx, accountID, bookID, query, maxContacts)
}

// SearchRecipients 为写信的收件人输入框提供补全: 先通讯录, 再最近往来的地址, 按地址去重。
func (a *App) SearchRecipients(query string) ([]store.Recipient, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	contacts, err := st.SearchRecipients(a.ctx, query, 8)
	if err != nil {
		return nil, err
	}
	recent, err := st.SearchCorrespondents(a.ctx, query, 12)
	if err != nil {
		return contacts, nil
	}
	seen := map[string]bool{}
	out := make([]store.Recipient, 0, len(contacts)+len(recent))
	for _, list := range [][]store.Recipient{contacts, recent} {
		for _, r := range list {
			key := strings.ToLower(r.Email)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, r)
			if len(out) >= 12 {
				return out, nil
			}
		}
	}
	return out, nil
}

// ContactField 是邮箱/电话条目。Context 取 work / private / 空; Feature 只用于电话(mobile/voice/fax/...)。
type ContactField struct {
	Value   string `json:"value"`
	Context string `json:"context"`
	Feature string `json:"feature"`
	Label   string `json:"label"`
}

// ContactAddress 是结构化地址。
type ContactAddress struct {
	Street   string `json:"street"`
	Locality string `json:"locality"`
	Region   string `json:"region"`
	Postcode string `json:"postcode"`
	Country  string `json:"country"`
	Context  string `json:"context"`
	// Full 是服务器给的整段地址(没有分量时), 只读展示。
	Full string `json:"full"`
}

// ContactService 是在线服务(社交账号、网站等)。
type ContactService struct {
	Service string `json:"service"`
	URI     string `json:"uri"`
	Label   string `json:"label"`
}

// ContactDate 是纪念日。Date 为 YYYY-MM-DD, 不含年份时为 --MM-DD。
type ContactDate struct {
	Kind string `json:"kind"` // birth / death / wedding / 其它
	Date string `json:"date"`
}

// ContactInfo 是个人信息(专长、爱好、兴趣)。
type ContactInfo struct {
	Kind  string `json:"kind"` // expertise / hobby / interest
	Value string `json:"value"`
	Level string `json:"level"` // high / medium / low
}

// ContactForm 是联系人的完整可编辑形态, 读写共用。
type ContactForm struct {
	AccountID     string `json:"accountId"`
	ID            string `json:"id"`
	UID           string `json:"uid"`
	AddressBookID string `json:"addressBookId"`
	// Kind: individual / org / group。
	Kind string `json:"kind"`

	Prefix   string `json:"prefix"`
	Given    string `json:"given"`
	Middle   string `json:"middle"`
	Surname  string `json:"surname"`
	Suffix   string `json:"suffix"`
	Nickname string `json:"nickname"`

	Organization string `json:"organization"`
	Department   string `json:"department"`
	JobTitle     string `json:"jobTitle"`
	Role         string `json:"role"`

	Emails         []ContactField   `json:"emails"`
	Phones         []ContactField   `json:"phones"`
	Addresses      []ContactAddress `json:"addresses"`
	OnlineServices []ContactService `json:"onlineServices"`
	Anniversaries  []ContactDate    `json:"anniversaries"`
	PersonalInfo   []ContactInfo    `json:"personalInfo"`
	Keywords       []string         `json:"keywords"`
	Notes          string           `json:"notes"`

	// Photo 是 data URI 或空。读取时带 PhotoURL 供显示; 保存时 PhotoChanged 为真才改照片。
	Photo        string `json:"photo"`
	PhotoURL     string `json:"photoUrl"`
	PhotoChanged bool   `json:"photoChanged"`

	// MemberIDs 是群组成员的联系人 id(同账号)。服务器按 uid 存成员, 映射在后端完成。
	MemberIDs []string `json:"memberIds"`
	members   []string

	DisplayName string `json:"displayName"`
	ReadOnly    bool   `json:"readOnly"`
}

// GetContact 读联系人。
func (a *App) GetContact(accountID, id string) (*ContactForm, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	raw, err := st.Contact(a.ctx, accountID, id)
	if err != nil {
		return nil, err
	}
	f := contactFormFrom([]byte(raw))
	f.AccountID = accountID
	if len(f.members) > 0 {
		uids, _ := st.ContactUIDs(a.ctx, accountID)
		want := map[string]bool{}
		for _, u := range f.members {
			want[u] = true
		}
		for id, uid := range uids {
			if uid != "" && want[uid] {
				f.MemberIDs = append(f.MemberIDs, id)
			}
		}
		sort.Strings(f.MemberIDs)
	}

	books, _ := st.AddressBooks(a.ctx)
	for _, b := range books {
		if b.AccountID == accountID && b.ID == f.AddressBookID {
			f.ReadOnly = b.MyRights != nil && !b.MyRights["mayWrite"]
		}
	}
	if f.Photo != "" || strings.HasPrefix(f.PhotoURL, "blob:") {
		f.PhotoURL = ContactPhotoPath + "?account=" + accountID + "&id=" + id
	}
	return f, nil
}

// cardJSON 是解析表单用的宽松结构。
type cardJSON struct {
	ID             string          `json:"id"`
	UID            string          `json:"uid"`
	Kind           string          `json:"kind"`
	AddressBookIDs map[string]bool `json:"addressBookIds"`
	Name           *struct {
		Full       string `json:"full"`
		Components []struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"components"`
	} `json:"name"`
	Nicknames     map[string]struct{ Name string } `json:"nicknames"`
	Organizations map[string]struct {
		Name  string `json:"name"`
		Units []struct {
			Name string `json:"name"`
		} `json:"units"`
	} `json:"organizations"`
	Titles map[string]struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"titles"`
	Emails map[string]struct {
		Address  string          `json:"address"`
		Contexts map[string]bool `json:"contexts"`
		Label    string          `json:"label"`
		Pref     int             `json:"pref"`
	} `json:"emails"`
	Phones map[string]struct {
		Number   string          `json:"number"`
		Contexts map[string]bool `json:"contexts"`
		Features map[string]bool `json:"features"`
		Label    string          `json:"label"`
	} `json:"phones"`
	Addresses map[string]struct {
		Full       string `json:"full"`
		Components []struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"components"`
		Contexts map[string]bool `json:"contexts"`
	} `json:"addresses"`
	OnlineServices map[string]struct {
		Service string `json:"service"`
		URI     string `json:"uri"`
		User    string `json:"user"`
		Label   string `json:"label"`
	} `json:"onlineServices"`
	Links map[string]struct {
		URI   string `json:"uri"`
		Label string `json:"label"`
	} `json:"links"`
	Anniversaries map[string]struct {
		Kind string          `json:"kind"`
		Date json.RawMessage `json:"date"`
	} `json:"anniversaries"`
	PersonalInfo map[string]struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
		Level string `json:"level"`
	} `json:"personalInfo"`
	Keywords map[string]bool `json:"keywords"`
	Notes    map[string]struct {
		Note string `json:"note"`
	} `json:"notes"`
	Media map[string]struct {
		Kind      string `json:"kind"`
		URI       string `json:"uri"`
		BlobID    string `json:"blobId"`
		MediaType string `json:"mediaType"`
	} `json:"media"`
	Members map[string]bool `json:"members"`
}

func contactFormFrom(raw []byte) *ContactForm {
	var c cardJSON
	_ = json.Unmarshal(raw, &c)
	f := &ContactForm{ID: c.ID, UID: c.UID, Kind: c.Kind}
	if f.Kind == "" {
		f.Kind = "individual"
	}
	if card, err := pim.ParseCard(raw); err == nil {
		f.DisplayName = card.DisplayName()
		if ids := card.AddressBookIDList(); len(ids) > 0 {
			f.AddressBookID = ids[0]
		}
	}

	if c.Name != nil {
		for _, p := range c.Name.Components {
			switch p.Kind {
			case "title", "prefix":
				f.Prefix = p.Value
			case "given":
				f.Given = p.Value
			case "given2", "additional", "middle":
				f.Middle = p.Value
			case "surname", "surname2":
				f.Surname = strings.TrimSpace(f.Surname + " " + p.Value)
			case "generation", "suffix", "credential":
				f.Suffix = p.Value
			}
		}
		// 只有全名没有分量(常见于从其它客户端导入的卡片)时, 把全名放进"名", 保存时不丢名字。
		if f.Given == "" && f.Surname == "" && c.Name.Full != "" && f.Kind != "org" {
			f.Given = c.Name.Full
		}
	}
	for _, k := range sortedMapKeys(c.Nicknames) {
		if f.Nickname = c.Nicknames[k].Name; f.Nickname != "" {
			break
		}
	}
	for _, k := range sortedMapKeys(c.Organizations) {
		o := c.Organizations[k]
		f.Organization = o.Name
		if len(o.Units) > 0 {
			f.Department = o.Units[0].Name
		}
		break
	}
	for _, k := range sortedMapKeys(c.Titles) {
		t := c.Titles[k]
		if t.Kind == "role" {
			f.Role = t.Name
		} else if f.JobTitle == "" {
			f.JobTitle = t.Name
		}
	}
	for _, k := range sortedMapKeys(c.Emails) {
		e := c.Emails[k]
		if e.Address != "" {
			f.Emails = append(f.Emails, ContactField{Value: e.Address, Context: firstContext(e.Contexts), Label: e.Label})
		}
	}
	for _, k := range sortedMapKeys(c.Phones) {
		p := c.Phones[k]
		if p.Number != "" {
			f.Phones = append(f.Phones, ContactField{Value: p.Number, Context: firstContext(p.Contexts), Feature: firstKey(p.Features), Label: p.Label})
		}
	}
	for _, k := range sortedMapKeys(c.Addresses) {
		ad := c.Addresses[k]
		out := ContactAddress{Context: firstContext(ad.Contexts), Full: ad.Full}
		for _, comp := range ad.Components {
			switch comp.Kind {
			case "name", "number", "street", "direction", "building", "floor", "apartment", "room", "subdistrict", "district", "block":
				out.Street = strings.TrimSpace(out.Street + " " + comp.Value)
			case "locality":
				out.Locality = comp.Value
			case "region":
				out.Region = comp.Value
			case "postcode":
				out.Postcode = comp.Value
			case "country":
				out.Country = comp.Value
			}
		}
		f.Addresses = append(f.Addresses, out)
	}
	for _, k := range sortedMapKeys(c.OnlineServices) {
		s := c.OnlineServices[k]
		uri := s.URI
		if uri == "" {
			uri = s.User
		}
		if uri != "" {
			f.OnlineServices = append(f.OnlineServices, ContactService{Service: s.Service, URI: uri, Label: s.Label})
		}
	}
	for _, k := range sortedMapKeys(c.Links) {
		if l := c.Links[k]; l.URI != "" {
			f.OnlineServices = append(f.OnlineServices, ContactService{Service: "网站", URI: l.URI, Label: l.Label})
		}
	}
	for _, k := range sortedMapKeys(c.Anniversaries) {
		an := c.Anniversaries[k]
		if d := partialDate(an.Date); d != "" {
			f.Anniversaries = append(f.Anniversaries, ContactDate{Kind: an.Kind, Date: d})
		}
	}
	for _, k := range sortedMapKeys(c.PersonalInfo) {
		p := c.PersonalInfo[k]
		if p.Value != "" {
			f.PersonalInfo = append(f.PersonalInfo, ContactInfo{Kind: p.Kind, Value: p.Value, Level: p.Level})
		}
	}
	for k, on := range c.Keywords {
		if on {
			f.Keywords = append(f.Keywords, k)
		}
	}
	sort.Strings(f.Keywords)
	for _, k := range sortedMapKeys(c.Notes) {
		if n := c.Notes[k].Note; n != "" {
			f.Notes = n
			break
		}
	}
	for _, k := range sortedMapKeys(c.Media) {
		m := c.Media[k]
		if m.Kind != "photo" {
			continue
		}
		if strings.HasPrefix(m.URI, "data:") {
			f.Photo = m.URI
		} else if m.BlobID != "" {
			f.PhotoURL = "blob:" + m.BlobID
		}
		break
	}
	for uid, on := range c.Members {
		if on {
			f.members = append(f.members, uid)
		}
	}
	return f
}

func firstContext(m map[string]bool) string {
	for _, k := range []string{"work", "private"} {
		if m[k] {
			return k
		}
	}
	return ""
}

func firstKey(m map[string]bool) string {
	for _, k := range []string{"mobile", "voice", "fax", "pager", "text", "video", "textphone", "main-number"} {
		if m[k] {
			return k
		}
	}
	return ""
}

// partialDate 解析 JSContact 的纪念日日期: 字符串(YYYY-MM-DD)或 PartialDate 对象。
func partialDate(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if len(s) >= 10 {
			return s[:10]
		}
		return s
	}
	var pd struct {
		Year  int `json:"year"`
		Month int `json:"month"`
		Day   int `json:"day"`
		UTC   string
	}
	if json.Unmarshal(raw, &pd) != nil || pd.Month == 0 {
		var ts struct {
			UTC string `json:"utc"`
		}
		if json.Unmarshal(raw, &ts) == nil && len(ts.UTC) >= 10 {
			return ts.UTC[:10]
		}
		return ""
	}
	if pd.Year > 0 {
		return fmt.Sprintf("%04d-%02d-%02d", pd.Year, pd.Month, pd.Day)
	}
	return fmt.Sprintf("--%02d-%02d", pd.Month, pd.Day)
}

var (
	emailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	datePattern  = regexp.MustCompile(`^(\d{4}|-)-(\d{2})-(\d{2})$`)
)

// SaveContact 新建或修改联系人, 返回 id。
func (a *App) SaveContact(f ContactForm) (string, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return "", err
	}
	if f.Kind == "group" {
		if err := a.resolveMembers(&f); err != nil {
			return "", err
		}
	}
	props, err := contactProps(&f)
	if err != nil {
		return "", err
	}

	if f.ID == "" {
		// 服务器不会自动生成 uid; 没有 uid 的联系人无法被群组引用, 也会让 CardDAV 客户端报错。
		props["uid"] = "urn:uuid:" + newUUID()
		if f.AddressBookID == "" {
			return "", fmt.Errorf("2111 address book is required")
		}
		props["@type"] = "Card"
		props["version"] = "1.0"
		props["addressBookIds"] = map[string]bool{f.AddressBookID: true}
		clean := map[string]any{}
		for k, v := range props {
			if v != nil {
				clean[k] = v
			}
		}
		created, err := s.SetPIM(a.ctx, f.AccountID, jmapx.ContactCard, map[string]any{"new": clean}, nil, nil, nil)
		if err != nil {
			return "", err
		}
		return created["new"], nil
	}

	if f.AddressBookID != "" {
		props["addressBookIds"] = map[string]bool{f.AddressBookID: true}
	}
	if f.PhotoChanged {
		st, err := a.currentStore()
		if err != nil {
			return "", err
		}
		raw, err := st.Contact(a.ctx, f.AccountID, f.ID)
		if err != nil {
			return "", err
		}
		props["media"] = mergePhoto([]byte(raw), f.Photo)
	}
	_, err = s.SetPIM(a.ctx, f.AccountID, jmapx.ContactCard, nil, map[string]map[string]any{f.ID: props}, nil, nil)
	return f.ID, err
}

// contactProps 把表单转成 JSContact 属性。取值为 nil 的属性在更新时表示删除。
func contactProps(f *ContactForm) (map[string]any, error) {
	trim := strings.TrimSpace
	kind := f.Kind
	if kind == "" {
		kind = "individual"
	}
	org := trim(f.Organization)

	props := map[string]any{"kind": kind, "updated": time.Now().UTC().Format(time.RFC3339)}

	if kind == "group" {
		if trim(f.Given) == "" {
			return nil, fmt.Errorf("2110 group name is required")
		}
		props["name"] = map[string]any{"full": trim(f.Given)}
		members := map[string]bool{}
		for _, m := range f.members {
			if m = trim(m); m != "" {
				members[m] = true
			}
		}
		props["members"] = members
		return props, nil
	}

	var comps []map[string]string
	if kind != "org" {
		for _, c := range []struct{ kind, value string }{
			{"title", f.Prefix}, {"given", f.Given}, {"given2", f.Middle}, {"surname", f.Surname}, {"generation", f.Suffix},
		} {
			if v := trim(c.value); v != "" {
				comps = append(comps, map[string]string{"kind": c.kind, "value": v})
			}
		}
	}
	if kind == "org" && org == "" {
		return nil, fmt.Errorf("2110 organization name is required")
	}
	if kind != "org" && trim(f.Given) == "" && trim(f.Surname) == "" && org == "" {
		return nil, fmt.Errorf("2110 contact needs a name or organization")
	}
	if len(comps) > 0 {
		props["name"] = map[string]any{"components": comps, "isOrdered": true, "full": fullName(f)}
	} else {
		// 没有个人姓名时把组织名放进 name.full: vCard 的 FN 由它生成, 缺了会被严格的客户端丢弃。
		props["name"] = map[string]any{"full": org}
	}

	props["nicknames"] = nil
	if v := trim(f.Nickname); v != "" {
		props["nicknames"] = map[string]any{"n0": map[string]any{"name": v}}
	}

	emails := map[string]any{}
	for i, e := range f.Emails {
		v := trim(e.Value)
		if v == "" {
			continue
		}
		if !emailPattern.MatchString(v) {
			return nil, fmt.Errorf("2112 invalid email %q", v)
		}
		entry := map[string]any{"address": v}
		if e.Context != "" {
			entry["contexts"] = map[string]bool{e.Context: true}
		}
		if trim(e.Label) != "" {
			entry["label"] = trim(e.Label)
		}
		emails[fmt.Sprintf("e%d", i)] = entry
	}
	props["emails"] = nilIfEmpty(emails)

	phones := map[string]any{}
	for i, p := range f.Phones {
		v := trim(p.Value)
		if v == "" {
			continue
		}
		entry := map[string]any{"number": v}
		if p.Context != "" {
			entry["contexts"] = map[string]bool{p.Context: true}
		}
		if p.Feature != "" {
			entry["features"] = map[string]bool{p.Feature: true}
		}
		if trim(p.Label) != "" {
			entry["label"] = trim(p.Label)
		}
		phones[fmt.Sprintf("p%d", i)] = entry
	}
	props["phones"] = nilIfEmpty(phones)

	props["organizations"] = nil
	if org != "" {
		o := map[string]any{"name": org}
		if d := trim(f.Department); d != "" {
			o["units"] = []map[string]string{{"name": d}}
		}
		props["organizations"] = map[string]any{"o0": o}
	}
	titles := map[string]any{}
	if v := trim(f.JobTitle); v != "" {
		titles["t0"] = map[string]any{"name": v, "kind": "title"}
	}
	if v := trim(f.Role); v != "" {
		titles["t1"] = map[string]any{"name": v, "kind": "role"}
	}
	props["titles"] = nilIfEmpty(titles)

	addrs := map[string]any{}
	for i, ad := range f.Addresses {
		var comps []map[string]string
		for _, c := range []struct{ kind, value string }{
			{"name", ad.Street}, {"locality", ad.Locality}, {"region", ad.Region}, {"postcode", ad.Postcode}, {"country", ad.Country},
		} {
			if v := trim(c.value); v != "" {
				comps = append(comps, map[string]string{"kind": c.kind, "value": v})
			}
		}
		if len(comps) == 0 {
			if trim(ad.Full) == "" {
				continue
			}
			entry := map[string]any{"full": trim(ad.Full)}
			addrs[fmt.Sprintf("a%d", i)] = entry
			continue
		}
		entry := map[string]any{"components": comps, "isOrdered": true, "defaultSeparator": ", "}
		if ad.Context != "" {
			entry["contexts"] = map[string]bool{ad.Context: true}
		}
		addrs[fmt.Sprintf("a%d", i)] = entry
	}
	props["addresses"] = nilIfEmpty(addrs)

	services := map[string]any{}
	for i, sv := range f.OnlineServices {
		if trim(sv.URI) == "" {
			continue
		}
		entry := map[string]any{"uri": trim(sv.URI)}
		if trim(sv.Service) != "" {
			entry["service"] = trim(sv.Service)
		}
		if trim(sv.Label) != "" {
			entry["label"] = trim(sv.Label)
		}
		services[fmt.Sprintf("os%d", i)] = entry
	}
	props["onlineServices"] = nilIfEmpty(services)
	props["links"] = nil

	anns := map[string]any{}
	for i, an := range f.Anniversaries {
		m := datePattern.FindStringSubmatch(trim(an.Date))
		if m == nil {
			if trim(an.Date) != "" {
				return nil, fmt.Errorf("2113 invalid date %q", an.Date)
			}
			continue
		}
		date := map[string]any{"@type": "PartialDate", "month": atoi(m[2]), "day": atoi(m[3])}
		if m[1] != "-" {
			date["year"] = atoi(m[1])
		}
		k := an.Kind
		if k == "" {
			k = "birth"
		}
		anns[fmt.Sprintf("an%d", i)] = map[string]any{"kind": k, "date": date}
	}
	props["anniversaries"] = nilIfEmpty(anns)

	info := map[string]any{}
	for i, p := range f.PersonalInfo {
		if trim(p.Value) == "" {
			continue
		}
		entry := map[string]any{"kind": p.Kind, "value": trim(p.Value)}
		if p.Level != "" {
			entry["level"] = p.Level
		}
		info[fmt.Sprintf("pi%d", i)] = entry
	}
	props["personalInfo"] = nilIfEmpty(info)

	keywords := map[string]bool{}
	for _, k := range f.Keywords {
		if k = trim(k); k != "" {
			keywords[k] = true
		}
	}
	props["keywords"] = nil
	if len(keywords) > 0 {
		props["keywords"] = keywords
	}

	props["notes"] = nil
	if v := trim(f.Notes); v != "" {
		props["notes"] = map[string]any{"n0": map[string]any{"note": v}}
	}

	if f.ID == "" && f.Photo != "" {
		props["media"] = map[string]any{"photo": photoMedia(f.Photo)}
	}
	return props, nil
}

func fullName(f *ContactForm) string {
	var parts []string
	for _, v := range []string{f.Prefix, f.Surname, f.Middle, f.Given, f.Suffix} {
		if v = strings.TrimSpace(v); v != "" {
			parts = append(parts, v)
		}
	}
	joined := strings.Join(parts, "")
	for _, r := range joined {
		if r >= 0x2E80 {
			// 中文姓名: 姓在前, 不加空格。
			return joined
		}
	}
	parts = parts[:0]
	for _, v := range []string{f.Prefix, f.Given, f.Middle, f.Surname, f.Suffix} {
		if v = strings.TrimSpace(v); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " ")
}

func photoMedia(dataURI string) map[string]any {
	mediaType := "image/jpeg"
	if i := strings.Index(dataURI, ";"); strings.HasPrefix(dataURI, "data:") && i > 5 {
		mediaType = dataURI[5:i]
	}
	return map[string]any{"kind": "photo", "uri": dataURI, "mediaType": mediaType}
}

// mergePhoto 替换照片, 保留其它媒体(logo、声音等)。photo 为空表示删除照片。
func mergePhoto(raw []byte, photo string) any {
	var c struct {
		Media map[string]map[string]any `json:"media"`
	}
	_ = json.Unmarshal(raw, &c)
	media := map[string]any{}
	key := "photo"
	for k, m := range c.Media {
		if m["kind"] == "photo" {
			key = k
			continue
		}
		media[k] = m
	}
	if photo != "" {
		media[key] = photoMedia(photo)
	}
	if len(media) == 0 {
		return nil
	}
	return media
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

func nilIfEmpty(m map[string]any) any {
	if len(m) == 0 {
		return nil
	}
	return m
}

// resolveMembers 把成员 id 换成 uid; 缺 uid 的旧联系人先补上。
func (a *App) resolveMembers(f *ContactForm) error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	uids, err := st.ContactUIDs(a.ctx, f.AccountID)
	if err != nil {
		return err
	}
	f.members = nil
	fix := map[string]map[string]any{}
	for _, id := range f.MemberIDs {
		uid, ok := uids[id]
		if !ok {
			continue
		}
		if uid == "" {
			uid = "urn:uuid:" + newUUID()
			fix[id] = map[string]any{"uid": uid}
		}
		f.members = append(f.members, uid)
	}
	if len(fix) > 0 {
		if _, err := s.SetPIM(a.ctx, f.AccountID, jmapx.ContactCard, nil, fix, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = cryptorand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// DeleteContacts 删除联系人。
func (a *App) DeleteContacts(accountID string, ids []string) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	_, err = s.SetPIM(a.ctx, accountID, jmapx.ContactCard, nil, nil, ids, nil)
	return err
}

// MoveContacts 把联系人移到同账号的另一个通讯录。
func (a *App) MoveContacts(accountID string, ids []string, addressBookID string) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	update := map[string]map[string]any{}
	for _, id := range ids {
		update[id] = map[string]any{"addressBookIds": map[string]bool{addressBookID: true}}
	}
	_, err = s.SetPIM(a.ctx, accountID, jmapx.ContactCard, nil, update, nil, nil)
	return err
}

// SaveAddressBook 新建或重命名通讯录, 返回 id。
func (a *App) SaveAddressBook(accountID, id, name string) (string, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("2114 address book name is required")
	}
	if id == "" {
		created, err := s.SetPIM(a.ctx, accountID, jmapx.AddressBook,
			map[string]any{"book": map[string]any{"name": name, "isSubscribed": true}}, nil, nil, nil)
		if err != nil {
			return "", err
		}
		return created["book"], nil
	}
	_, err = s.SetPIM(a.ctx, accountID, jmapx.AddressBook, nil, map[string]map[string]any{id: {"name": name}}, nil, nil)
	return id, err
}

// DeleteAddressBook 删除通讯录及其中的联系人。
func (a *App) DeleteAddressBook(accountID, id string) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	_, err = s.SetPIM(a.ctx, accountID, jmapx.AddressBook, nil, nil, []string{id},
		map[string]any{"onDestroyRemoveContents": true})
	if err == nil {
		s.ResyncPIM(a.ctx, accountID, jmapx.ContactCard)
	}
	return err
}

// RecentEmailsWith 返回与联系人最近的往来邮件。
func (a *App) RecentEmailsWith(address string) ([]store.EmailWithAccount, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	return st.RecentEmailsWith(a.ctx, address, 10)
}

// UpcomingEventsWith 返回未来 90 天内含该邮箱参与人的日程。
func (a *App) UpcomingEventsWith(address string) ([]EventOccurrence, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	addr := store.NormalizeAddress(address)
	if addr == "" {
		return nil, nil
	}
	now := time.Now()
	all, err := a.expandEvents(st, nil, now, now.Add(90*24*time.Hour))
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	var out []EventOccurrence
	for _, o := range all {
		key := o.AccountID + "/" + o.EventID
		match, seen := ids[key]
		if !seen {
			raw, err := st.Event(a.ctx, o.AccountID, o.EventID)
			match = err == nil && strings.Contains(strings.ToLower(raw), addr)
			ids[key] = match
		}
		if match {
			out = append(out, o)
			if len(out) >= 5 {
				break
			}
		}
	}
	return out, nil
}

// ContactPhotoPath 是联系人照片的本地路径。照片可能是 data URI 或服务器 blob, 统一经此输出。
const ContactPhotoPath = "/czl-contact-photo"

func (a *App) serveContactPhoto(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	st, err := a.currentStore()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	raw, err := st.Contact(r.Context(), q.Get("account"), q.Get("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f := contactFormFrom([]byte(raw))

	var data []byte
	var ctype string
	switch {
	case strings.HasPrefix(f.Photo, "data:"):
		comma := strings.Index(f.Photo, ",")
		header := f.Photo[5:max(comma, 5)]
		if comma < 0 || !strings.HasSuffix(header, ";base64") {
			http.NotFound(w, r)
			return
		}
		ctype = strings.TrimSuffix(header, ";base64")
		if data, err = base64.StdEncoding.DecodeString(f.Photo[comma+1:]); err != nil {
			http.NotFound(w, r)
			return
		}
	case strings.HasPrefix(f.PhotoURL, "blob:"):
		s, err := a.currentSyncer()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		rc, err := s.DownloadBlob(r.Context(), q.Get("account"), strings.TrimPrefix(f.PhotoURL, "blob:"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer rc.Close()
		buf := make([]byte, 0, 64<<10)
		tmp := make([]byte, 32<<10)
		for len(buf) < 5<<20 {
			n, err := rc.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if err != nil {
				break
			}
		}
		data = buf
		ctype = http.DetectContentType(data)
	default:
		http.NotFound(w, r)
		return
	}
	// 只输出位图: 这是与应用同源的路径, SVG 之类能带脚本的类型一律拒绝。
	switch http.DetectContentType(data) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp":
	default:
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	h.Set("Cache-Control", "private, max-age=60")
	_, _ = w.Write(data)
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
