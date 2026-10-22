package notes

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	weekdaysZH      = [7]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	weekdaysShortZH = [7]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	templateTag     = regexp.MustCompile(`\{\{\s*(date|time|title)\s*(?::([^}]*))?\}\}`)
)

// FormatDate expands Obsidian (Moment.js) date tokens: YYYY YY MM M DD D dddd ddd HH H mm ss.
// Text inside [brackets] is literal. lang "zh" names weekdays in Chinese, anything else in English.
func FormatDate(layout string, t time.Time, lang string) string {
	var b strings.Builder
	zh := strings.HasPrefix(lang, "zh")
	for i := 0; i < len(layout); {
		if layout[i] == '[' {
			if end := strings.IndexByte(layout[i:], ']'); end > 0 {
				b.WriteString(layout[i+1 : i+end])
				i += end + 1
				continue
			}
		}
		token, value := "", ""
		for _, candidate := range []string{"YYYY", "dddd", "ddd", "YY", "MM", "DD", "HH", "mm", "ss", "M", "D", "H"} {
			if strings.HasPrefix(layout[i:], candidate) {
				token = candidate
				break
			}
		}
		switch token {
		case "YYYY":
			value = fmt.Sprintf("%04d", t.Year())
		case "YY":
			value = fmt.Sprintf("%02d", t.Year()%100)
		case "MM":
			value = fmt.Sprintf("%02d", int(t.Month()))
		case "M":
			value = fmt.Sprint(int(t.Month()))
		case "DD":
			value = fmt.Sprintf("%02d", t.Day())
		case "D":
			value = fmt.Sprint(t.Day())
		case "dddd":
			value = t.Weekday().String()
			if zh {
				value = weekdaysZH[t.Weekday()]
			}
		case "ddd":
			value = t.Weekday().String()[:3]
			if zh {
				value = weekdaysShortZH[t.Weekday()]
			}
		case "HH":
			value = fmt.Sprintf("%02d", t.Hour())
		case "H":
			value = fmt.Sprint(t.Hour())
		case "mm":
			value = fmt.Sprintf("%02d", t.Minute())
		case "ss":
			value = fmt.Sprintf("%02d", t.Second())
		}
		if token == "" {
			_, size := utf8.DecodeRuneInString(layout[i:])
			b.WriteString(layout[i : i+size])
			i += size
			continue
		}
		b.WriteString(value)
		i += len(token)
	}
	return b.String()
}

// ExpandTemplate fills Obsidian core-template tags: {{date}}, {{time}}, {{title}}, {{date:FORMAT}}, {{time:FORMAT}}.
// Unknown text, including other {{tags}}, is kept verbatim.
func ExpandTemplate(template string, t time.Time, title, lang string) string {
	return templateTag.ReplaceAllStringFunc(template, func(tag string) string {
		parts := templateTag.FindStringSubmatch(tag)
		layout := strings.TrimSpace(parts[2])
		switch parts[1] {
		case "title":
			return title
		case "time":
			if layout == "" {
				layout = "HH:mm"
			}
		default:
			if layout == "" {
				layout = "YYYY-MM-DD"
			}
		}
		return FormatDate(layout, t, lang)
	})
}

// EnsureNote opens folders/name, creating missing folders and then the note with content.
// An existing note is returned untouched, so a second click on "today" never overwrites the diary.
func (s *Service) EnsureNote(vault string, folders []string, name, content string) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled {
		return Entry{}, false, ErrDisabled
	}
	if _, ok := s.vaults[vault]; !ok {
		return Entry{}, false, ErrNotFound
	}
	if !strings.HasSuffix(strings.ToLower(name), ".md") {
		name += ".md"
	}
	if !validName(name) || len(content) > MaxText || !utf8.ValidString(content) {
		return Entry{}, false, ErrInvalid
	}
	parent := ""
	for _, folder := range folders {
		if !validName(folder) {
			return Entry{}, false, ErrInvalid
		}
		existing, ok := s.liveChild(vault, parent, folder)
		if ok && !existing.Directory {
			return Entry{}, false, ErrInvalid
		}
		if !ok {
			existing = Entry{ID: newID(), VaultID: vault, ParentID: parent, Name: folder, Directory: true, Status: "pending"}
			stamp(&existing)
			if err := s.commit(existing, "", ""); err != nil {
				return Entry{}, false, err
			}
		}
		parent = existing.ID
	}
	if existing, ok := s.liveChild(vault, parent, name); ok {
		if existing.Directory || existing.Attachment {
			return Entry{}, false, ErrInvalid
		}
		existing = clone(existing)
		existing.Path = s.folderPath(existing)
		return existing, false, nil
	}
	e := Entry{ID: newID(), VaultID: vault, ParentID: parent, Name: name, Content: content, Status: "pending", Type: "text/markdown"}
	stamp(&e)
	if err := s.commit(e, "", ""); err != nil {
		return Entry{}, false, err
	}
	e = clone(e)
	e.Path = s.folderPath(e)
	return e, true, nil
}

// ReadPath reads the live note at a root-relative path such as "模板/日记.md".
func (s *Service) ReadPath(vault, path string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled {
		return Entry{}, ErrDisabled
	}
	parent := ""
	var found Entry
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		e, ok := s.liveChild(vault, parent, part)
		if !ok {
			return Entry{}, ErrNotFound
		}
		found, parent = e, e.ID
	}
	if found.ID == "" || found.Directory || found.Attachment {
		return Entry{}, ErrNotFound
	}
	return clone(found), nil
}

// liveChild finds a non-trashed child by case-insensitive name; callers hold s.mu.
func (s *Service) liveChild(vault, parent, name string) (Entry, bool) {
	for _, e := range s.entries {
		if e.VaultID == vault && e.ParentID == parent && !e.Deleted && strings.EqualFold(e.Name, name) {
			return e, true
		}
	}
	return Entry{}, false
}

// Touched lists live Markdown notes created or modified in [from, to) (Unix ms), newest first.
func (s *Service) Touched(vault string, from, to int64) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled {
		return nil, ErrDisabled
	}
	out := []Entry{}
	for _, e := range s.entries {
		if e.VaultID != vault || e.Directory || e.Attachment || s.effectivelyDeleted(e) {
			continue
		}
		if e.Created >= from && e.Created < to || e.Modified >= from && e.Modified < to {
			e = clone(e)
			e.Content, e.RemoteContent = "", ""
			e.Path = s.folderPath(e)
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified > out[j].Modified })
	return out, nil
}
