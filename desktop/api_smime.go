package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

// maxCertificateFile 是导入的 .p12 / .pfx 的大小上限; 正常的证书文件只有几 KB。
const maxCertificateFile = 1 << 20

// PickSigningCertificate 选择要导入的证书文件 (.p12 / .pfx), 取消时返回空串。
// 只在手机上可用: 桌面端用系统证书存储里的证书。
func (a *App) PickSigningCertificate() (string, error) {
	return a.openFileDialog(fileDialog{
		Title:   a.t("导入签名证书"),
		Filters: []fileFilter{{Name: a.t("证书文件 (.p12, .pfx)"), Pattern: "*.p12;*.pfx"}},
	})
}

// ImportSigningCertificate 导入 .p12 / .pfx: 私钥与证书链存进系统密钥库, 之后可在发件身份里选用。
// 密码不对返回 2407, 文件不能用 2408, 证书不能签名邮件 2409。
func (a *App) ImportSigningCertificate(path, password string) (smime.Certificate, error) {
	info, err := os.Stat(path)
	if err != nil {
		return smime.Certificate{}, fmt.Errorf("2408 read certificate file: %w", err)
	}
	if info.Size() > maxCertificateFile {
		return smime.Certificate{}, fmt.Errorf("2408 certificate file is too large")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return smime.Certificate{}, fmt.Errorf("2408 read certificate file: %w", err)
	}
	c, err := smime.Import(data, password, time.Now())
	switch {
	case errors.Is(err, smime.ErrUnsupported):
		return c, fmt.Errorf("2170 certificate import not supported on this platform")
	case errors.Is(err, smime.ErrBadPassword):
		return c, fmt.Errorf("2407 wrong certificate password")
	case errors.Is(err, smime.ErrBadFile):
		return c, fmt.Errorf("2408 not a usable PKCS#12 certificate file")
	case errors.Is(err, smime.ErrNotSigning):
		return c, fmt.Errorf("2409 certificate cannot sign email")
	case err != nil:
		return c, fmt.Errorf("2410 store certificate: %w", err)
	}
	// 手机上的文件选择器把文件复制进了本程序的缓存目录; 导入后私钥已在密钥库里, 删掉这份副本。
	if isMobile {
		_ = os.Remove(path)
	}
	return c, nil
}

// RemoveSigningCertificate 删除导入的证书 (私钥一并从密钥库删除)。
func (a *App) RemoveSigningCertificate(thumbprint string) error {
	err := smime.Remove(strings.ToUpper(strings.ReplaceAll(thumbprint, " ", "")))
	switch {
	case errors.Is(err, smime.ErrUnsupported):
		return fmt.Errorf("2170 certificate import not supported on this platform")
	case err != nil:
		return fmt.Errorf("2410 remove certificate: %w", err)
	}
	return nil
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

// VerifySignature 核对邮件的 S/MIME 签名。不是签名邮件时返回 nil。
// 需要下载原文, 离线且原文不在服务器可达时返回错误, 界面不显示结果。
func (a *App) VerifySignature(accountID, emailID string) (*smime.Verification, error) {
	detail, err := a.emailDetail(accountID, emailID)
	if err != nil {
		return nil, err
	}
	raw, err := a.rawMessage(accountID, emailID)
	if err != nil {
		return nil, err
	}
	from := ""
	if len(detail.From) == 1 {
		from = detail.From[0].Email
	}
	return smime.VerifyMessage(raw, from, time.Now(), nil)
}
