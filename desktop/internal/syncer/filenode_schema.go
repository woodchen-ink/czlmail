package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"git.sr.ht/~rockorager/go-jmap"
	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
)

// FileNode 草案中途改过属性名, 两种服务器都在用:
//
//   - 旧写法 (draft ≤ -14, Stalwart 0.16): 节点种类 nodeType 可省略 (没有 blobId 即目录), 媒体类型叫 type
//   - 新写法 (draft -17 起, Stalwart 1.x): 节点种类 @type ("File" / "Directory") 创建时必填, 媒体类型改名 mediaType;
//     发 type 会被拒 (invalidProperties)
//
// 调用方一律按旧写法组织对象, 发送前由 adaptFileNodeWrite 按服务器转换, 不在各处分支。

// fileSchemaTyped 报告该账号的服务器是否用新写法。结论缓存在 s.fileSchema (account → bool)。
func (s *Syncer) fileSchemaTyped(ctx context.Context, account string) bool {
	if v, ok := s.fileSchema.Load(account); ok {
		return v.(bool)
	}
	typed, known := s.detectFileSchema(ctx, account)
	if known {
		s.fileSchema.Store(account, typed)
	}
	return typed
}

// detectFileSchema 先看能力对象 (新草案声明 supportedNodeTypes), 再看缓存里服务器给过的节点原文。
// 都判断不了 (网盘是空的) 时按旧写法发送, 被拒后由 SetPIM / SetFileConditional 换写法重试。
func (s *Syncer) detectFileSchema(ctx context.Context, account string) (typed, known bool) {
	if s.client.Session != nil {
		if acc, ok := s.client.Session.Accounts[jmap.ID(account)]; ok {
			if raw, ok := acc.RawCapabilities[jmapx.FileNodeURI]; ok {
				var capability map[string]json.RawMessage
				if json.Unmarshal(raw, &capability) == nil {
					if _, ok := capability["supportedNodeTypes"]; ok {
						return true, true
					}
				}
			}
		}
	}
	if s.store != nil {
		if raw, err := s.store.FileNodeRawSample(ctx, account); err == nil && raw != "" {
			var fields map[string]json.RawMessage
			if json.Unmarshal([]byte(raw), &fields) == nil {
				_, has := fields["@type"]
				return has, true
			}
		}
	}
	return false, false
}

// rejectsFileSchema 判断写入是否因为属性写法不对被拒 (服务器点名 type 或 @type)。
func rejectsFileSchema(err error) bool {
	var set *jmapx.SetError
	if !errors.As(err, &set) || set.Type != "invalidProperties" {
		return false
	}
	return slices.Contains(set.Properties, "type") || slices.Contains(set.Properties, "@type") || slices.Contains(set.Properties, "mediaType")
}

// adaptFileNodeWrite 按服务器写法复制并改写 FileNode/set 的 create 与 update。
// 新写法: 新建对象补 @type (有 blobId 为 File, 否则 Directory), type 改名 mediaType; 更新里 @type 不可变, 只改名。
func adaptFileNodeWrite(typed bool, create map[string]any, update map[string]map[string]any) (map[string]any, map[string]map[string]any) {
	if !typed {
		return create, update
	}
	rename := func(o map[string]any) map[string]any {
		out := make(map[string]any, len(o)+1)
		for k, v := range o {
			if k == "type" {
				k = "mediaType"
			}
			out[k] = v
		}
		return out
	}
	var c map[string]any
	if create != nil {
		c = make(map[string]any, len(create))
		for key, v := range create {
			o, ok := v.(map[string]any)
			if !ok {
				c[key] = v
				continue
			}
			o = rename(o)
			if _, ok := o["@type"]; !ok {
				if _, file := o["blobId"]; file {
					o["@type"] = "File"
				} else {
					o["@type"] = "Directory"
				}
			}
			c[key] = o
		}
	}
	var u map[string]map[string]any
	if update != nil {
		u = make(map[string]map[string]any, len(update))
		for id, o := range update {
			u[id] = rename(o)
		}
	}
	return c, u
}
