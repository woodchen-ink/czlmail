// Package i18n 让 Go 端与界面共用同一套语言文件 (desktop/frontend/lib/i18n/<id>.json)。
//
// 规则与界面的 lib/i18n 相同: 中文是源语言, 原文即 key; 译文里的 {0} {1} 按参数顺序替换, 插入的值不再解释;
// 查不到的原文原样返回。支持哪些语言由目录里有哪些 json 决定, 加语言不用改 Go 代码。
package i18n

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Source 是源语言 (中文) 的 id, 不需要词典。
const Source = "zh-CN"

// Catalog 是加载好的全部词典, 只读, 可并发使用。
type Catalog struct {
	messages map[string]map[string]string
}

// Load 读取 fsys 根目录下的 *.json 词典, 文件名 (去掉 .json) 即语言 id。
func Load(fsys fs.FS) (*Catalog, error) {
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, err
	}
	c := &Catalog{messages: map[string]map[string]string{}}
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		var dict map[string]string
		if err := json.Unmarshal(data, &dict); err != nil {
			return nil, fmt.Errorf("i18n %s: %w", name, err)
		}
		c.messages[strings.TrimSuffix(path.Base(name), ".json")] = dict
	}
	return c, nil
}

// Has 报告是否支持该语言 (源语言总是支持)。
func (c *Catalog) Has(locale string) bool {
	if locale == Source {
		return true
	}
	_, ok := c.messages[locale]
	return ok
}

// Locales 列出支持的语言 id, 源语言在内。
func (c *Catalog) Locales() []string {
	out := []string{Source}
	for id := range c.messages {
		out = append(out, id)
	}
	return out
}

// Lookup 返回原文在该语言下的译文; 没有时 ok 为假。
func (c *Catalog) Lookup(locale, source string) (string, bool) {
	v, ok := c.messages[locale][source]
	return v, ok
}

var placeholder = regexp.MustCompile(`\{(\d+)\}`)

// T 翻译原文并替换占位符。源语言或查不到时用原文本身作模板。
func (c *Catalog) T(locale, source string, values ...any) string {
	template := source
	if v, ok := c.messages[locale][source]; ok {
		template = v
	}
	return placeholder.ReplaceAllStringFunc(template, func(m string) string {
		i, _ := strconv.Atoi(m[1 : len(m)-1])
		if i < len(values) {
			return fmt.Sprint(values[i])
		}
		return m
	})
}
