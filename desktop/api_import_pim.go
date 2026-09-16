package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"git.sr.ht/~rockorager/go-jmap"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
)

// 导入 .ics / .vcf。解析交给服务端的 T/parse(能力 calendars:parse / contacts:parse),
// 客户端不自带 iCalendar/vCard 解析器: 两种格式的边角情况太多, 服务端已经实现过一遍。

// maxImportFileBytes 限制单个导入文件, 防止误选一个巨大文件卡住上传。
const maxImportFileBytes = 20 << 20

// ImportCalendarFile 选择 .ics 导入到日历, 返回导入的事件数。
func (a *App) ImportCalendarFile(accountID, calendarID string) (int, error) {
	return a.importPIMFile(accountID, jmapx.CalendarEvent, "导入日历", "*.ics;*.ical;*.ifb",
		func(obj map[string]any) { obj["calendarIds"] = map[string]bool{calendarID: true} })
}

// ImportContactsFile 选择 .vcf 导入到通讯录, 返回导入的联系人数。
func (a *App) ImportContactsFile(accountID, addressBookID string) (int, error) {
	return a.importPIMFile(accountID, jmapx.ContactCard, "导入联系人", "*.vcf;*.vcard",
		func(obj map[string]any) { obj["addressBookIds"] = map[string]bool{addressBookID: true} })
}

func (a *App) importPIMFile(accountID, typeName, title, pattern string, assign func(map[string]any)) (int, error) {
	path, err := wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title:   title,
		Filters: []wruntime.FileFilter{{DisplayName: title, Pattern: pattern}},
	})
	if err != nil || path == "" {
		return 0, err
	}
	data, err := readImportSource(path)
	if err != nil {
		return 0, err
	}
	return a.importPIMData(accountID, typeName, data, assign)
}

// ImportCalendarSource 从本地 .ics 路径或 https 地址(webcal 订阅)导入到日历。
func (a *App) ImportCalendarSource(accountID, calendarID, source string) (int, error) {
	data, err := readImportSource(source)
	if err != nil {
		return 0, err
	}
	return a.importPIMData(accountID, jmapx.CalendarEvent, data,
		func(obj map[string]any) { obj["calendarIds"] = map[string]bool{calendarID: true} })
}

func readImportSource(source string) ([]byte, error) {
	var r io.Reader
	if strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "http://") {
		resp, err := http.Get(source)
		if err != nil {
			return nil, fmt.Errorf("2135 download calendar: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("2135 download calendar: HTTP %d", resp.StatusCode)
		}
		r = resp.Body
	} else {
		f, err := os.Open(source)
		if err != nil {
			return nil, fmt.Errorf("2074 read %s: %w", filepath.Base(source), err)
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxImportFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImportFileBytes {
		return nil, fmt.Errorf("2130 file is too large to import")
	}
	return data, nil
}

func (a *App) importPIMData(accountID, typeName string, data []byte, assign func(map[string]any)) (int, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return 0, err
	}
	blobID, _, _, err := s.UploadBlob(a.ctx, accountID, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}

	req := &jmap.Request{Context: a.ctx}
	req.Invoke(&jmapx.Call{Method: typeName + "/parse", Args: map[string]any{
		"accountId": accountID, "blobIds": []string{blobID},
	}})
	resp, err := a.doJMAP(req)
	if err != nil {
		return 0, err
	}
	parsed, ok := resp.Responses[0].Args.(*jmapx.ParseResponse)
	if !ok {
		return 0, fmt.Errorf("2131 unexpected parse response")
	}
	raw, ok := parsed.Parsed[blobID]
	if !ok {
		return 0, fmt.Errorf("2132 file could not be parsed")
	}

	var objs []map[string]any
	if json.Unmarshal(raw, &objs) != nil {
		var one map[string]any
		if err := json.Unmarshal(raw, &one); err != nil {
			return 0, fmt.Errorf("2132 file could not be parsed")
		}
		objs = []map[string]any{one}
	}

	create := map[string]any{}
	for i, obj := range objs {
		delete(obj, "id")
		assign(obj)
		create[fmt.Sprintf("i%d", i)] = obj
	}
	if len(create) == 0 {
		return 0, nil
	}
	// 一次 /set 受 maxObjectsInSet 限制, 分批提交。
	count := 0
	batch := map[string]any{}
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		created, err := s.SetPIM(a.ctx, accountID, typeName, batch, nil, nil, nil)
		count += len(created)
		batch = map[string]any{}
		return err
	}
	for k, v := range create {
		batch[k] = v
		if len(batch) >= 200 {
			if err := flush(); err != nil {
				return count, err
			}
		}
	}
	return count, flush()
}

// doJMAP 直接发起一次请求并把方法错误转成 error。
func (a *App) doJMAP(req *jmap.Request) (*jmap.Response, error) {
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()
	if client == nil {
		return nil, errNotSignedIn
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("2133 jmap request: %w", err)
	}
	for _, inv := range resp.Responses {
		if me, ok := inv.Args.(*jmap.MethodError); ok {
			return nil, fmt.Errorf("2134 %s failed: %s", inv.Name, me.Type)
		}
	}
	return resp, nil
}
