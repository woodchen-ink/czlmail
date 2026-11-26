package service

import (
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

// Accounts manages users, admin sessions, the first-run setup link and app passwords.
type Accounts struct {
	db       *gorm.DB
	settings *Settings
	limiter  *RateLimiter
	inval    *Invalidations
	log      *slog.Logger
	now      func() time.Time

	setupMu sync.Mutex
	setup   setupToken
}

// NewAccounts wires the account service.
func NewAccounts(db *gorm.DB, settings *Settings, limiter *RateLimiter, inval *Invalidations, log *slog.Logger) *Accounts {
	return &Accounts{db: db, settings: settings, limiter: limiter, inval: inval, log: log, now: time.Now}
}

// NewUser is a validated request to create a user.
type NewUser struct {
	Email    string // lower-case
	Name     string
	Password string
	Role     string // admin or user
}

// NormalizeEmail is the form emails are stored and compared in.
func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// IsAdmin reports whether u may manage users and settings.
func IsAdmin(u *model.User) bool {
	return u != nil && (u.Role == model.RoleOwner || u.Role == model.RoleAdmin)
}

func (a *Accounts) userByPublicID(tx *gorm.DB, id string) (*model.User, error) {
	var u model.User
	err := tx.Where("public_id = ?", id).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUserNotFound
	}
	return &u, err
}

func (a *Accounts) userByEmail(tx *gorm.DB, email string) (*model.User, error) {
	var u model.User
	err := tx.Where("email = ?", email).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUserNotFound
	}
	return &u, err
}

func (a *Accounts) emailTaken(tx *gorm.DB, email string, exceptID uint) (bool, error) {
	var n int64
	err := tx.Model(&model.User{}).Where("email = ? AND id <> ?", email, exceptID).Count(&n).Error
	return n > 0, err
}

// insertUser hashes the password and stores a new user.
func (a *Accounts) insertUser(tx *gorm.DB, in NewUser, role string) (*model.User, error) {
	taken, err := a.emailTaken(tx, in.Email, 0)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, ErrEmailTaken
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	u := &model.User{PublicID: NewPublicID(), Email: in.Email, Name: in.Name, PasswordHash: hash, Role: role}
	if err := tx.Create(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

func (a *Accounts) userCount(tx *gorm.DB) (int64, error) {
	var n int64
	err := tx.Model(&model.User{}).Count(&n).Error
	return n, err
}
