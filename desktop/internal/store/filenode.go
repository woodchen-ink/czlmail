package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// FileNode 是文件或目录。
type FileNode struct {
	AccountID   string          `json:"accountId"`
	ID          string          `json:"id"`
	ParentID    string          `json:"parentId"`
	Name        string          `json:"name"`
	IsDirectory bool            `json:"isDirectory"`
	ContentType string          `json:"contentType"`
	BlobID      string          `json:"blobId"`
	Size        int64           `json:"size"`
	Created     int64           `json:"created"`
	Modified    int64           `json:"modified"`
	Role        string          `json:"role"`
	MyRights    map[string]bool `json:"myRights"`
}

// UpsertFileNodes 写入文件节点。
func UpsertFileNodes(ctx context.Context, tx *sql.Tx, accountID string, nodes []FileNode, raws []string) error {
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO file_nodes (account_id, id, parent_id, name, type, blob_id, size, created_at, modified_at,
		                        my_rights, raw_json, content_type, role)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (account_id, id) DO UPDATE SET
			parent_id = excluded.parent_id, name = excluded.name, type = excluded.type,
			blob_id = excluded.blob_id, size = excluded.size, created_at = excluded.created_at,
			modified_at = excluded.modified_at, my_rights = excluded.my_rights, raw_json = excluded.raw_json,
			content_type = excluded.content_type, role = excluded.role`)
	if err != nil {
		return wrap(CodeQuery, "prepare file node upsert", err)
	}
	defer stmt.Close()

	for i, n := range nodes {
		rights, _ := json.Marshal(n.MyRights)
		var parent any
		if n.ParentID != "" {
			parent = n.ParentID
		}
		kind := "file"
		if n.IsDirectory {
			kind = "directory"
		}
		if _, err := stmt.ExecContext(ctx, accountID, n.ID, parent, n.Name, kind, n.BlobID, n.Size,
			n.Created, n.Modified, string(rights), raws[i], n.ContentType, n.Role); err != nil {
			return wrap(CodeQuery, "upsert file node", err)
		}
	}
	return nil
}

const fileNodeColumns = `account_id, id, COALESCE(parent_id, ''), name, type, blob_id, size,
	created_at, modified_at, content_type, role, my_rights`

// FileNodes 列出某目录的直接子节点, parentID 为空表示根目录。目录在前。
func (s *Store) FileNodes(ctx context.Context, accountID, parentID string) ([]FileNode, error) {
	query := `SELECT ` + fileNodeColumns + ` FROM file_nodes WHERE account_id = ? AND `
	args := []any{accountID}
	if parentID == "" {
		query += `parent_id IS NULL`
	} else {
		query += `parent_id = ?`
		args = append(args, parentID)
	}
	query += ` ORDER BY type = 'file', name COLLATE NOCASE`
	return s.scanFileNodes(ctx, query, args...)
}

// FileNodeByID 读单个节点。
func (s *Store) FileNodeByID(ctx context.Context, accountID, id string) (*FileNode, error) {
	list, err := s.scanFileNodes(ctx, `SELECT `+fileNodeColumns+` FROM file_nodes WHERE account_id = ? AND id = ?`, accountID, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// FileNodeByPath 按父目录与名称找节点, 用于模板同步文件这类固定路径。
func (s *Store) FileNodeByName(ctx context.Context, accountID, parentID, name string) (*FileNode, error) {
	query := `SELECT ` + fileNodeColumns + ` FROM file_nodes WHERE account_id = ? AND name = ? AND `
	args := []any{accountID, name}
	if parentID == "" {
		query += `parent_id IS NULL`
	} else {
		query += `parent_id = ?`
		args = append(args, parentID)
	}
	list, err := s.scanFileNodes(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

func (s *Store) scanFileNodes(ctx context.Context, query string, args ...any) ([]FileNode, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap(CodeQuery, "query file nodes", err)
	}
	defer rows.Close()

	var out []FileNode
	for rows.Next() {
		var n FileNode
		var kind, rights string
		if err := rows.Scan(&n.AccountID, &n.ID, &n.ParentID, &n.Name, &kind, &n.BlobID, &n.Size,
			&n.Created, &n.Modified, &n.ContentType, &n.Role, &rights); err != nil {
			return nil, wrap(CodeQuery, "scan file node", err)
		}
		n.IsDirectory = kind == "directory"
		_ = json.Unmarshal([]byte(rights), &n.MyRights)
		out = append(out, n)
	}
	return out, wrap(CodeQuery, "iterate file nodes", rows.Err())
}
