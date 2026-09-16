package syncer

import (
	"context"
	"io"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/core"
)

// DownloadBlob 流式下载一个 blob。调用方负责关闭。
func (s *Syncer) DownloadBlob(ctx context.Context, accountID, blobID string) (io.ReadCloser, error) {
	rc, err := s.client.DownloadWithContext(ctx, jmap.ID(accountID), jmap.ID(blobID))
	if err != nil {
		return nil, wrap(CodeRequest, "download blob", err)
	}
	return rc, nil
}

// UploadBlob 上传一段数据, 返回 blob id 与服务器识别的类型。
//
// 注意: 上传得到的 blob 只对上传者可见, 且在被某封邮件引用前可能被服务器回收,
// 因此只能在即将发送时上传, 不能提前上传后长期持有。
func (s *Syncer) UploadBlob(ctx context.Context, accountID string, r io.Reader) (blobID, contentType string, size int64, err error) {
	up, err := s.client.UploadWithContext(ctx, jmap.ID(accountID), r)
	if err != nil {
		return "", "", 0, wrap(CodeRequest, "upload blob", err)
	}
	return string(up.ID), up.Type, int64(up.Size), nil
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
