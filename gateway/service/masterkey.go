package service

import (
	"errors"
	"fmt"
	"strconv"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/crypto"
)

// keyCheckPlaintext is sealed once with the master key; opening it at startup proves
// that GATEWAY_MASTER_KEY is the key the stored secrets were encrypted with.
const keyCheckPlaintext = "czlmail-gateway master key check"

var keyCheckAAD = crypto.AAD{Table: "meta", RowID: model.MetaMasterKeyCheck, Column: "value"}

// ErrWrongMasterKey means GATEWAY_MASTER_KEY differs from the key the data was sealed with.
var ErrWrongMasterKey = errors.New("GATEWAY_MASTER_KEY does not match the key this data directory was encrypted with")

// LoadKeyring returns the keyring for the current master key. On a fresh database it
// records version 1 and its check value; afterwards it refuses a different key instead of
// letting every stored secret fail to open later.
func LoadKeyring(db *gorm.DB, master []byte) (*crypto.Keyring, error) {
	version, check, err := readKeyMeta(db)
	if err != nil {
		return nil, err
	}
	if check == "" {
		ring, err := crypto.NewKeyring(1, master)
		if err != nil {
			return nil, err
		}
		if err := writeKeyMeta(db, ring); err != nil {
			return nil, err
		}
		return ring, nil
	}
	ring, err := crypto.NewKeyring(version, master)
	if err != nil {
		return nil, err
	}
	if _, err := ring.Open(crypto.PurposeKeyCheck, keyCheckAAD, check); err != nil {
		return nil, ErrWrongMasterKey
	}
	return ring, nil
}

// SealedColumn names a text column that holds values sealed with crypto.Keyring.
// Packages that add such a column register it, so a key rotation re-encrypts it.
type SealedColumn struct {
	Table    string // table name
	IDColumn string // the row's public id, used in the AAD
	Column   string // the sealed column
	Purpose  string // crypto purpose
	// Where narrows the rows (written in code, never from input), for tables
	// where only some rows hold a sealed value, such as settings.
	Where string
}

var (
	sealedMu      sync.Mutex
	sealedColumns []SealedColumn
)

// RegisterSealedColumn adds a column to the rotation set. Call it from an init function.
func RegisterSealedColumn(c SealedColumn) {
	sealedMu.Lock()
	defer sealedMu.Unlock()
	sealedColumns = append(sealedColumns, c)
}

func registeredSealedColumns() []SealedColumn {
	sealedMu.Lock()
	defer sealedMu.Unlock()
	return append([]SealedColumn(nil), sealedColumns...)
}

// RotateMasterKey re-encrypts every registered sealed column from oldMaster to newMaster
// in one transaction and bumps the key version. Nothing changes if any value fails to open.
// It returns the number of values re-encrypted.
func RotateMasterKey(db *gorm.DB, oldMaster, newMaster []byte) (int, error) {
	oldRing, err := LoadKeyring(db, oldMaster)
	if err != nil {
		if errors.Is(err, ErrWrongMasterKey) {
			return 0, errors.New("the old master key does not match this data directory")
		}
		return 0, err
	}
	newRing, err := crypto.NewKeyring(oldRing.Current()+1, newMaster)
	if err != nil {
		return 0, err
	}
	if err := newRing.AddOld(oldRing.Current(), oldMaster); err != nil {
		return 0, err
	}

	count := 0
	err = db.Transaction(func(tx *gorm.DB) error {
		for _, c := range registeredSealedColumns() {
			n, err := rotateColumn(tx, newRing, c)
			if err != nil {
				return fmt.Errorf("%s.%s: %w", c.Table, c.Column, err)
			}
			count += n
		}
		return writeKeyMeta(tx, newRing)
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

func rotateColumn(tx *gorm.DB, ring *crypto.Keyring, c SealedColumn) (int, error) {
	type row struct {
		ID     string
		Sealed string
	}
	var rows []row
	err := tx.Table(c.Table).
		Select(c.IDColumn + " AS id, " + c.Column + " AS sealed"). // names come from code, never from input
		Where(c.Column + " IS NOT NULL AND " + c.Column + " <> ''").
		Scan(&rows).Error
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		if !ring.NeedsRotation(r.Sealed) {
			continue
		}
		aad := crypto.AAD{Table: c.Table, RowID: r.ID, Column: c.Column}
		plain, err := ring.Open(c.Purpose, aad, r.Sealed)
		if err != nil {
			return 0, fmt.Errorf("row %s: %w", r.ID, err)
		}
		sealed, err := ring.Seal(c.Purpose, aad, plain)
		if err != nil {
			return 0, err
		}
		if err := tx.Table(c.Table).Where(c.IDColumn+" = ?", r.ID).Update(c.Column, sealed).Error; err != nil {
			return 0, err
		}
		n++
	}
	return n, nil
}

func whereOrTrue(w string) string {
	if w == "" {
		return "1 = 1"
	}
	return w
}

func readKeyMeta(db *gorm.DB) (uint32, string, error) {
	var rows []model.Meta
	if err := db.Where("`key` IN ?", []string{model.MetaMasterKeyVersion, model.MetaMasterKeyCheck}).Find(&rows).Error; err != nil {
		return 0, "", err
	}
	var version uint32
	var check string
	for _, r := range rows {
		switch r.Key {
		case model.MetaMasterKeyVersion:
			v, err := strconv.ParseUint(r.Value, 10, 32)
			if err != nil || v == 0 {
				return 0, "", fmt.Errorf("corrupt %s %q", model.MetaMasterKeyVersion, r.Value)
			}
			version = uint32(v)
		case model.MetaMasterKeyCheck:
			check = r.Value
		}
	}
	if check != "" && version == 0 {
		return 0, "", fmt.Errorf("missing %s", model.MetaMasterKeyVersion)
	}
	return version, check, nil
}

func writeKeyMeta(db *gorm.DB, ring *crypto.Keyring) error {
	check, err := ring.Seal(crypto.PurposeKeyCheck, keyCheckAAD, []byte(keyCheckPlaintext))
	if err != nil {
		return err
	}
	rows := []model.Meta{
		{Key: model.MetaMasterKeyVersion, Value: strconv.FormatUint(uint64(ring.Current()), 10)},
		{Key: model.MetaMasterKeyCheck, Value: check},
	}
	return db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&rows).Error
}
