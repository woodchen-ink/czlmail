package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// `czlmail.exe mcp`: 给只支持 stdio 的 MCP 客户端(如 Claude Desktop)用的桥。
//
// 桥本身不碰邮件数据, 只把 stdio 上的工具调用转发到正在运行的桌面端的本地 HTTP 端点。
// 这样数据只有一个读写方(桌面端), 不会出现两个进程同时同步、同时写缓存库。
// 桌面端没在运行时先把它拉起来, 等端点就绪。

const bridgeWaitTimeout = 30 * time.Second

func runMCPBridge() int {
	ctx := context.Background()

	info, err := waitForMCP(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "czlmail mcp:", err)
		return 1
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "czlmail-bridge", Version: version}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             info.URL,
		HTTPClient:           &http.Client{Transport: bearer{token: info.Token}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "czlmail mcp: connect to CZL Mail:", err)
		return 1
	}
	defer session.Close()

	server := mcp.NewServer(&mcp.Implementation{Name: "czlmail", Title: "CZL Mail", Version: version}, nil)
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "czlmail mcp: list tools:", err)
			return 1
		}
		name := tool.Name
		server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args any
			if len(req.Params.Arguments) > 0 {
				args = json.RawMessage(req.Params.Arguments)
			}
			return session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		})
	}

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "czlmail mcp:", err)
		return 1
	}
	return 0
}

// waitForMCP 读取桌面端写下的连接信息; 桌面端未运行时启动它并等待。
func waitForMCP(ctx context.Context) (MCPInfo, error) {
	path, err := mcpInfoPath()
	if err != nil {
		return MCPInfo{}, err
	}

	launched := false
	deadline := time.Now().Add(bridgeWaitTimeout)
	for {
		if info, ok := readMCPInfo(ctx, path); ok {
			return info, nil
		}
		if !launched {
			launched = true
			if exe, err := os.Executable(); err == nil {
				// 以普通方式启动桌面端(会缩在托盘里); 已在运行时单实例锁会让它立即退出。
				_ = exec.Command(exe).Start()
			}
		}
		if time.Now().After(deadline) {
			return MCPInfo{}, errors.New("CZL Mail is not running or MCP is disabled; enable it in CZL Mail → 设置 → AI 助手 (MCP)")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// readMCPInfo 读取连接信息并确认端点真的在监听: 桌面端异常退出时 mcp.json 可能残留。
func readMCPInfo(ctx context.Context, path string) (MCPInfo, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return MCPInfo{}, false
	}
	var info MCPInfo
	if json.Unmarshal(data, &info) != nil || info.URL == "" {
		return MCPInfo{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, info.URL, nil)
	resp, err := (&http.Client{Transport: bearer{token: info.Token}}).Do(req)
	if err != nil {
		return MCPInfo{}, false
	}
	resp.Body.Close()
	return info, resp.StatusCode != http.StatusUnauthorized
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}
