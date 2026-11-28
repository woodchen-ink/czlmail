package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 标签的名字与颜色 (设置 → 标签)。
//
// 邮件上的标签就是 JMAP 的自定义关键字 (IMAP 的 keyword / flag), 存在服务器上, 别的客户端也看得到;
// 但关键字只能是可见 ASCII, 服务器 (如 Stalwart) 还会转成小写, 也没有地方存标签的显示名与颜色。
// 所以本程序给每个关键字另记一条「显示名 + 颜色」: 显示名可以有空格、汉字、带音调的字母, 关键字由名字生成
// (「Účetnictví 2026」→ ucetnictvi-2026)。改名只改显示名, 关键字不变, 已打标签的邮件不受影响。
//
// 名字与颜色不分账号: 同一个关键字在每个账号里都是同一个标签。跨设备同步与账户设置相同,
// 存成个人网盘的 CZL Mail/message-tags.json, 本机每条一行 app_settings (messageTag:<关键字>), 逐条"最后修改者胜",
// 删除写空值 (墓碑) 才能传到别的设备。删除只删名字与颜色, 邮件上的关键字照旧, 界面退回显示关键字本身。

const (
	messageTagsFileName = "message-tags.json"
	messageTagsFileType = "czlmail-message-tags"

	// messageTagPrefix 是本机 app_settings 里标签定义的键前缀, 后接关键字。
	messageTagPrefix = "messageTag:"

	// completedKeyword 是已完成的待办。待办的旗标本身用 $flagged, 见 SetTaskFlag。
	completedKeyword = "$completed"
)

// MessageTag 是一个标签的定义; Color 是 accountColors 里的键名 (与账号颜色同一套低饱和色)。
type MessageTag struct {
	Keyword string `json:"keyword"`
	Name    string `json:"name"`
	Color   string `json:"color,omitempty"`
}

type messageTagValue struct {
	Name  string `json:"name,omitempty"`
	Color string `json:"color,omitempty"`
}

// normalizeTagValue 去掉首尾空白, 名字最多 64 个字符, 不认识的颜色丢掉。
func normalizeTagValue(v messageTagValue) messageTagValue {
	v.Name = strings.Join(strings.Fields(v.Name), " ")
	if r := []rune(v.Name); len(r) > 64 {
		v.Name = string(r[:64])
	}
	if !slices.Contains(accountColors, v.Color) {
		v.Color = ""
	}
	return v
}

func encodeTagValue(v messageTagValue) string {
	if v.Name == "" && v.Color == "" {
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func decodeTagValue(s string) messageTagValue {
	var v messageTagValue
	if s != "" {
		_ = json.Unmarshal([]byte(s), &v)
	}
	return normalizeTagValue(v)
}

// hiddenLabels 是别的客户端不带 $ 的内部标记, 不能当标签用 (列表里也不显示, 见 store.labelKeywordSQL)。
var hiddenLabels = []string{"junk", "nonjunk", "notjunk"}

// tagKeyword 由标签名生成关键字: 去掉音调符号, 转小写, 空白与常见分隔符换成 "-",
// 其余 RFC 8621 不允许或不好看的字符丢掉。汉字等转不成 ASCII 的字被丢掉时, 后面接名字的短哈希
// (「工作」→ tag-<8 位>,「项目A」→ a-<8 位>): 否则不同的中文名都落到 tag、tag-2 上,
// 编号又只看本机已有的标签, 两台设备各自新建的标签会拿到同一个关键字。哈希只取决于名字, 各设备上同名得到同一个。
func tagKeyword(name string) string {
	var b strings.Builder
	dash, dropped := false, false
	for _, r := range norm.NFD.String(strings.ToLower(name)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue
		case r == 'ł':
			r = 'l'
		case r == 'ß':
			b.WriteString("ss")
			dash = false
			continue
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r == '-' || r == '_' || r == '.' || r == '/' || r == '+' || r == '&' || unicode.IsSpace(r):
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			dropped = true
		}
	}
	kw := strings.TrimRight(b.String(), "-")
	if dropped || kw == "" {
		if len(kw) > 39 {
			kw = strings.TrimRight(kw[:39], "-")
		}
		return cmp.Or(kw, "tag") + "-" + tagNameHash(name)
	}
	if len(kw) > 48 {
		kw = strings.TrimRight(kw[:48], "-")
	}
	if slices.Contains(hiddenLabels, kw) {
		kw = "tag-" + kw
	}
	return kw
}

// tagNameHash 是标签名 (不分大小写, 空白合并) 的 8 位十六进制短哈希。
func tagNameHash(name string) string {
	sum := sha256.Sum256([]byte(norm.NFC.String(strings.ToLower(strings.Join(strings.Fields(name), " ")))))
	return hex.EncodeToString(sum[:4])
}

// GetMessageTags 列出定义过名字或颜色的标签, 按名字排序。
func (a *App) GetMessageTags() ([]MessageTag, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	rows, err := st.TimedSettingsWithPrefix(a.ctx, messageTagPrefix)
	if err != nil {
		return nil, err
	}
	return messageTagList(rows), nil
}

func messageTagList(rows map[string]store.TimedSetting) []MessageTag {
	out := []MessageTag{}
	for kw, row := range rows {
		v := decodeTagValue(row.Value)
		if v.Name == "" && v.Color == "" {
			continue
		}
		out = append(out, MessageTag{Keyword: kw, Name: cmp.Or(v.Name, kw), Color: v.Color})
	}
	slices.SortFunc(out, func(x, y MessageTag) int {
		return cmp.Or(strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)), strings.Compare(x.Keyword, y.Keyword))
	})
	return out
}

// SaveMessageTag 新建或修改一个标签。Keyword 为空时是新建: 由名字生成关键字, 与已有标签重名
// (生成的关键字相同而名字不同) 时在后面加 -2、-3。返回保存后的标签。
// 名字与颜色都为空等于删除 (见 DeleteMessageTag)。
func (a *App) SaveMessageTag(tag MessageTag) (MessageTag, error) {
	st, err := a.currentStore()
	if err != nil {
		return MessageTag{}, err
	}
	v := normalizeTagValue(messageTagValue{Name: tag.Name, Color: tag.Color})
	kw := strings.ToLower(strings.TrimSpace(tag.Keyword))
	if kw == "" {
		if v.Name == "" {
			return MessageTag{}, fmt.Errorf("2033 label is empty")
		}
		rows, err := st.TimedSettingsWithPrefix(a.ctx, messageTagPrefix)
		if err != nil {
			return MessageTag{}, err
		}
		kw = uniqueTagKeyword(tagKeyword(v.Name), v.Name, rows)
	}
	if err := validateLabel(kw); err != nil {
		return MessageTag{}, err
	}
	if err := a.putMessageTag(st, kw, encodeTagValue(v)); err != nil {
		return MessageTag{}, err
	}
	return MessageTag{Keyword: kw, Name: cmp.Or(v.Name, kw), Color: v.Color}, nil
}

// uniqueTagKeyword 让新标签的关键字不与别的已定义标签相撞; 名字相同 (不分大小写) 的就是同一个标签。
// 撞上名字不同的标签 (「Práce」与「Prace」) 时先接名字的短哈希, 与 tagKeyword 一样各设备上结果相同;
// 哈希也撞上 (几乎不会) 才加序号。
func uniqueTagKeyword(base, name string, rows map[string]store.TimedSetting) string {
	hashed := base
	if !strings.HasSuffix(base, "-"+tagNameHash(name)) {
		hashed = base + "-" + tagNameHash(name)
	}
	for i := 0; ; i++ {
		kw := base
		if i == 1 {
			kw = hashed
		} else if i > 1 {
			kw = hashed + "-" + strconv.Itoa(i)
		}
		row, ok := rows[kw]
		if !ok || row.Value == "" {
			return kw
		}
		if strings.EqualFold(decodeTagValue(row.Value).Name, name) {
			return kw
		}
	}
}

// DeleteMessageTag 删除标签的名字与颜色; 邮件上的关键字不动, 界面改为显示关键字本身。
func (a *App) DeleteMessageTag(keyword string) error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	return a.putMessageTag(st, strings.ToLower(strings.TrimSpace(keyword)), "")
}

func (a *App) putMessageTag(st *store.Store, keyword, value string) error {
	if keyword == "" {
		return fmt.Errorf("2033 label is empty")
	}
	key := messageTagPrefix + keyword
	cur, err := st.StringSetting(a.ctx, key)
	switch {
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return err
	case err == nil && cur == value, errors.Is(err, store.ErrNotFound) && value == "":
		return nil
	}
	if err := st.SetStringSetting(a.ctx, key, value); err != nil {
		return err
	}
	a.emit(EventSettingsChanged)
	a.syncMessageTagsSoon()
	return nil
}

// SetTaskFlag 设置邮件的待办旗标 (同 Outlook 的后续标志, 不带日期): "flagged" 待办、"completed" 已完成、"none" 清除。
//
// 待办就是 $flagged: 别的客户端 (Outlook、Apple Mail、Thunderbird 的星标、Gmail 的星标) 都认它。
// 「已完成」没有标准关键字, 用 $completed, 同时去掉 $flagged —— 与 Outlook 经 IMAP 完成待办的效果一样,
// 别的客户端里旗标消失, 本程序里显示为对勾。
func (a *App) SetTaskFlag(accountID string, emailIDs []string, state string) error {
	var keywords map[string]bool
	switch state {
	case "flagged":
		keywords = map[string]bool{"$flagged": true, completedKeyword: false}
	case "completed":
		keywords = map[string]bool{"$flagged": false, completedKeyword: true}
	case "none":
		keywords = map[string]bool{"$flagged": false, completedKeyword: false}
	default:
		return fmt.Errorf("2167 unknown task flag state %q", state)
	}
	s, err := a.syncerFor(accountID)
	if err != nil {
		return err
	}
	return s.SetKeywords(a.ctx, accountID, emailIDs, keywords)
}

type messageTagsFile struct {
	Version    int                       `json:"version"`
	Type       string                    `json:"type"`
	ExportedAt string                    `json:"exportedAt"`
	Tags       map[string]messageTagFile `json:"tags"`
}

// messageTagFile 是文件里的一个标签, 键是关键字; 名字与颜色都空是墓碑。
type messageTagFile struct {
	Name      string `json:"name,omitempty"`
	Color     string `json:"color,omitempty"`
	UpdatedAt string `json:"updatedAt"`
}

// syncMessageTagsSoon 在后台同步标签定义, 失败只记日志。
func (a *App) syncMessageTagsSoon() {
	go func() {
		if err := a.SyncMessageTags(); errors.Is(err, errNoDriveSync) {
			a.log.Debug("sync message tags skipped", "err", err)
		} else if err != nil {
			a.log.Warn("sync message tags", "err", err)
		}
	}()
}

// SyncMessageTags 立即与个人网盘里的 message-tags.json 合并。
func (a *App) SyncMessageTags() error { return retryDriveSync(a.syncMessageTagsOnce) }

func (a *App) syncMessageTagsOnce() error {
	driveSyncMu.Lock()
	defer driveSyncMu.Unlock()

	st, err := a.currentStore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()

	file, data, err := a.openDriveSyncFile(ctx, messageTagsFileName)
	if err != nil {
		return err
	}
	remote := map[string]store.TimedSetting{}
	if data != nil {
		var f messageTagsFile
		if err := json.Unmarshal(data, &f); err != nil || f.Type != messageTagsFileType {
			return fmt.Errorf("2166 %s/%s is not a message tags file", driveSyncFolder, messageTagsFileName)
		}
		for k, e := range f.Tags {
			remote[k] = store.TimedSetting{
				Value:     encodeTagValue(normalizeTagValue(messageTagValue{Name: e.Name, Color: e.Color})),
				UpdatedAt: parseISO(e.UpdatedAt),
			}
		}
	}

	local, err := st.TimedSettingsWithPrefix(ctx, messageTagPrefix)
	if err != nil {
		return err
	}
	merged, changes, remoteChanged := mergeTimed(local, remote, nil)
	if len(changes) > 0 {
		if err := st.WithTx(ctx, func(tx *sql.Tx) error {
			for k, v := range changes {
				if err := store.PutTimedSetting(ctx, tx, messageTagPrefix+k, v); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		a.emit(EventSettingsChanged)
	}

	if !remoteChanged && file.exists() {
		return nil
	}
	out := messageTagsFile{
		Version: 1, Type: messageTagsFileType, ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Tags: make(map[string]messageTagFile, len(merged)),
	}
	for k, v := range merged {
		d := decodeTagValue(v.Value)
		out.Tags[k] = messageTagFile{Name: d.Name, Color: d.Color, UpdatedAt: isoTime(v.UpdatedAt)}
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return file.write(ctx, body)
}
