package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/smime"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// SmimeChoice 是某个发件地址的 S/MIME 签名设置。
type SmimeChoice struct {
	// Thumbprint 是系统证书存储里证书的指纹 (SHA-1, 十六进制)。
	Thumbprint string `json:"thumbprint"`
	// Auto 为真时写信默认勾选「签名」。
	Auto bool `json:"auto"`
}

// settingSmime 存 发件地址(小写) → SmimeChoice 的 JSON。
const settingSmime = "smimeSigning"

// ListSigningCertificates 列出系统证书存储里可用于邮件签名的证书 (带私钥与邮箱地址)。
// 不支持的系统返回 2170。
func (a *App) ListSigningCertificates() ([]smime.Certificate, error) {
	list, err := smime.List(time.Now())
	if errors.Is(err, smime.ErrUnsupported) {
		return nil, fmt.Errorf("2170 certificate store not supported on this platform")
	}
	if list == nil {
		list = []smime.Certificate{}
	}
	return list, err
}

func smimeSettings(raw string) map[string]SmimeChoice {
	out := map[string]SmimeChoice{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

// normalizeSmime 把地址统一成小写, 去掉没选证书的项。
func normalizeSmime(in map[string]SmimeChoice) map[string]SmimeChoice {
	out := map[string]SmimeChoice{}
	for addr, c := range in {
		addr = strings.ToLower(strings.TrimSpace(addr))
		c.Thumbprint = strings.ToUpper(strings.ReplaceAll(c.Thumbprint, " ", ""))
		if addr != "" && c.Thumbprint != "" {
			out[addr] = c
		}
	}
	return out
}

// smimeSigner 返回给 from 签名的函数; 地址没配置证书时返回 2400。
// 证书在每次发送时重新打开并检查: 程序运行期间证书可能过期或被删除。
func (a *App) smimeSigner(st *store.Store, from string) (func([]byte) ([]byte, error), error) {
	raw, _ := st.StringSetting(a.ctx, settingSmime)
	choice, ok := smimeSettings(raw)[strings.ToLower(from)]
	if !ok || choice.Thumbprint == "" {
		return nil, fmt.Errorf("2400 no signing certificate for %s", from)
	}
	open := func() (*smime.Signer, error) {
		s, err := smime.Open(choice.Thumbprint)
		switch {
		case errors.Is(err, smime.ErrUnsupported):
			return nil, fmt.Errorf("2170 certificate store not supported on this platform")
		case errors.Is(err, smime.ErrNotFound):
			return nil, fmt.Errorf("2405 signing certificate not found in the certificate store")
		case err != nil:
			return nil, fmt.Errorf("2406 cannot open signing certificate: %w", err)
		}
		if p := smime.CheckUsable(s.Cert, from, time.Now()); p != "" {
			if s.Close != nil {
				s.Close()
			}
			return nil, fmt.Errorf("%s signing certificate cannot sign for %s", p, from)
		}
		return s, nil
	}
	// 发送前先检查一次, 证书不能用时不在服务器上建任何临时邮件。
	s, err := open()
	if err != nil {
		return nil, err
	}
	if s.Close != nil {
		s.Close()
	}
	return func(msg []byte) ([]byte, error) {
		s, err := open()
		if err != nil {
			return nil, err
		}
		if s.Close != nil {
			defer s.Close()
		}
		out, err := smime.SignMessage(msg, s.Sign)
		if err != nil {
			return nil, fmt.Errorf("2406 signing failed: %w", err)
		}
		return out, nil
	}, nil
}
