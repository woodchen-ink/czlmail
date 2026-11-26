package imap

import (
	"context"
	"errors"
	"strings"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

// Mailbox writes on Gmail are label writes: only user labels can be created,
// renamed and deleted. Deleting a label keeps its messages (they stay in All
// Mail and move to Archive when it was their last label).

const gmDelim = "/"

func errSystemMailbox() error {
	return rejected("mailbox", errors.New("Gmail system mailboxes cannot be changed"))
}

func userLabel(m *model.IndexMailbox) bool {
	return m.Virtual && m.Label != "" && !strings.HasPrefix(m.Label, `\`)
}

// labelPath builds the label of name under parentID.
func (g *Gmail) labelPath(parentID, name string) (string, error) {
	if name == "" || strings.Contains(name, gmDelim) {
		return "", rejected("name", errors.New("name is empty or contains /"))
	}
	if parentID == "" {
		return name, nil
	}
	p, err := g.mailbox(parentID)
	if err != nil {
		return "", err
	}
	if !userLabel(p) {
		return "", errSystemMailbox()
	}
	return p.Label + gmDelim + name, nil
}

// CreateMailbox creates a user label.
func (g *Gmail) CreateMailbox(ctx context.Context, parentID, name string) (string, map[string]string, error) {
	label, err := g.labelPath(parentID, name)
	if err != nil {
		return "", nil, err
	}
	var id string
	states, err := g.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		if err := c.Create(ctx, label); err != nil {
			return err
		}
		return g.commit(states, func(tx *mailindex.Tx) error {
			lr, err := loadLabelRows(tx)
			if err != nil {
				return err
			}
			if m := lr.byLabel[label]; m != nil {
				id = m.ID
				return nil
			}
			m, err := lr.create(tx, label, gmDelim, "")
			if m != nil {
				id = m.ID
			}
			return err
		})
	})
	return id, states, err
}

// RenameMailbox renames or moves a user label; its sub-labels follow, and every
// mailbox keeps its id.
func (g *Gmail) RenameMailbox(ctx context.Context, mailboxID, parentID, name string) (map[string]string, error) {
	m, err := g.mailbox(mailboxID)
	if err != nil {
		return nil, err
	}
	if !userLabel(m) {
		return nil, errSystemMailbox()
	}
	label, err := g.labelPath(parentID, name)
	if err != nil {
		return nil, err
	}
	if label == m.Label {
		return map[string]string{}, nil
	}
	old := m.Label
	return g.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		if err := c.Rename(ctx, old, label); err != nil {
			return err
		}
		return g.commit(states, func(tx *mailindex.Tx) error {
			all, err := tx.Mailboxes()
			if err != nil {
				return err
			}
			for i := range all {
				x := &all[i]
				switch {
				case x.ID == m.ID:
					x.Label, x.Name, x.ParentID = label, label, parentID
				case userLabel(x) && strings.HasPrefix(x.Label, old+gmDelim):
					x.Label = label + strings.TrimPrefix(x.Label, old)
					x.Name = x.Label
				default:
					continue
				}
				if err := tx.UpdateMailbox(x, true); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

// DeleteMailbox deletes a user label.
func (g *Gmail) DeleteMailbox(ctx context.Context, mailboxID string) (map[string]string, error) {
	m, err := g.mailbox(mailboxID)
	if err != nil {
		return nil, err
	}
	if !userLabel(m) {
		return nil, errSystemMailbox()
	}
	return g.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		if sel := c.Selected(); sel != nil && sel.Name == m.Name {
			_ = c.Unselect(ctx)
		}
		if err := c.Delete(ctx, m.Name); err != nil {
			return err
		}
		return g.commit(states, func(tx *mailindex.Tx) error {
			lr, err := loadLabelRows(tx)
			if err != nil {
				return err
			}
			return g.dropLabelRow(tx, lr, m)
		})
	})
}
