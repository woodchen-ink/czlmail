package service

import (
	"errors"
	"strings"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

// addressScanLimit is how many of the newest messages resolveAddress reads.
const addressScanLimit = 2000

// defaultName is the account name of a new OAuth mailbox: its address, with the
// provider's label ("me@x.com (Outlook)") while the address is only the
// sign-in name or when another mailbox of the owner already has that name
// (the same address at Gmail and at Microsoft).
func (s *Upstreams) defaultName(ownerID uint, label, address string, confirmed bool) (string, error) {
	labelled := address
	if label != "" {
		labelled = address + " (" + label + ")"
	}
	if !confirmed {
		return labelled, nil
	}
	var n int64
	if err := s.db.Model(&model.Upstream{}).Where("user_id = ? AND lower(name) = ?", ownerID, strings.ToLower(address)).Count(&n).Error; err != nil {
		return "", err
	}
	if n > 0 {
		return labelled, nil
	}
	return address, nil
}

// resolveAddress finds the own address of a mailbox whose address is pending
// (a personal Microsoft account signed in with a Gmail address has an
// outlook_<hex>@outlook.com mailbox that no API reports) in the addresses of
// its newest mail, after its first sync. When found, Email (and so the primary
// identity) becomes that address, and a default name follows it. Without a
// match it stays pending; the next start looks again, and the admin may set it.
func (m *UpstreamManager) resolveAddress(accountID string, idx *mailindex.Store) {
	var row model.Upstream
	if err := m.db.Where("public_id = ?", accountID).First(&row).Error; err != nil || !row.AddressPending || m.oauth == nil {
		return
	}
	p, ok := m.oauth.providerForKind(row.Kind)
	if !ok || p.OwnAddress == nil {
		return
	}
	var mails []model.IndexEmail
	if err := idx.DB().Select("from_addr", "to_addr", "cc", "bcc").Where("orphaned_from = ''").
		Order("received_at DESC").Limit(addressScanLimit).Find(&mails).Error; err != nil {
		m.log.Warn("upstream address scan", "account", accountID, "err", err)
		return
	}
	counts := map[string]int{}
	best := ""
	for _, e := range mails {
		for _, col := range []string{e.From, e.To, e.Cc, e.Bcc} {
			for _, a := range mailindex.DecodeAddresses(col) {
				addr := strings.ToLower(a.Email)
				if !p.OwnAddress(addr) {
					continue
				}
				counts[addr]++
				if counts[addr] > counts[best] {
					best = addr
				}
			}
		}
	}
	if best == "" {
		m.log.Info("upstream address still pending", "account", accountID, "scanned", len(mails))
		return
	}
	updates := map[string]any{"email": best, "address_pending": false}
	if row.Name == row.Email+" ("+p.Label+")" {
		name, err := m.oauth.ups.defaultName(row.UserID, p.Label, best, true)
		if err != nil {
			return
		}
		updates["name"] = name
	}
	// Only while still pending: an address the admin set meanwhile wins.
	res := m.db.Model(&model.Upstream{}).Where("id = ? AND address_pending = ?", row.ID, true).Updates(updates)
	if res.Error != nil || res.RowsAffected == 0 {
		return
	}
	if err := m.db.First(&row, row.ID).Error; errors.Is(err, gorm.ErrRecordNotFound) || err != nil {
		return
	}
	m.log.Info("upstream address found", "account", accountID, "email", best)
	m.Renamed(&row)
}
