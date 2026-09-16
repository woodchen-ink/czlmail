package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/vacationresponse"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/sieve"
)

// 邮件过滤器(Sieve)与假期自动回复。都在服务端执行, 客户端关着也生效。

// FilterState 是过滤器页面的数据。
type FilterState struct {
	// Managed 为假表示现有脚本不是规则编辑器生成的, 只能用原始编辑器修改。
	Managed bool         `json:"managed"`
	Rules   []sieve.Rule `json:"rules"`
	Script  string       `json:"script"`
}

const filterScriptName = "filters"

type sieveScript struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BlobID   string `json:"blobId"`
	IsActive bool   `json:"isActive"`
}

// activeScript 返回当前生效的脚本(没有时返回同名脚本或 nil)及其内容。
func (a *App) activeScript(accountID string) (*sieveScript, string, error) {
	req := &jmap.Request{Context: a.ctx}
	req.Invoke(jmapx.Get("SieveScript", accountID, nil))
	resp, err := a.doJMAP(req)
	if err != nil {
		return nil, "", err
	}
	got, ok := resp.Responses[0].Args.(*jmapx.GetResponse)
	if !ok {
		return nil, "", fmt.Errorf("2180 unexpected response to SieveScript/get")
	}
	var chosen *sieveScript
	for _, raw := range got.List {
		var s sieveScript
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		if s.IsActive || (chosen == nil && s.Name == filterScriptName) {
			c := s
			chosen = &c
			if s.IsActive {
				break
			}
		}
	}
	if chosen == nil {
		return nil, "", nil
	}
	syncer, err := a.currentSyncer()
	if err != nil {
		return nil, "", err
	}
	rc, err := syncer.DownloadBlob(a.ctx, accountID, chosen.BlobID)
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	return chosen, string(data), err
}

// GetFilters 读取过滤规则。
func (a *App) GetFilters(accountID string) (FilterState, error) {
	_, script, err := a.activeScript(accountID)
	if err != nil {
		return FilterState{}, err
	}
	rules, err := sieve.Parse(script)
	if err == sieve.ErrNoMetadata {
		return FilterState{Managed: false, Script: script}, nil
	}
	if err != nil {
		return FilterState{}, err
	}
	if rules == nil {
		rules = []sieve.Rule{}
	}
	return FilterState{Managed: true, Rules: rules, Script: script}, nil
}

// SaveFilters 按规则生成脚本并启用。
func (a *App) SaveFilters(accountID string, rules []sieve.Rule) error {
	script, err := sieve.Generate(rules)
	if err != nil {
		return fmt.Errorf("2181 %w", err)
	}
	return a.SaveSieveScript(accountID, script)
}

// SaveSieveScript 校验并保存原始脚本, 保存后立即生效。
func (a *App) SaveSieveScript(accountID, script string) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	blobID, _, _, err := s.UploadBlob(a.ctx, accountID, bytes.NewReader([]byte(script)))
	if err != nil {
		return err
	}

	check := &jmap.Request{Context: a.ctx}
	check.Invoke(&jmapx.Call{Method: "SieveScript/validate", Args: map[string]any{"accountId": accountID, "blobId": blobID}})
	resp, err := a.doJMAP(check)
	if err != nil {
		return err
	}
	if v, ok := resp.Responses[0].Args.(*jmapx.ValidateResponse); ok && v.Error != nil {
		return fmt.Errorf("2182 invalid sieve script: %s", strings.TrimSpace(v.Error.Description))
	}

	existing, _, err := a.activeScript(accountID)
	if err != nil {
		return err
	}
	req := &jmap.Request{Context: a.ctx}
	args := map[string]any{"accountId": accountID}
	if existing != nil {
		args["update"] = map[string]any{existing.ID: map[string]any{"blobId": blobID}}
		args["onSuccessActivateScript"] = existing.ID
	} else {
		args["create"] = map[string]any{"s": map[string]any{"name": filterScriptName, "blobId": blobID}}
		args["onSuccessActivateScript"] = "#s"
	}
	req.Invoke(&jmapx.Call{Method: "SieveScript/set", Args: args})
	resp, err = a.doJMAP(req)
	if err != nil {
		return err
	}
	if set, ok := resp.Responses[0].Args.(*jmapx.SetResponse); ok {
		if err := set.FirstError(); err != nil {
			return fmt.Errorf("2183 save sieve script: %w", err)
		}
	}
	return nil
}

// Vacation 是假期自动回复。日期为 RFC 3339 或空。
type Vacation struct {
	Enabled  bool   `json:"enabled"`
	FromDate string `json:"fromDate"`
	ToDate   string `json:"toDate"`
	Subject  string `json:"subject"`
	TextBody string `json:"textBody"`
	HTMLBody string `json:"htmlBody"`
}

func (a *App) GetVacation(accountID string) (Vacation, error) {
	req := &jmap.Request{Context: a.ctx}
	req.Invoke(&vacationresponse.Get{Account: jmap.ID(accountID)})
	resp, err := a.doJMAP(req)
	if err != nil {
		return Vacation{}, err
	}
	got, ok := resp.Responses[0].Args.(*vacationresponse.GetResponse)
	if !ok || len(got.List) == 0 {
		return Vacation{}, nil
	}
	v := got.List[0]
	out := Vacation{Enabled: v.IsEnabled, Subject: deref(v.Subject), TextBody: deref(v.TextBody), HTMLBody: deref(v.HTMLBody)}
	if v.FromDate != nil {
		out.FromDate = v.FromDate.Format("2006-01-02T15:04:05Z07:00")
	}
	if v.ToDate != nil {
		out.ToDate = v.ToDate.Format("2006-01-02T15:04:05Z07:00")
	}
	return out, nil
}

func (a *App) SaveVacation(accountID string, v Vacation) error {
	patch := jmap.Patch{
		"isEnabled": v.Enabled,
		"subject":   nilIfBlank(v.Subject),
		"textBody":  nilIfBlank(v.TextBody),
		"htmlBody":  nilIfBlank(v.HTMLBody),
		"fromDate":  nilIfBlank(v.FromDate),
		"toDate":    nilIfBlank(v.ToDate),
	}
	req := &jmap.Request{Context: a.ctx}
	req.Invoke(&vacationresponse.Set{Account: jmap.ID(accountID), Update: map[jmap.ID]jmap.Patch{"singleton": patch}})
	resp, err := a.doJMAP(req)
	if err != nil {
		return err
	}
	if set, ok := resp.Responses[0].Args.(*vacationresponse.SetResponse); ok {
		if e, bad := set.NotUpdated["singleton"]; bad && e != nil {
			desc := e.Type
			if e.Description != nil {
				desc += ": " + *e.Description
			}
			return fmt.Errorf("2184 save vacation: %s", desc)
		}
	}
	return nil
}

func nilIfBlank(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
