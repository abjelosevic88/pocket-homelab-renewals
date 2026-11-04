package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-renewals/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/store"
)

func remarshal(in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// moneyResult says what happened in Pocket Money.
type moneyResult struct {
	OK      bool   `json:"ok"`
	TxID    int64  `json:"tx_id,omitempty"`
	Account string `json:"account,omitempty"`
	URL     string `json:"url,omitempty"`
	Via     string `json:"via,omitempty"`
	Error   string `json:"error,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
}

// wantsMoney is the person's choice for this cost: the form's tick, else
// the setting — and only when Money is connected at all.
func (s *Server) wantsMoney(ctx context.Context, uid int64, asked *bool) bool {
	st, _ := s.store.GetSettings(ctx, uid)
	mode, _, token, _ := connSettings(st, appMoney)
	if s.connMode(mode, token) == modeOff {
		return false
	}
	if asked != nil {
		return *asked
	}
	return st.MoneyBook
}

// categoryWords match a Money category for renewal costs by name.
var categoryWords = map[string][]string{
	"insurance":    {"insurance", "osiguranje", "versicherung"},
	"vehicle":      {"car", "auto", "vehicle", "transport", "registracija", "registration"},
	"subscription": {"subscriptions", "subscription", "pretplate", "pretplata", "streaming"},
	"online":       {"internet", "hosting", "domains", "software", "subscriptions"},
	"identity":     {"fees", "government", "taxes", "administration", "takse", "documents", "dokumenti"},
	"health":       {"health", "medical", "zdravlje", "pharmacy"},
	"contract":     {"utilities", "phone", "telefon", "bills", "režije", "rezije"},
}

var fallbackWords = []string{"fees", "bills", "subscriptions", "administration", "other"}

func pickAccount(accts []pocket.Account, want, setting int64) *pocket.Account {
	for _, id := range []int64{want, setting} {
		for i := range accts {
			if id > 0 && accts[i].ID == id {
				return &accts[i]
			}
		}
	}
	for i := range accts {
		if accts[i].Everyday {
			return &accts[i]
		}
	}
	if len(accts) > 0 {
		return &accts[0]
	}
	return nil
}

func pickCategory(cats []pocket.Category, want, setting int64, group string) int64 {
	for _, id := range []int64{want, setting} {
		for _, c := range cats {
			if id > 0 && c.ID == id {
				return c.ID
			}
		}
	}
	for _, words := range [][]string{categoryWords[group], fallbackWords} {
		for _, w := range words {
			for _, c := range cats {
				if strings.EqualFold(c.Name, w) {
					return c.ID
				}
			}
		}
		for _, w := range words {
			for _, c := range cats {
				if strings.Contains(strings.ToLower(c.FullName+" "+c.Name), w) {
					return c.ID
				}
			}
		}
	}
	return 0
}

// bookRenewal creates the transaction for what a renewal cost, as the
// person, and remembers it on the history entry. Errors come back as text;
// the renewal itself is kept.
func (s *Server) bookRenewal(ctx context.Context, uid int64, r *store.Renewal, h *store.History) moneyResult {
	s.moneyMu.Lock()
	defer s.moneyMu.Unlock()
	if h.MoneyTxID != nil {
		return moneyResult{OK: true, TxID: *h.MoneyTxID, Skipped: true}
	}
	if h.Cost <= 0 {
		return moneyResult{Skipped: true}
	}
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	c := s.clients(cctx, uid)
	conn, err := c.conn(cctx, appMoney, scopeAccountsRead, scopeTxWrite)
	if err != nil {
		return moneyResult{Error: err.Error()}
	}
	accts, err := conn.client.Accounts(cctx)
	if err != nil {
		return moneyResult{Error: "Pocket Money: " + err.Error(), Via: conn.via}
	}
	acct := pickAccount(accts, 0, c.st.MoneyAccountID)
	if acct == nil {
		return moneyResult{Error: "Pocket Money has no account you can book on", Via: conn.via}
	}
	cats, _ := conn.client.Categories(cctx, acct.ID)
	desc := "Renewed: " + r.Title
	if r.Holder != "" && !strings.Contains(strings.ToLower(r.Title), strings.ToLower(r.Holder)) {
		desc += " (" + r.Holder + ")"
	}
	tx := pocket.Tx{AccountID: acct.ID, CategoryID: pickCategory(cats, 0, c.st.MoneyCategoryID, r.Group), Date: h.RenewedOn, Amount: h.Cost, Currency: h.Currency,
		Description: desc, Payee: r.Provider, Tags: []string{"renewal"},
		Notes: fmt.Sprintf("Pocket Renewals — now valid until %s. %s", h.NewExpires, h.Note), ExternalID: fmt.Sprintf("renewals:history:%d", h.ID)}
	id, err := conn.client.CreateTx(cctx, tx)
	if err != nil {
		return moneyResult{Error: "Pocket Money: " + err.Error(), Via: conn.via}
	}
	_ = s.store.SetHistoryMoney(ctx, uid, h.ID, &id)
	h.MoneyTxID = &id
	return moneyResult{OK: true, TxID: id, Account: acct.Name, Via: conn.via, URL: strings.TrimRight(conn.public, "/") + "/transactions/" + itoa(id)}
}
