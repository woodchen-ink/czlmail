package pim

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Card 是 JSContact Card 中本客户端展示与检索用到的字段。编辑时对原始 JSON 打补丁,
// 不从这个结构体整体回写, 避免丢掉不认识的字段(照片、生日、社交账号等)。
type Card struct {
	ID             string                           `json:"id"`
	UID            string                           `json:"uid"`
	Kind           string                           `json:"kind"`
	AddressBookIDs map[string]bool                  `json:"addressBookIds"`
	Name           *cardName                        `json:"name"`
	Nicknames      map[string]struct{ Name string } `json:"nicknames"`
	Emails         map[string]cardEmail             `json:"emails"`
	Phones         map[string]cardPhone             `json:"phones"`
	Organizations  map[string]struct{ Name string } `json:"organizations"`
	Titles         map[string]struct{ Name string } `json:"titles"`
	Updated        string                           `json:"updated"`
	Media          map[string]json.RawMessage       `json:"media"`
}

type cardName struct {
	Full       string `json:"full"`
	Components []struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"components"`
}

type cardEmail struct {
	Address  string          `json:"address"`
	Contexts map[string]bool `json:"contexts"`
	Pref     int             `json:"pref"`
	Label    string          `json:"label"`
}

type cardPhone struct {
	Number   string          `json:"number"`
	Features map[string]bool `json:"features"`
	Contexts map[string]bool `json:"contexts"`
	Label    string          `json:"label"`
}

// ContactEmail 是展示用的邮箱条目。
type ContactEmail struct {
	Key     string `json:"key"`
	Address string `json:"address"`
	Label   string `json:"label"`
}

// ContactPhone 是展示用的电话条目。
type ContactPhone struct {
	Key    string `json:"key"`
	Number string `json:"number"`
	Label  string `json:"label"`
}

// ParseCard 解析联系人。
func ParseCard(raw []byte) (*Card, error) {
	var c Card
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("decode card: %w", err)
	}
	return &c, nil
}

// DisplayName 按 全名 → 姓名组件 → 昵称 → 组织 → 邮箱 的顺序取第一个非空值。
func (c *Card) DisplayName() string {
	if c.Name != nil {
		if s := strings.TrimSpace(c.Name.Full); s != "" {
			return s
		}
		var given, surname []string
		for _, p := range c.Name.Components {
			switch p.Kind {
			case "given", "given2":
				given = append(given, p.Value)
			case "surname", "surname2":
				surname = append(surname, p.Value)
			}
		}
		if s := strings.TrimSpace(joinName(surname, given)); s != "" {
			return s
		}
	}
	for _, k := range sortedKeys(c.Nicknames) {
		if s := strings.TrimSpace(c.Nicknames[k].Name); s != "" {
			return s
		}
	}
	if s := c.Organization(); s != "" {
		return s
	}
	if e := c.EmailList(); len(e) > 0 {
		return e[0].Address
	}
	return ""
}

// joinName 中文名姓在前且不加空格, 西文名名在前以空格分隔。
func joinName(surname, given []string) string {
	all := strings.Join(append(append([]string{}, surname...), given...), "")
	if isCJK(all) {
		return all
	}
	return strings.TrimSpace(strings.Join(given, " ") + " " + strings.Join(surname, " "))
}

func isCJK(s string) bool {
	for _, r := range s {
		if r >= 0x2E80 {
			return true
		}
	}
	return false
}

// Organization 返回第一个组织名。
func (c *Card) Organization() string {
	for _, k := range sortedKeys(c.Organizations) {
		if s := strings.TrimSpace(c.Organizations[k].Name); s != "" {
			return s
		}
	}
	return ""
}

// Title 返回第一个职位。
func (c *Card) Title() string {
	for _, k := range sortedKeys(c.Titles) {
		if s := strings.TrimSpace(c.Titles[k].Name); s != "" {
			return s
		}
	}
	return ""
}

// EmailList 按偏好排序返回邮箱; pref 越小越优先, 未设置的排最后。
func (c *Card) EmailList() []ContactEmail {
	keys := sortedKeys(c.Emails)
	sort.SliceStable(keys, func(i, j int) bool {
		return prefRank(c.Emails[keys[i]].Pref, c.Emails[keys[i]].Contexts) < prefRank(c.Emails[keys[j]].Pref, c.Emails[keys[j]].Contexts)
	})
	out := make([]ContactEmail, 0, len(keys))
	for _, k := range keys {
		e := c.Emails[k]
		if strings.TrimSpace(e.Address) == "" {
			continue
		}
		out = append(out, ContactEmail{Key: k, Address: strings.TrimSpace(e.Address), Label: contextLabel(e.Label, e.Contexts)})
	}
	return out
}

// PhoneList 返回电话。
func (c *Card) PhoneList() []ContactPhone {
	out := make([]ContactPhone, 0, len(c.Phones))
	for _, k := range sortedKeys(c.Phones) {
		p := c.Phones[k]
		if strings.TrimSpace(p.Number) == "" {
			continue
		}
		out = append(out, ContactPhone{Key: k, Number: strings.TrimSpace(p.Number), Label: contextLabel(p.Label, p.Contexts)})
	}
	return out
}

func prefRank(pref int, contexts map[string]bool) int {
	if pref > 0 {
		return pref
	}
	// vCard 转来的 Card 常把首选写成 contexts.pref。
	if contexts["pref"] {
		return 0
	}
	return 1000
}

func contextLabel(label string, contexts map[string]bool) string {
	if label != "" {
		return label
	}
	switch {
	case contexts["work"]:
		return "工作"
	case contexts["private"]:
		return "私人"
	}
	return ""
}

// SortName 用于列表排序, 统一小写。
func (c *Card) SortName() string {
	return strings.ToLower(c.DisplayName())
}

// AddressBookIDList 返回所属通讯录 id。
func (c *Card) AddressBookIDList() []string {
	out := make([]string, 0, len(c.AddressBookIDs))
	for id, on := range c.AddressBookIDs {
		if on {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
