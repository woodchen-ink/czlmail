package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

// Setting keys. Every key is registered in settingDefs with its type and default;
// code reads settings through Settings, never from the table.
const (
	// SettingRegistrationOpen lets anyone create a user account in the admin.
	SettingRegistrationOpen = "site.registration_open"
	// SettingAllowPrivateNetworks lets upstream and proxy hosts resolve to private,
	// loopback and link-local addresses. Off, so the gateway cannot be used to probe
	// the network it runs in.
	SettingAllowPrivateNetworks = "security.allow_private_networks"
)

// SettingKind is the JSON type a setting holds.
type SettingKind string

// Setting kinds.
const (
	KindBool SettingKind = "bool"
)

// SettingDef registers one setting.
type SettingDef struct {
	Key     string
	Kind    SettingKind
	Default any
}

var settingDefs = []SettingDef{
	{Key: SettingRegistrationOpen, Kind: KindBool, Default: false},
	{Key: SettingAllowPrivateNetworks, Kind: KindBool, Default: false},
}

func findSettingDef(key string) (SettingDef, bool) {
	i := slices.IndexFunc(settingDefs, func(d SettingDef) bool { return d.Key == key })
	if i < 0 {
		return SettingDef{}, false
	}
	return settingDefs[i], true
}

// SettingValue is a setting with its current value, for the admin.
type SettingValue struct {
	Key     string      `json:"key"`
	Kind    SettingKind `json:"kind"`
	Value   any         `json:"value"`
	Default any         `json:"default"`
}

// SettingError names the setting that failed validation.
type SettingError struct {
	Key string
	Err error // ErrUnknownSetting or ErrInvalidSetting
}

func (e *SettingError) Error() string { return fmt.Sprintf("%s: %v", e.Key, e.Err) }
func (e *SettingError) Unwrap() error { return e.Err }

// Settings caches the settings table in memory; writes refresh the cache at once.
type Settings struct {
	db     *gorm.DB
	mu     sync.RWMutex
	values map[string]any
}

// LoadSettings reads every stored setting. Rows for unknown keys are ignored, and a row
// that no longer parses falls back to the default.
func LoadSettings(db *gorm.DB) (*Settings, error) {
	var rows []model.Setting
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	s := &Settings{db: db, values: map[string]any{}}
	for _, r := range rows {
		def, ok := findSettingDef(r.Key)
		if !ok {
			continue
		}
		if v, err := parseSetting(def, json.RawMessage(r.Value)); err == nil {
			s.values[r.Key] = v
		}
	}
	return s, nil
}

// Bool returns a bool setting, or its default.
func (s *Settings) Bool(key string) bool {
	v, _ := s.get(key).(bool)
	return v
}

func (s *Settings) get(key string) any {
	s.mu.RLock()
	v, ok := s.values[key]
	s.mu.RUnlock()
	if ok {
		return v
	}
	def, _ := findSettingDef(key)
	return def.Default
}

// List returns every registered setting.
func (s *Settings) List() []SettingValue {
	out := make([]SettingValue, 0, len(settingDefs))
	for _, d := range settingDefs {
		out = append(out, SettingValue{Key: d.Key, Kind: d.Kind, Value: s.get(d.Key), Default: d.Default})
	}
	return out
}

// Update validates and stores several settings at once; nothing is stored if any fails.
func (s *Settings) Update(actor *model.User, changes map[string]json.RawMessage) error {
	if !IsAdmin(actor) {
		return ErrForbidden
	}
	parsed := map[string]any{}
	rows := make([]model.Setting, 0, len(changes))
	for key, raw := range changes {
		def, ok := findSettingDef(key)
		if !ok {
			return &SettingError{Key: key, Err: ErrUnknownSetting}
		}
		v, err := parseSetting(def, raw)
		if err != nil {
			return &SettingError{Key: key, Err: ErrInvalidSetting}
		}
		parsed[key] = v
		enc, _ := json.Marshal(v)
		rows = append(rows, model.Setting{Key: key, Value: string(enc), UpdatedBy: actor.PublicID})
	}
	if len(rows) == 0 {
		return nil
	}
	if err := s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&rows).Error; err != nil {
		return err
	}
	s.mu.Lock()
	for k, v := range parsed {
		s.values[k] = v
	}
	s.mu.Unlock()
	return nil
}

func parseSetting(def SettingDef, raw json.RawMessage) (any, error) {
	switch def.Kind {
	case KindBool:
		var b bool
		if string(raw) == "null" {
			return nil, ErrInvalidSetting
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return b, nil
	}
	return nil, fmt.Errorf("unsupported kind %q", def.Kind)
}
