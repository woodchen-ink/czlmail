package main

import (
	"encoding/json"
	"fmt"
)

// contactOrig 是保存联系人时服务器上的原卡片 (取自本机缓存)。
//
// 编辑器只管理 JSContact 的一部分字段: 邮箱只认一个 context、电话只认一个 feature、地址把街道类分量合成一栏,
// pref、kind、mediaType、countryCode 等一概不管。整体重写会把这些连同条目原有的 key 一起丢掉,
// 所以写回时以原卡片为底: 编辑器里没改的属性不发, 改过的列表按原 key 写回, 条目上只改用户动过的字段。
type contactOrig struct {
	form    *ContactForm
	entries map[string]map[string]map[string]any // 属性名 → 条目 key → 条目
}

// newContactOrig 解析原卡片; 解析不了时返回 nil, 保存退回整体重写。
func newContactOrig(raw []byte) *contactOrig {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return nil
	}
	o := &contactOrig{form: contactFormFrom(raw), entries: map[string]map[string]map[string]any{}}
	for _, name := range []string{"emails", "phones", "addresses", "onlineServices", "links", "anniversaries", "personalInfo"} {
		var m map[string]map[string]any
		if v, ok := top[name]; ok && json.Unmarshal(v, &m) == nil && m != nil {
			o.entries[name] = m
		}
	}
	return o
}

// contactEntries 为一个列表属性分配条目: 沿用原 key 时以原条目为底, 新条目取不冲突的新 key。
type contactEntries struct {
	orig   map[string]map[string]any
	prefix string
	out    map[string]any
}

func (o *contactOrig) list(name, prefix string) *contactEntries {
	l := &contactEntries{prefix: prefix, out: map[string]any{}}
	if o != nil {
		l.orig = o.entries[name]
	}
	return l
}

// entry 返回条目 (以原条目的副本为底) 与 fresh: fresh 为真表示新条目, 所有字段都要写。
func (l *contactEntries) entry(key string, i int) (map[string]any, bool) {
	if src, ok := l.orig[key]; ok && key != "" {
		if _, taken := l.out[key]; !taken {
			e := make(map[string]any, len(src))
			for k, v := range src {
				e[k] = v
			}
			l.out[key] = e
			return e, false
		}
	}
	key = fmt.Sprintf("%s%d", l.prefix, i)
	for n := 1; l.taken(key); n++ {
		key = fmt.Sprintf("%s%d_%d", l.prefix, i, n)
	}
	e := map[string]any{}
	l.out[key] = e
	return e, true
}

func (l *contactEntries) taken(key string) bool {
	_, used := l.out[key]
	_, orig := l.orig[key]
	return used || orig
}

// put 在 fresh 或字段被改动时写入字段; value 为 nil 表示去掉该字段。
func put(e map[string]any, fresh, changed bool, field string, value any) {
	if !fresh && !changed {
		return
	}
	if s, ok := value.(string); value == nil || ok && s == "" {
		delete(e, field)
		return
	}
	e[field] = value
}

// origField / origAddress / … 按 key 取原卡片在编辑器里的样子, 用来判断用户改了哪些字段。
func (o *contactOrig) origField(list func(*ContactForm) []ContactField, key string) (ContactField, bool) {
	if o == nil || key == "" {
		return ContactField{}, false
	}
	for _, v := range list(o.form) {
		if v.Key == key {
			return v, true
		}
	}
	return ContactField{}, false
}

func (o *contactOrig) origAddress(key string) (ContactAddress, bool) {
	if o != nil && key != "" {
		for _, v := range o.form.Addresses {
			if v.Key == key {
				return v, true
			}
		}
	}
	return ContactAddress{}, false
}

func (o *contactOrig) origService(key string, link bool) (ContactService, bool) {
	if o != nil && key != "" {
		for _, v := range o.form.OnlineServices {
			if v.Key == key && v.Link == link {
				return v, true
			}
		}
	}
	return ContactService{}, false
}

func (o *contactOrig) origDate(key string) (ContactDate, bool) {
	if o != nil && key != "" {
		for _, v := range o.form.Anniversaries {
			if v.Key == key {
				return v, true
			}
		}
	}
	return ContactDate{}, false
}

func (o *contactOrig) origInfo(key string) (ContactInfo, bool) {
	if o != nil && key != "" {
		for _, v := range o.form.PersonalInfo {
			if v.Key == key {
				return v, true
			}
		}
	}
	return ContactInfo{}, false
}

// dropUnchanged 去掉编辑器里没改过的属性, 让服务器上的原值 (含编辑器不认识的部分) 原样保留。
// 判断方式: 把原卡片与修改后的表单各自按整体重写生成一遍, 结果相同就说明用户没动这个属性。
func dropUnchanged(props map[string]any, f *ContactForm, o *contactOrig) {
	if o == nil {
		return
	}
	before, err := contactProps(o.form, nil)
	if err != nil {
		return
	}
	after, err := contactProps(f, nil)
	if err != nil {
		return
	}
	for k := range props {
		if k == "updated" {
			continue
		}
		if a, b := jsonOf(after[k]), jsonOf(before[k]); a != "" && a == b {
			delete(props, k)
		}
	}
}

func jsonOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// contextMap 把编辑器里的单个取值写成 JSContact 的集合 ({"work": true}); 空值返回 nil, 表示去掉该字段。
func contextMap(v string) any {
	if v == "" {
		return nil
	}
	return map[string]bool{v: true}
}
