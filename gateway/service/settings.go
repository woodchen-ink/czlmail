package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/crypto"
	"github.com/woodchen-ink/czlmail/gateway/pkg/netx"
)

// Setting keys. Every key is registered in settingDefs with its type and default;
// code reads settings through Settings, never from the table.
const (
	// SettingRegistrationOpen lets anyone create a user account in the admin.
	SettingRegistrationOpen = "site.registration_open"
	// SettingAllowPrivateNetworks lets upstream and proxy hosts and JMAP push URLs
	// resolve to private, loopback and link-local addresses. Off, so the gateway
	// cannot be used to probe the network it runs in.
	SettingAllowPrivateNetworks = "security.allow_private_networks"
	// SettingProxyDefault is the SOCKS5 proxy of upstreams in proxy mode "follow":
	// socks5://[user@]host:port, empty for direct connections.
	SettingProxyDefault = "proxy.default"
	// SettingProxyDefaultPassword is that proxy's password (encrypted).
	SettingProxyDefaultPassword = "proxy.default_password"
)

// SettingKind is the JSON type a setting holds.
type SettingKind string

// Setting kinds.
const (
	KindBool   SettingKind = "bool"
	KindString SettingKind = "string"
	// KindSecret is a string sealed with the master key. The admin sees "********"
	// when it is set; writing "" keeps it, null clears it.
	KindSecret SettingKind = "secret"
)

// SettingDef registers one setting.
type SettingDef struct {
	Key     string
	Kind    SettingKind
	Default any
	// Validate checks a string value (KindString); nil accepts anything.
	Validate func(string) error
	// Purpose is the crypto purpose of a KindSecret.
	Purpose string
}

var settingDefs = []SettingDef{
	{Key: SettingRegistrationOpen, Kind: KindBool, Default: false},
	{Key: SettingAllowPrivateNetworks, Kind: KindBool, Default: false},
	{Key: SettingProxyDefault, Kind: KindString, Default: "", Validate: validProxySetting},
	{Key: SettingProxyDefaultPassword, Kind: KindSecret, Default: "", Purpose: crypto.PurposeProxySecret},
}

func validProxySetting(s string) error {
	if s == "" {
		return nil
	}
	_, err := netx.ParseProxyURL(s)
	return err
}

// secretMask is what the admin sees for a secret that is set.
const secretMask = "********"

func init() {
	// Secret settings are stored sealed in settings.value; rotate-key re-encrypts them.
	for _, d := range settingDefs {
		if d.Kind == KindSecret {
			RegisterSealedColumn(SealedColumn{Table: "settings", IDColumn: "key", Column: "value", Purpose: d.Purpose, Where: "key = '" + d.Key + "'"})
		}
	}
}

func secretAAD(key string) crypto.AAD {
	return crypto.AAD{Table: "settings", RowID: key, Column: "value"}
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
	ring   *crypto.Keyring
	mu     sync.RWMutex
	values map[string]any // secrets hold their sealed text

	obsMu     sync.Mutex
	observers []func(keys []string)
}

// LoadSettings reads every stored setting. Rows for unknown keys are ignored, and a row
// that no longer parses falls back to the default. ring seals secret settings; with a
// nil ring they cannot be written.
func LoadSettings(db *gorm.DB, ring *crypto.Keyring) (*Settings, error) {
	var rows []model.Setting
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	s := &Settings{db: db, ring: ring, values: map[string]any{}}
	for _, r := range rows {
		def, ok := findSettingDef(r.Key)
		if !ok {
			continue
		}
		if def.Kind == KindSecret {
			s.values[r.Key] = r.Value
			continue
		}
		if v, err := parseSetting(def, json.RawMessage(r.Value)); err == nil {
			s.values[r.Key] = v
		}
	}
	return s, nil
}

// OnChange registers fn to run after settings are saved, with the changed keys.
func (s *Settings) OnChange(fn func(keys []string)) {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	s.observers = append(s.observers, fn)
}

// String returns a string setting, or its default.
func (s *Settings) String(key string) string {
	v, _ := s.get(key).(string)
	return v
}

// Secret returns a secret setting in clear text ("" when unset).
func (s *Settings) Secret(key string) (string, error) {
	sealed, _ := s.get(key).(string)
	if sealed == "" || s.ring == nil {
		return "", nil
	}
	def, _ := findSettingDef(key)
	plain, err := s.ring.Open(def.Purpose, secretAAD(key), sealed)
	if err != nil {
		return "", err
	}
	return string(plain), nil
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
		v := s.get(d.Key)
		if d.Kind == KindSecret {
			if v != "" {
				v = secretMask
			}
		}
		out = append(out, SettingValue{Key: d.Key, Kind: d.Kind, Value: v, Default: d.Default})
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
	var cleared []string
	for key, raw := range changes {
		def, ok := findSettingDef(key)
		if !ok {
			return &SettingError{Key: key, Err: ErrUnknownSetting}
		}
		if def.Kind == KindSecret {
			sealed, keep, err := s.sealSecret(def, raw)
			if err != nil {
				return &SettingError{Key: key, Err: ErrInvalidSetting}
			}
			switch {
			case keep:
			case sealed == "":
				cleared = append(cleared, key)
				parsed[key] = ""
			default:
				parsed[key] = sealed
				rows = append(rows, model.Setting{Key: key, Value: sealed, UpdatedBy: actor.PublicID})
			}
			continue
		}
		v, err := parseSetting(def, raw)
		if err != nil {
			return &SettingError{Key: key, Err: ErrInvalidSetting}
		}
		parsed[key] = v
		enc, _ := json.Marshal(v)
		rows = append(rows, model.Setting{Key: key, Value: string(enc), UpdatedBy: actor.PublicID})
	}
	if len(parsed) == 0 {
		return nil
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if len(rows) > 0 {
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&rows).Error; err != nil {
				return err
			}
		}
		if len(cleared) > 0 {
			return tx.Where("`key` IN ?", cleared).Delete(&model.Setting{}).Error
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	keys := make([]string, 0, len(parsed))
	for k, v := range parsed {
		s.values[k] = v
		keys = append(keys, k)
	}
	s.mu.Unlock()
	s.obsMu.Lock()
	obs := slices.Clone(s.observers)
	s.obsMu.Unlock()
	for _, fn := range obs {
		fn(keys)
	}
	return nil
}

// sealSecret reads a secret update: a string seals it, "" keeps the stored one,
// null clears it.
func (s *Settings) sealSecret(def SettingDef, raw json.RawMessage) (sealed string, keep bool, err error) {
	if string(raw) == "null" {
		return "", false, nil
	}
	var plain string
	if err := json.Unmarshal(raw, &plain); err != nil {
		return "", false, err
	}
	if plain == "" {
		return "", true, nil
	}
	if s.ring == nil || len(plain) > 1024 {
		return "", false, ErrInvalidSetting
	}
	sealed, err = s.ring.Seal(def.Purpose, secretAAD(def.Key), []byte(plain))
	return sealed, false, err
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
	case KindString:
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return nil, err
		}
		str = strings.TrimSpace(str)
		if len(str) > 1024 {
			return nil, ErrInvalidSetting
		}
		if def.Validate != nil {
			if err := def.Validate(str); err != nil {
				return nil, err
			}
		}
		return str, nil
	}
	return nil, fmt.Errorf("unsupported kind %q", def.Kind)
}
