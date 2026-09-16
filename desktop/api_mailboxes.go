package main

import (
	"fmt"
	"strings"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/mailbox"
)

// 文件夹管理: 新建、重命名、移动、删除。

func (a *App) setMailbox(accountID string, set *mailbox.Set) error {
	req := &jmap.Request{Context: a.ctx}
	set.Account = jmap.ID(accountID)
	req.Invoke(set)
	resp, err := a.doJMAP(req)
	if err != nil {
		return err
	}
	if r, ok := resp.Responses[0].Args.(*mailbox.SetResponse); ok {
		for _, m := range []map[jmap.ID]*jmap.SetError{r.NotCreated, r.NotUpdated, r.NotDestroyed} {
			for _, e := range m {
				if e == nil {
					continue
				}
				desc := e.Type
				if e.Description != nil {
					desc += ": " + *e.Description
				}
				if e.Type == "mailboxHasEmail" {
					return fmt.Errorf("2191 folder is not empty")
				}
				if e.Type == "mailboxHasChild" {
					return fmt.Errorf("2192 folder has subfolders")
				}
				return fmt.Errorf("2190 %s", desc)
			}
		}
	}
	if s, err := a.currentSyncer(); err == nil {
		return s.SyncAccount(a.ctx, accountID)
	}
	return nil
}

// CreateMailbox 新建文件夹, parentID 为空表示顶层。
func (a *App) CreateMailbox(accountID, parentID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "/") {
		return fmt.Errorf("2193 invalid folder name")
	}
	mb := &mailbox.Mailbox{Name: name, IsSubscribed: true}
	if parentID != "" {
		mb.ParentID = jmap.ID(parentID)
	}
	return a.setMailbox(accountID, &mailbox.Set{Create: map[jmap.ID]*mailbox.Mailbox{"new": mb}})
}

// RenameMailbox 重命名文件夹。
func (a *App) RenameMailbox(accountID, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "/") {
		return fmt.Errorf("2193 invalid folder name")
	}
	return a.setMailbox(accountID, &mailbox.Set{Update: map[jmap.ID]jmap.Patch{jmap.ID(id): {"name": name}}})
}

// MoveMailbox 把文件夹移到另一个父文件夹下, parentID 为空表示移到顶层。
func (a *App) MoveMailbox(accountID, id, parentID string) error {
	if id == parentID {
		return fmt.Errorf("2194 cannot move a folder into itself")
	}
	var parent any
	if parentID != "" {
		parent = parentID
	}
	return a.setMailbox(accountID, &mailbox.Set{Update: map[jmap.ID]jmap.Patch{jmap.ID(id): {"parentId": parent}}})
}

// DeleteMailbox 删除文件夹。deleteEmails 为假且文件夹不空时报错, 由界面询问后再带真值重试。
// 只在其它文件夹里也有的邮件不会被删除。
func (a *App) DeleteMailbox(accountID, id string, deleteEmails bool) error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	boxes, err := st.Mailboxes(a.ctx, accountID)
	if err != nil {
		return err
	}
	for _, b := range boxes {
		if b.ID == id && b.Role != "" {
			return fmt.Errorf("2195 system folders cannot be deleted")
		}
	}
	return a.setMailbox(accountID, &mailbox.Set{Destroy: []jmap.ID{jmap.ID(id)}, OnDestroyRemoveEmails: deleteEmails})
}
