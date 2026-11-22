package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"
)

func blobSyncer(server *httptest.Server, uploadURL, downloadURL string) *Syncer {
	return &Syncer{client: &jmap.Client{
		HttpClient: server.Client(),
		Session: &jmap.Session{
			UploadURL:   server.URL + uploadURL,
			DownloadURL: server.URL + downloadURL,
		},
	}}
}

// TestUploadBlobContentType 上传带调用方给的类型, 未知时 application/octet-stream,
// 请求体原样到达; 201 与 200 都算成功。
func TestUploadBlobContentType(t *testing.T) {
	body := []byte("require \"fileinto\";\r\n\x00\xff")
	for _, tc := range []struct {
		give, want string
		status     int
	}{
		{"application/sieve", "application/sieve", http.StatusCreated},
		{"", "application/octet-stream", http.StatusOK},
	} {
		var gotType, gotPath string
		var gotBody []byte
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotType, gotPath = r.Header.Get("Content-Type"), r.URL.EscapedPath()
			gotBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			json.NewEncoder(w).Encode(map[string]any{"accountId": "a 1", "blobId": "B1", "type": gotType, "size": len(gotBody)})
		}))
		s := blobSyncer(server, "/upload/{accountId}/", "")
		id, serverType, size, err := s.UploadBlobType(context.Background(), "a 1", tc.give, bytes.NewReader(body))
		server.Close()
		if err != nil {
			t.Fatalf("%q: %v", tc.give, err)
		}
		if gotType != tc.want || serverType != tc.want {
			t.Errorf("%q: Content-Type %q, server type %q, want %q", tc.give, gotType, serverType, tc.want)
		}
		if !bytes.Equal(gotBody, body) || size != int64(len(body)) || id != "B1" {
			t.Errorf("%q: body or answer changed: %q %d %q", tc.give, gotBody, size, id)
		}
		if gotPath != "/upload/a%201/" {
			t.Errorf("%q: path %q", tc.give, gotPath)
		}
	}
}

// TestImportMessageUploadsRFC822 导入的原文以 message/rfc822 上传。
func TestImportMessageUploadsRFC822(t *testing.T) {
	var gotType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusBadRequest) // 上传之后的请求不在本测试范围内
	}))
	defer server.Close()
	s := blobSyncer(server, "/upload/{accountId}/", "")
	s.ImportMessage(context.Background(), "a", "m", []byte("Subject: x\r\n\r\nx\r\n"), nil)
	if gotType != "message/rfc822" {
		t.Errorf("Content-Type %q", gotType)
	}
}

// TestDownloadBlobURL 下载地址按 RFC 6570 第 1 级展开: 类型里的 "/" 也要编码,
// 不论 {type} 在路径段里还是查询串里; GET 不带 Content-Type。
func TestDownloadBlobURL(t *testing.T) {
	for _, tc := range []struct{ template, want string }{
		{"/download/{accountId}/{blobId}/{type}/{name}", "/download/a%201/P%3A2%2Fx/application%2Foctet-stream/filename"},
		{"/download/{accountId}/{blobId}/{name}?accept={type}", "/download/a%201/P%3A2%2Fx/filename?accept=application%2Foctet-stream"},
	} {
		var got, gotType string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, gotType = r.URL.EscapedPath(), r.Header.Get("Content-Type")
			if r.URL.RawQuery != "" {
				got += "?" + r.URL.RawQuery
			}
			io.WriteString(w, "data")
		}))
		s := blobSyncer(server, "", tc.template)
		rc, err := s.DownloadBlob(context.Background(), "a 1", "P:2/x")
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		server.Close()
		if got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.template, got, tc.want)
		}
		if gotType != "" {
			t.Errorf("GET sent Content-Type %q", gotType)
		}
		if string(data) != "data" {
			t.Errorf("body %q", data)
		}
	}
}

// TestBlobErrors 失败时的错误与以前一样: 错误码 1102, 非 JSON 只报状态,
// JSON 问题详情解成 jmap.RequestError。
func TestBlobErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.Error(w, "empty body", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"type":"urn:ietf:params:jmap:error:notFound","status":404,"detail":"no such blob"}`)
	}))
	defer server.Close()
	s := blobSyncer(server, "/upload/{accountId}/", "/download/{accountId}/{blobId}/{name}?accept={type}")

	_, _, _, err := s.UploadBlob(context.Background(), "a", strings.NewReader("x"))
	if err == nil || err.Error() != "1102 upload blob: HTTP 400 400 Bad Request" {
		t.Errorf("upload error %v", err)
	}
	var syncErr *Error
	if !errors.As(err, &syncErr) || syncErr.Code != CodeRequest {
		t.Errorf("upload error is not a 1102: %#v", err)
	}

	_, err = s.DownloadBlob(context.Background(), "a", "B")
	var reqErr *jmap.RequestError
	if !errors.As(err, &reqErr) || !strings.HasPrefix(err.Error(), "1102 download blob: ") {
		t.Errorf("download error %v", err)
	}
}

func TestExpandBlobURL(t *testing.T) {
	got := expandBlobURL("https://h/d/{accountId}/{blobId}/{name}?t={type}&x={unknown}", map[string]string{
		"accountId": "u@example.com", "blobId": "b~-._1", "name": "Q1 report (final).pdf", "type": "application/pdf",
	})
	want := "https://h/d/u%40example.com/b~-._1/Q1%20report%20%28final%29.pdf?t=application%2Fpdf&x={unknown}"
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}
