package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/core"
)

// 上传与下载不走 go-jmap 的 UploadWithContext / DownloadWithContext, 原因有二:
//
//   - 上传时它固定发送 Content-Type: application/json, 不管实际内容是什么。
//     RFC 8620 §6.1 规定服务器按这个头记录 blob 的类型; 有的服务器(如 Node 写的
//     JMAP 网关)还会按 JSON 解析请求体, 附件、Sieve 脚本一律 400。
//   - 下载时它把 {type} 与 {name} 原样替换成 application/octet-stream 与 filename,
//     不做 URI 模板要求的编码。类型里的 "/" 一旦落在路径段里, 地址就多出一段,
//     服务器按 404 处理。
//
// 这里用同一个 HttpClient(认证、会话守卫、超时都在它上面)与同一个 Session,
// 对 Stalwart 只有请求头与地址编码不同。

// blobSession 返回当前会话, 未取得时先认证, 与 go-jmap 内部的处理一致。
func (s *Syncer) blobSession() (*jmap.Session, error) {
	c := s.client
	c.Lock()
	session, endpoint := c.Session, c.SessionEndpoint
	c.Unlock()
	if session != nil {
		return session, nil
	}
	if endpoint == "" {
		return nil, fmt.Errorf("jmap/client: SessionEndpoint is empty")
	}
	if err := c.Authenticate(); err != nil {
		return nil, err
	}
	c.Lock()
	defer c.Unlock()
	return c.Session, nil
}

// expandBlobURL 展开 uploadUrl / downloadUrl 模板。
//
// RFC 8620 §2 规定这两个地址是 RFC 6570 第 1 级模板: 每个变量做简单字符串展开,
// 除非保留字符(字母、数字与 "-._~")以外一律百分号编码 —— 包括 "/", 所以
// "application/pdf" 展开为 "application%2Fpdf", 不论变量在路径里还是查询串里。
func expandBlobURL(template string, vars map[string]string) string {
	var b strings.Builder
	for {
		open := strings.IndexByte(template, '{')
		if open < 0 {
			b.WriteString(template)
			return b.String()
		}
		end := strings.IndexByte(template[open:], '}')
		if end < 0 {
			b.WriteString(template)
			return b.String()
		}
		name := template[open+1 : open+end]
		b.WriteString(template[:open])
		if value, ok := vars[name]; ok {
			b.WriteString(uriTemplateEscape(value))
		} else {
			// 不认识的变量原样保留, 与 go-jmap 的行为一致。
			b.WriteString(template[open : open+end+1])
		}
		template = template[open+end+1:]
	}
}

// uriTemplateEscape 按 RFC 6570 简单展开编码: 只保留 unreserved 字符。
func uriTemplateEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

// blobHTTPError 与 go-jmap 一样解读失败的响应: JSON 时按 RFC 7807 问题详情,
// 否则只报状态码。错误文本保持原样, 日志与界面上的提示不受影响。
func blobHTTPError(resp *http.Response) error {
	if resp.Header.Get("Content-Type") != "application/json" {
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Status)
	}
	reqErr := &jmap.RequestError{}
	if err := json.NewDecoder(resp.Body).Decode(reqErr); err != nil {
		return fmt.Errorf("HTTP %d %s (failed to decode JSON body: %v)", resp.StatusCode, resp.Status, err)
	}
	return reqErr
}

// DownloadBlob 流式下载一个 blob。调用方负责关闭。
func (s *Syncer) DownloadBlob(ctx context.Context, accountID, blobID string) (io.ReadCloser, error) {
	rc, err := s.downloadBlob(ctx, accountID, blobID)
	if err != nil {
		return nil, wrap(CodeRequest, "download blob", err)
	}
	return rc, nil
}

func (s *Syncer) downloadBlob(ctx context.Context, accountID, blobID string) (io.ReadCloser, error) {
	session, err := s.blobSession()
	if err != nil {
		return nil, err
	}
	// 类型与文件名只影响服务器回给我们的响应头, 调用方自己决定怎么展示,
	// 所以沿用 go-jmap 的取值, 只是按模板规则编码。
	target := expandBlobURL(session.DownloadURL, map[string]string{
		"accountId": s.sid(accountID),
		"blobId":    blobID,
		"type":      "application/octet-stream",
		"name":      "filename",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.HttpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, blobHTTPError(resp)
	}
	return resp.Body, nil
}

// UploadBlob 上传一段数据, 返回 blob id 与服务器识别的类型。类型未知时按
// application/octet-stream 上传; 知道类型的调用方用 UploadBlobType。
//
// 注意: 上传得到的 blob 只对上传者可见, 且在被某封邮件引用前可能被服务器回收,
// 因此只能在即将发送时上传, 不能提前上传后长期持有。
func (s *Syncer) UploadBlob(ctx context.Context, accountID string, r io.Reader) (blobID, contentType string, size int64, err error) {
	return s.UploadBlobType(ctx, accountID, "", r)
}

// UploadBlobType 以给定的 Content-Type 上传, 空串按 application/octet-stream。
func (s *Syncer) UploadBlobType(ctx context.Context, accountID, contentType string, r io.Reader) (blobID, serverType string, size int64, err error) {
	up, err := s.uploadBlob(ctx, accountID, contentType, r)
	if err != nil {
		return "", "", 0, wrap(CodeRequest, "upload blob", err)
	}
	return string(up.ID), up.Type, int64(up.Size), nil
}

func (s *Syncer) uploadBlob(ctx context.Context, accountID, contentType string, r io.Reader) (*jmap.UploadResponse, error) {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	session, err := s.blobSession()
	if err != nil {
		return nil, err
	}
	target := expandBlobURL(session.UploadURL, map[string]string{"accountId": s.sid(accountID)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := s.client.HttpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// RFC 8620 §6.1 的成功响应是 201 Created; 不少服务器(包括 Stalwart)回 200。
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, blobHTTPError(resp)
	}
	up := &jmap.UploadResponse{}
	if err := json.NewDecoder(resp.Body).Decode(up); err != nil {
		return nil, err
	}
	return up, nil
}

// MaxUploadBytes 返回服务器允许的单个上传大小, 未知时返回 0。
func (s *Syncer) MaxUploadBytes() int64 {
	if s.client.Session == nil {
		return 0
	}
	c, ok := s.client.Session.Capabilities[jmap.CoreURI].(*core.Core)
	if !ok {
		return 0
	}
	return int64(c.MaxSizeUpload)
}
