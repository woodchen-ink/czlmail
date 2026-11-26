package service

import (
	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

// UserPatch is a validated partial update; nil fields stay unchanged.
type UserPatch struct {
	Email    *string
	Name     *string
	Role     *string // admin or user
	Disabled *bool
	Password *string
}

// ListUsers returns every user, oldest first. Admins only.
func (a *Accounts) ListUsers(actor *model.User) ([]model.User, error) {
	if !IsAdmin(actor) {
		return nil, ErrForbidden
	}
	var users []model.User
	err := a.db.Order("id").Find(&users).Error
	return users, err
}

// GetUser returns one user. Admins only.
func (a *Accounts) GetUser(actor *model.User, publicID string) (*model.User, error) {
	if !IsAdmin(actor) {
		return nil, ErrForbidden
	}
	return a.userByPublicID(a.db, publicID)
}

// CreateUser adds a user. Admins create users; only the owner creates admins.
func (a *Accounts) CreateUser(actor *model.User, in NewUser) (*model.User, error) {
	if err := canAssignRole(actor, in.Role); err != nil {
		return nil, err
	}
	var u *model.User
	err := a.db.Transaction(func(tx *gorm.DB) error {
		var err error
		u, err = a.insertUser(tx, in, in.Role)
		return err
	})
	return u, err
}

// UpdateUser changes a user. Admins manage users; the owner also manages admins.
// Nobody changes their own role or status here, so the owner can never be demoted,
// disabled or deleted. Disabling ends the user's sessions and app password logins at once.
func (a *Accounts) UpdateUser(actor *model.User, publicID string, p UserPatch) (*model.User, error) {
	var target *model.User
	var events []UserInvalidation
	err := a.db.Transaction(func(tx *gorm.DB) error {
		var err error
		if target, err = a.userByPublicID(tx, publicID); err != nil {
			return err
		}
		if err := canManage(actor, target); err != nil {
			return err
		}
		self := actor.ID == target.ID
		updates := map[string]any{}
		if p.Role != nil && *p.Role != target.Role {
			if self {
				return ErrSelfModify
			}
			if err := canAssignRole(actor, *p.Role); err != nil {
				return err
			}
			updates["role"] = *p.Role
		}
		if p.Disabled != nil && *p.Disabled != target.Disabled {
			if self {
				return ErrSelfModify
			}
			updates["disabled"] = *p.Disabled
			if *p.Disabled {
				events = append(events, UserInvalidation{UserPublicID: target.PublicID, Reason: "disabled"})
				if err := endSessions(tx, target.ID, 0); err != nil {
					return err
				}
			}
		}
		if p.Email != nil && *p.Email != target.Email {
			taken, err := a.emailTaken(tx, *p.Email, target.ID)
			if err != nil {
				return err
			}
			if taken {
				return ErrEmailTaken
			}
			updates["email"] = *p.Email
			events = append(events, UserInvalidation{UserPublicID: target.PublicID, Reason: "email_changed"})
		}
		if p.Name != nil {
			updates["name"] = *p.Name
		}
		if p.Password != nil {
			if self {
				return ErrSelfModify // own password changes go through ChangeOwnPassword
			}
			hash, err := hashPassword(*p.Password)
			if err != nil {
				return err
			}
			updates["password_hash"] = hash
			if err := endSessions(tx, target.ID, 0); err != nil {
				return err
			}
		}
		if len(updates) == 0 {
			return nil
		}
		if err := tx.Model(target).Updates(updates).Error; err != nil {
			return err
		}
		return tx.First(target, target.ID).Error
	})
	if err != nil {
		return nil, err
	}
	a.inval.userChanged(target.PublicID)
	for _, ev := range events {
		a.inval.publish(ev)
	}
	return target, nil
}

// DeleteUser removes a user with their sessions and app passwords.
func (a *Accounts) DeleteUser(actor *model.User, publicID string) error {
	var target *model.User
	err := a.db.Transaction(func(tx *gorm.DB) error {
		var err error
		if target, err = a.userByPublicID(tx, publicID); err != nil {
			return err
		}
		if err := canManage(actor, target); err != nil {
			return err
		}
		if actor.ID == target.ID {
			return ErrSelfModify
		}
		if err := tx.Where("user_id = ?", target.ID).Delete(&model.AppPassword{}).Error; err != nil {
			return err
		}
		if err := endSessions(tx, target.ID, 0); err != nil {
			return err
		}
		return tx.Delete(target).Error
	})
	if err != nil {
		return err
	}
	a.inval.userChanged(target.PublicID)
	a.inval.publish(UserInvalidation{UserPublicID: target.PublicID, Reason: "deleted"})
	return nil
}

// canManage: the owner manages everyone, admins manage users and themselves.
func canManage(actor, target *model.User) error {
	switch {
	case actor.Role == model.RoleOwner:
		return nil
	case actor.Role == model.RoleAdmin && (target.Role == model.RoleUser || target.ID == actor.ID):
		return nil
	}
	return ErrForbidden
}

func canAssignRole(actor *model.User, role string) error {
	switch {
	case actor.Role == model.RoleOwner && (role == model.RoleAdmin || role == model.RoleUser):
		return nil
	case actor.Role == model.RoleAdmin && role == model.RoleUser:
		return nil
	}
	return ErrForbidden
}
