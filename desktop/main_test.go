package main

import (
	"os"
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
)

// TestMain 把钥匙串服务名换成测试专用的。
//
// 被测代码 (ensureCacheOwner、SaveAIConfig 等) 会真的读写系统钥匙串; 不换的话,
// 在装了正式版的开发机上跑一次测试就把正式版的 AI API Key 删掉。
func TestMain(m *testing.M) {
	config.KeyringService = "czlmail-go-test"
	os.Exit(m.Run())
}
