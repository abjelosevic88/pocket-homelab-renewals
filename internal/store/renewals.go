package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
)

const renewalCols = `r.id, r.user_id, r.title, r.kind, r.holder, r.provider, r.number, r.issued_on, r.expires_on, r.renew_n, r.renew_unit, r.auto_renews,
	r.remind_days, r.lead_days, r.cost, r.currency, r.renew_url, r.notes, r.document_id, r.document_title, r.contact_id, r.contact_name, r.archived,
	r.needs_check, r.source, r.source_ref, r.source_url, r.found_by, r.notified_days, r.notified_on, r.cal_event_id, r.cal_synced, r.task_id,
	r.created_at, r.updated_at, (SELECT COUNT(1) FROM history h WHERE h.renewal_id = r.id)`

func scanRenewal(row interface{ Scan(...any) error }) (*Renewal, error) {
	var r Renewal
	var cost, doc, contact, cal, task sql.NullInt64
	var notified sql.NullInt64
	var auto, arch, check int
	if err := row.Scan(&r.ID, &r.UserID, &r.Title, &r.Kind, &r.Holder, &r.Provider, &r.Number, &r.IssuedOn, &r.ExpiresOn, &r.RenewN, &r.RenewUnit, &auto,
		&r.RemindDays, &r.LeadDays, &cost, &r.Currency, &r.RenewURL, &r.Notes, &doc, &r.DocumentTitle, &contact, &r.ContactName, &arch,
		&check, &r.Source, &r.SourceRef, &r.SourceURL, &r.FoundBy, &notified, &r.NotifiedOn, &cal, &r.CalSynced, &task,
		&r.CreatedAt, &r.UpdatedAt, &r.Renewals); err != nil {
		return nil, notFound(err)
	}
	r.AutoRenews, r.Archived, r.NeedsCheck = auto == 1, arch == 1, check == 1
	r.Cost, r.DocumentID, r.ContactID, r.CalEventID, r.TaskID = ptr(cost), ptr(doc), ptr(contact), ptr(cal), ptr(task)
	if notified.Valid {
		n := int(notified.Int64)
		r.NotifiedDays = &n
	}
	r.derive(Today())
	return &r, nil
}

func ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// RenewalFilter narrows a list.
type RenewalFilter struct {
	Q        string
	Kind     string
	Group    string
	Holder   string
	Status   string // expired | soon | ok | undated | archived | check | "" (everything not archived)
	Source   string
	Before   string // expires on or before
	After    string // expires on or after
	Archived bool   // archived ones too
}

// ListRenewals lists a person's renewals, soonest first (no date last).
func (s *Store) ListRenewals(ctx context.Context, uid int64, f RenewalFilter) ([]Renewal, error) {
	q := `SELECT ` + renewalCols + ` FROM renewals r WHERE r.user_id = ?`
	args := []any{uid}
	if f.Status != StatusArchived && !f.Archived {
		q += ` AND r.archived = 0`
	}
	if f.Status == StatusArchived {
		q += ` AND r.archived = 1`
	}
	if f.Kind != "" {
		q += ` AND r.kind = ?`
		args = append(args, f.Kind)
	}
	if f.Holder != "" {
		q += ` AND r.holder = ? COLLATE NOCASE`
		args = append(args, f.Holder)
	}
	if f.Source != "" {
		q += ` AND r.source = ?`
		args = append(args, f.Source)
	}
	if f.Before != "" {
		q += ` AND r.expires_on <> '' AND r.expires_on <= ?`
		args = append(args, f.Before)
	}
	if f.After != "" {
		q += ` AND r.expires_on >= ?`
		args = append(args, f.After)
	}
	if t := strings.TrimSpace(f.Q); t != "" {
		like := "%" + strings.ToLower(t) + "%"
		q += ` AND (lower(r.title) LIKE ? OR lower(r.holder) LIKE ? OR lower(r.provider) LIKE ? OR lower(r.number) LIKE ? OR lower(r.notes) LIKE ?)`
		args = append(args, like, like, like, like, like)
	}
	q += ` ORDER BY r.expires_on = '', r.expires_on, lower(r.title)`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Renewal{}
	for rows.Next() {
		r, err := scanRenewal(rows)
		if err != nil {
			return nil, err
		}
		if f.Group != "" && r.Group != f.Group {
			continue
		}
		switch f.Status {
		case "", StatusArchived:
		case "check":
			if !r.NeedsCheck {
				continue
			}
		default:
			if r.Status != f.Status {
				continue
			}
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// GetRenewal reads one of a person's renewals.
func (s *Store) GetRenewal(ctx context.Context, uid, id int64) (*Renewal, error) {
	return scanRenewal(s.DB.QueryRowContext(ctx, `SELECT `+renewalCols+` FROM renewals r WHERE r.user_id = ? AND r.id = ?`, uid, id))
}

// RenewalBySource finds the renewal made from a source item.
func (s *Store) RenewalBySource(ctx context.Context, uid int64, ref string) (*Renewal, error) {
	if ref == "" {
		return nil, ErrNotFound
	}
	return scanRenewal(s.DB.QueryRowContext(ctx, `SELECT `+renewalCols+` FROM renewals r WHERE r.user_id = ? AND r.source_ref = ?`, uid, ref))
}

func (r *Renewal) clean() {
	r.Title = strings.TrimSpace(r.Title)
	if !ValidKind(r.Kind) {
		r.Kind = "other"
	}
	if !ValidUnit(r.RenewUnit) {
		r.RenewUnit = "year"
	}
	if r.RenewN < 0 {
		r.RenewN = 0
	}
	if r.LeadDays < 0 {
		r.LeadDays = 0
	}
	r.RemindDays = JoinRemind(ParseRemind(r.RemindDays))
	if r.Currency == "" {
		r.Currency = "EUR"
	}
	if r.Source == "" {
		r.Source = "web"
	}
}

// CreateRenewal adds a renewal.
func (s *Store) CreateRenewal(ctx context.Context, uid int64, r *Renewal) error {
	r.clean()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO renewals (user_id, title, kind, holder, provider, number, issued_on, expires_on, renew_n, renew_unit, auto_renews,
		remind_days, lead_days, cost, currency, renew_url, notes, document_id, document_title, contact_id, contact_name, archived, needs_check, source, source_ref,
		source_url, found_by) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		uid, r.Title, r.Kind, r.Holder, r.Provider, r.Number, r.IssuedOn, r.ExpiresOn, r.RenewN, r.RenewUnit, boolInt(r.AutoRenews),
		r.RemindDays, r.LeadDays, nullCost(r.Cost), r.Currency, r.RenewURL, r.Notes, nullI(r.DocumentID), r.DocumentTitle, nullI(r.ContactID), r.ContactName,
		boolInt(r.Archived), boolInt(r.NeedsCheck), r.Source, r.SourceRef, r.SourceURL, r.FoundBy)
	if err != nil {
		return conflict(err)
	}
	r.ID, _ = res.LastInsertId()
	got, err := s.GetRenewal(ctx, uid, r.ID)
	if err != nil {
		return err
	}
	// what is already due when it is added was just seen: remind from the next step on
	if at := got.Stage(); at != nil {
		_ = s.SetNotified(ctx, uid, got.ID, *at, Today())
	}
	*r = *got
	return nil
}

func nullCost(c *int64) any {
	if c == nil {
		return nil
	}
	return *c
}

// UpdateRenewal saves the editable fields. A changed date resets the
// reminders and the calendar event.
func (s *Store) UpdateRenewal(ctx context.Context, uid int64, r *Renewal) error {
	r.clean()
	res, err := s.DB.ExecContext(ctx, `UPDATE renewals SET title = ?, kind = ?, holder = ?, provider = ?, number = ?, issued_on = ?,
		notified_days = CASE WHEN expires_on <> ? THEN NULL ELSE notified_days END, cal_synced = '',
		expires_on = ?, renew_n = ?, renew_unit = ?, auto_renews = ?, remind_days = ?, lead_days = ?, cost = ?, currency = ?, renew_url = ?, notes = ?,
		document_id = ?, document_title = ?, contact_id = ?, contact_name = ?, archived = ?, needs_check = ?, source_url = ?, updated_at = ?
		WHERE user_id = ? AND id = ?`,
		r.Title, r.Kind, r.Holder, r.Provider, r.Number, r.IssuedOn, r.ExpiresOn,
		r.ExpiresOn, r.RenewN, r.RenewUnit, boolInt(r.AutoRenews), r.RemindDays, r.LeadDays, nullCost(r.Cost), r.Currency, r.RenewURL, r.Notes,
		nullI(r.DocumentID), r.DocumentTitle, nullI(r.ContactID), r.ContactName, boolInt(r.Archived), boolInt(r.NeedsCheck), r.SourceURL, Now(), uid, r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	got, err := s.GetRenewal(ctx, uid, r.ID)
	if err == nil {
		*r = *got
	}
	return err
}

// DeleteRenewal removes a renewal; one that was found in another app is
// remembered, so the next scan does not bring it back.
func (s *Store) DeleteRenewal(ctx context.Context, uid, id int64) (*Renewal, error) {
	r, err := s.GetRenewal(ctx, uid, id)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if r.SourceRef != "" {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO dismissed (user_id, source_ref, title) VALUES (?, ?, ?)`, uid, r.SourceRef, r.Title); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM renewals WHERE user_id = ? AND id = ?`, uid, id); err != nil {
		return nil, err
	}
	return r, tx.Commit()
}

// SetChecked marks found renewals as looked at (ids nil = all of them).
func (s *Store) SetChecked(ctx context.Context, uid int64, ids []int64) (int, error) {
	q := `UPDATE renewals SET needs_check = 0, updated_at = ? WHERE user_id = ? AND needs_check = 1`
	args := []any{Now(), uid}
	if ids != nil {
		if len(ids) == 0 {
			return 0, nil
		}
		q += ` AND id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)`
		for _, id := range ids {
			args = append(args, id)
		}
	}
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// SetNotified remembers the last reminder sent.
func (s *Store) SetNotified(ctx context.Context, uid, id int64, days int, on string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE renewals SET notified_days = ?, notified_on = ? WHERE user_id = ? AND id = ?`, days, on, uid, id)
	return err
}

// SetCalendar remembers the renewal's event in Pocket Calendar.
func (s *Store) SetCalendar(ctx context.Context, uid, id int64, event *int64, synced string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE renewals SET cal_event_id = ?, cal_synced = ? WHERE user_id = ? AND id = ?`, nullI(event), synced, uid, id)
	return err
}

// SetTask remembers the task in Pocket Tasks.
func (s *Store) SetTask(ctx context.Context, uid, id int64, task *int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE renewals SET task_id = ? WHERE user_id = ? AND id = ?`, nullI(task), uid, id)
	return err
}

// Renew moves a renewal to its new date and writes the history, in one go.
func (s *Store) Renew(ctx context.Context, uid int64, r *Renewal, h *History) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	h.OldExpires = r.ExpiresOn
	if h.Currency == "" {
		h.Currency = r.Currency
	}
	if h.Source == "" {
		h.Source = "web"
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO history (user_id, renewal_id, renewed_on, old_expires, new_expires, cost, currency, note, money_tx_id, source)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, uid, r.ID, h.RenewedOn, h.OldExpires, h.NewExpires, h.Cost, h.Currency, h.Note, nullI(h.MoneyTxID), h.Source)
	if err != nil {
		return err
	}
	h.ID, _ = res.LastInsertId()
	h.RenewalID = r.ID
	if _, err := tx.ExecContext(ctx, `UPDATE renewals SET expires_on = ?, notified_days = NULL, notified_on = '', cal_synced = '', archived = 0, updated_at = ?
		WHERE user_id = ? AND id = ?`, h.NewExpires, Now(), uid, r.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	got, err := s.GetRenewal(ctx, uid, r.ID)
	if err == nil {
		*r = *got
	}
	return err
}

const historyCols = `h.id, h.user_id, h.renewal_id, h.renewed_on, h.old_expires, h.new_expires, h.cost, h.currency, h.note, h.money_tx_id, h.source, h.created_at, r.title`

// ListHistory lists renewals done, newest first (renewal 0 = all of them).
func (s *Store) ListHistory(ctx context.Context, uid, renewalID int64, limit int) ([]History, error) {
	q := `SELECT ` + historyCols + ` FROM history h JOIN renewals r ON r.id = h.renewal_id WHERE h.user_id = ?`
	args := []any{uid}
	if renewalID > 0 {
		q += ` AND h.renewal_id = ?`
		args = append(args, renewalID)
	}
	q += ` ORDER BY h.renewed_on DESC, h.id DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []History{}
	for rows.Next() {
		var h History
		var tx sql.NullInt64
		if err := rows.Scan(&h.ID, &h.UserID, &h.RenewalID, &h.RenewedOn, &h.OldExpires, &h.NewExpires, &h.Cost, &h.Currency, &h.Note, &tx, &h.Source, &h.CreatedAt, &h.Title); err != nil {
			return nil, err
		}
		h.MoneyTxID = ptr(tx)
		out = append(out, h)
	}
	return out, rows.Err()
}

// GetHistory reads one history entry.
func (s *Store) GetHistory(ctx context.Context, uid, id int64) (*History, error) {
	var h History
	var tx sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT `+historyCols+` FROM history h JOIN renewals r ON r.id = h.renewal_id WHERE h.user_id = ? AND h.id = ?`, uid, id).
		Scan(&h.ID, &h.UserID, &h.RenewalID, &h.RenewedOn, &h.OldExpires, &h.NewExpires, &h.Cost, &h.Currency, &h.Note, &tx, &h.Source, &h.CreatedAt, &h.Title)
	if err != nil {
		return nil, notFound(err)
	}
	h.MoneyTxID = ptr(tx)
	return &h, nil
}

// SetHistoryMoney remembers the Money booking of a renewal.
func (s *Store) SetHistoryMoney(ctx context.Context, uid, id int64, tx *int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE history SET money_tx_id = ? WHERE user_id = ? AND id = ?`, nullI(tx), uid, id)
	return err
}

// DeleteHistory removes one history entry (the renewal keeps its date).
func (s *Store) DeleteHistory(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM history WHERE user_id = ? AND id = ?`, uid, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// InsertHistory adds a history row as it is (imports).
func (s *Store) InsertHistory(ctx context.Context, uid int64, h *History) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO history (user_id, renewal_id, renewed_on, old_expires, new_expires, cost, currency, note, source)
		SELECT ?,?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM renewals WHERE id = ? AND user_id = ?)`,
		uid, h.RenewalID, h.RenewedOn, h.OldExpires, h.NewExpires, h.Cost, h.Currency, h.Note, h.Source, h.RenewalID, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	h.ID, _ = res.LastInsertId()
	return nil
}

// Holders lists the names renewals belong to, most used first.
func (s *Store) Holders(ctx context.Context, uid int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT holder FROM renewals WHERE user_id = ? AND holder <> '' AND archived = 0 GROUP BY holder COLLATE NOCASE ORDER BY COUNT(1) DESC, holder`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ---- found in other apps ----

// IsDismissed reports whether the person deleted what this source item made.
func (s *Store) IsDismissed(ctx context.Context, uid int64, ref string) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM dismissed WHERE user_id = ? AND source_ref = ?`, uid, ref).Scan(&n)
	return n > 0
}

// ListDismissed lists what will not be added again.
func (s *Store) ListDismissed(ctx context.Context, uid int64) ([]map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT source_ref, title, created_at FROM dismissed WHERE user_id = ? ORDER BY created_at DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var ref, title, at string
		if err := rows.Scan(&ref, &title, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]string{"source_ref": ref, "title": title, "created_at": at})
	}
	return out, rows.Err()
}

// Undismiss lets a scan add it again (ref "" = everything).
func (s *Store) Undismiss(ctx context.Context, uid int64, ref string) error {
	if ref == "" {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM dismissed WHERE user_id = ?`, uid)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM dismissed WHERE user_id = ? AND source_ref = ?`, uid, ref)
	return err
}

// Found is one date that runs out, seen in another app.
type Found struct {
	Ref        string // unique per person: "<source>:<what>:<id>"
	Source     string
	Title      string
	Kind       string
	Holder     string
	Provider   string
	Number     string
	IssuedOn   string
	ExpiresOn  string
	AutoRenews bool
	RenewN     int
	RenewUnit  string
	Cost       *int64
	Currency   string
	SourceURL  string
	FoundBy    string
	DocumentID *int64
	DocTitle   string
	Unsure     bool // read by AI or guessed: shown as "check this"
}

// Upsert results.
const (
	UpsertAdded     = "added"
	UpsertUpdated   = "updated"
	UpsertSame      = "same"
	UpsertDismissed = "dismissed"
)

// UpsertFound adds what a source found, or moves the date of what it found
// before when the source now has a later one (renewed there). What the
// person edited stays theirs: only the date moves on, with a history entry.
func (s *Store) UpsertFound(ctx context.Context, uid int64, f Found) (string, *Renewal, error) {
	if s.IsDismissed(ctx, uid, f.Ref) {
		return UpsertDismissed, nil, nil
	}
	have, err := s.RenewalBySource(ctx, uid, f.Ref)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return "", nil, err
	}
	if have != nil {
		if f.ExpiresOn == "" || f.ExpiresOn <= have.ExpiresOn {
			return UpsertSame, have, nil
		}
		h := &History{RenewedOn: Today(), NewExpires: f.ExpiresOn, Note: "New date found in " + sourceName(f.Source), Source: f.Source}
		if err := s.Renew(ctx, uid, have, h); err != nil {
			return "", nil, err
		}
		// the link back follows what the date came from (a newer vaccine, a newer paper)
		if f.SourceURL != "" && f.SourceURL != have.SourceURL {
			if _, err := s.DB.ExecContext(ctx, `UPDATE renewals SET source_url = ? WHERE user_id = ? AND id = ?`, f.SourceURL, uid, have.ID); err == nil {
				have.SourceURL = f.SourceURL
			}
		}
		return UpsertUpdated, have, nil
	}
	k := KindOf(f.Kind)
	r := &Renewal{Title: f.Title, Kind: f.Kind, Holder: f.Holder, Provider: f.Provider, Number: f.Number, IssuedOn: f.IssuedOn, ExpiresOn: f.ExpiresOn,
		RenewN: f.RenewN, RenewUnit: f.RenewUnit, AutoRenews: f.AutoRenews, Cost: f.Cost, Currency: f.Currency, Source: f.Source, SourceRef: f.Ref,
		SourceURL: f.SourceURL, FoundBy: f.FoundBy, DocumentID: f.DocumentID, DocumentTitle: f.DocTitle, NeedsCheck: true}
	if r.RenewN == 0 && f.RenewUnit == "" {
		r.RenewN, r.RenewUnit = k.TermN, k.TermU
	}
	if err := s.CreateRenewal(ctx, uid, r); err != nil {
		if errors.Is(err, ErrConflict) {
			return UpsertSame, nil, nil
		}
		return "", nil, err
	}
	return UpsertAdded, r, nil
}

// SourceNames are the apps a renewal can be found in.
var SourceNames = map[string]string{"documents": "Pocket Documents", "home": "Pocket Home", "garage": "Pocket Garage", "inventory": "Pocket Inventory",
	"monitoring": "Pocket Monitoring", "money": "Pocket Money", "wealth": "Pocket Wealth", "pets": "Pocket Pets", "email": "Pocket Email"}

func sourceName(src string) string {
	if n, ok := SourceNames[src]; ok {
		return n
	}
	return src
}

// ---- scans ----

// StartScan records the start of a scan.
func (s *Store) StartScan(ctx context.Context, uid int64, source, trigger string) (*Scan, error) {
	sc := &Scan{Source: source, Trigger: trigger, StartedAt: Now()}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO scans (user_id, source, trigger, started_at) VALUES (?,?,?,?)`, uid, source, trigger, sc.StartedAt)
	if err != nil {
		return nil, err
	}
	sc.ID, _ = res.LastInsertId()
	return sc, nil
}

// FinishScan stores a scan's result and keeps the last 200 per person.
func (s *Store) FinishScan(ctx context.Context, uid int64, sc *Scan) error {
	sc.FinishedAt = Now()
	if _, err := s.DB.ExecContext(ctx, `UPDATE scans SET finished_at = ?, looked = ?, found = ?, added = ?, updated = ?, error = ? WHERE user_id = ? AND id = ?`,
		sc.FinishedAt, sc.Looked, sc.Found, sc.Added, sc.Updated, sc.Error, uid, sc.ID); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM scans WHERE user_id = ? AND id NOT IN (SELECT id FROM scans WHERE user_id = ? ORDER BY id DESC LIMIT 200)`, uid, uid)
	return err
}

// DeleteScan forgets a scan (a live document that turned up nothing).
func (s *Store) DeleteScan(ctx context.Context, uid, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM scans WHERE user_id = ? AND id = ?`, uid, id)
	return err
}

// ListScans lists recent scans, newest first.
func (s *Store) ListScans(ctx context.Context, uid int64, limit int) ([]Scan, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, source, trigger, started_at, finished_at, looked, found, added, updated, error FROM scans
		WHERE user_id = ? ORDER BY id DESC LIMIT ?`, uid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Scan{}
	for rows.Next() {
		var sc Scan
		if err := rows.Scan(&sc.ID, &sc.UserID, &sc.Source, &sc.Trigger, &sc.StartedAt, &sc.FinishedAt, &sc.Looked, &sc.Found, &sc.Added, &sc.Updated, &sc.Error); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// LastScans is the newest finished scan per source.
func (s *Store) LastScans(ctx context.Context, uid int64) (map[string]Scan, error) {
	all, err := s.ListScans(ctx, uid, 200)
	if err != nil {
		return nil, err
	}
	out := map[string]Scan{}
	for _, sc := range all {
		if _, ok := out[sc.Source]; !ok && sc.FinishedAt != "" && sc.Trigger != "event" {
			out[sc.Source] = sc
		}
	}
	return out, nil
}

// SortByUrgency orders renewals: expired, then soonest.
func SortByUrgency(list []Renewal) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if StatusRank(a.Status) != StatusRank(b.Status) {
			return StatusRank(a.Status) < StatusRank(b.Status)
		}
		return a.ExpiresOn < b.ExpiresOn
	})
}
