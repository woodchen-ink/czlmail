// Package accountid 定义本机账号 id 的格式。
//
// 应用可以同时登录多个服务器 (每个登录是一个 JMAP 会话), 不同服务器的账号 id 会撞车
// (Stalwart 的账号 id 只有一个字母)。本机缓存、设置、界面里的账号 id 因此带上登录的前缀:
// "<前缀>~<服务器上的账号 id>"。主登录没有前缀, 账号 id 与服务器一致, 旧缓存不用迁移。
// JMAP 的 id 只能是字母、数字、"-" 与 "_", 不会含有分隔符, 拆分没有歧义。
package accountid

import "strings"

// Separator 分隔登录前缀与服务器上的账号 id。
const Separator = "~"

// Primary 是主登录的前缀。
const Primary = ""

// Join 把登录前缀与服务器上的账号 id 拼成本机账号 id。
func Join(prefix, serverID string) string {
	if prefix == Primary {
		return serverID
	}
	return prefix + Separator + serverID
}

// Split 拆出本机账号 id 的登录前缀与服务器上的账号 id。没有前缀的属于主登录。
func Split(id string) (prefix, serverID string) {
	if p, rest, ok := strings.Cut(id, Separator); ok {
		return p, rest
	}
	return Primary, id
}
