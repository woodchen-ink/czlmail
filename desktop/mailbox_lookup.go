package main

import (
	"context"
	"fmt"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// errNoTrashMailbox 在账号没有回收站时返回, 由界面提示用户只能永久删除。
var errNoTrashMailbox = fmt.Errorf("2030 account has no trash mailbox")

// mailboxByRole 按 JMAP 语义角色找邮箱, 找不到返回空串而非错误。
//
// 用 role 而不是用名字匹配: name 是用户可改的, 中文界面下"收件箱"与 Inbox
// 都指向同一个 role=inbox 的邮箱, 按名字找必然在多语言环境下失效。
func mailboxByRole(st *store.Store, ctx context.Context, accountID, role string) (string, error) {
	boxes, err := st.Mailboxes(ctx, accountID)
	if err != nil {
		return "", err
	}
	for _, b := range boxes {
		// 用 Kind 而不是 Role: 没有 role 但名为 "Trash" 的邮箱也应被当作回收站。
		if b.Kind == role {
			return b.ID, nil
		}
	}
	return "", nil
}
