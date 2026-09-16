package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ClientInfo 是一次动态客户端注册(RFC 7591)的结果。
type ClientInfo struct {
	ID          string `json:"client_id"`
	RedirectURI string `json:"-"`
}

type registrationRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	ApplicationType         string   `json:"application_type"`
}

// Register 动态注册一个公开客户端。
//
// token_endpoint_auth_method 固定为 none: 桌面端的二进制会分发到用户机器上,
// 任何嵌进去的 secret 都等同于公开, 声称持有它只会造成虚假的安全感。
// 流程的实际防护来自 PKCE 与回环重定向。
func Register(ctx context.Context, meta *Metadata, redirectURI string) (*ClientInfo, error) {
	if meta.RegistrationEndpoint == "" {
		// 这是运营方的配置问题, 不是用户能在客户端里解决的, 错误信息要让用户
		// 能直接转述给管理员。
		return nil, fmt.Errorf("3010 authorization server %s does not support dynamic client registration (RFC 7591); ask the server administrator to enable it", meta.Issuer)
	}

	body, err := json.Marshal(registrationRequest{
		ClientName:              "CZL Mail",
		RedirectURIs:            []string{redirectURI},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
		ApplicationType:         "native",
	})
	if err != nil {
		return nil, fmt.Errorf("3011 encode registration request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.RegistrationEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("3012 register client: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("3013 register client: status %d", resp.StatusCode)
	}

	var info ClientInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("3014 decode registration response: %w", err)
	}
	if info.ID == "" {
		return nil, fmt.Errorf("3015 registration response has no client_id")
	}

	info.RedirectURI = redirectURI
	return &info, nil
}
