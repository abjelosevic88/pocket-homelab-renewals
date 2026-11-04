package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-renewals/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/store"
)

// ---------- Pocket Calendar: expiry dates as all-day events ----------
//
// With "Show on my calendar" on, every renewal with a date has one all-day
// event on the day it runs out in the person's Pocket Calendar. A renewal
// remembers what its event shows (cal_synced); any change clears that and
// the next sync (right after the change, and every ten minutes) moves the
// event. Archived and deleted renewals lose theirs. Calendar lets an app
// change only the events it created, so the person's own are never touched.

func calendarText(r store.Renewal) (title, desc string) {
	title = displayName(r) + " runs out"
	desc = store.KindOf(r.Kind).Label + " (Pocket Renewals)."
	if r.Provider != "" {
		desc += " " + r.Provider + "."
	}
	if r.ActBy != "" {
		desc += " Act by " + r.ActBy + "."
	}
	if r.AutoRenews {
		desc += " Renews by itself."
	}
	if r.RenewURL != "" {
		desc += "\nRenew at " + r.RenewURL
	}
	if r.Notes != "" {
		desc += "\n\n" + r.Notes
	}
	return title, desc
}

func syncedKey(r store.Renewal, calID int64) string {
	t, d := calendarText(r)
	return fmt.Sprintf("%d|%s|%s|%x", calID, r.ExpiresOn, t, len(d))
}

// syncCalendarSoon syncs one person's calendar in the background.
func (s *Server) syncCalendarSoon(uid int64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = s.syncCalendar(ctx, uid)
	}()
}

func (s *Server) syncAllCalendars(ctx context.Context) {
	ids, err := s.store.SettingsUsers(ctx)
	if err != nil {
		return
	}
	for _, uid := range ids {
		if _, err := s.syncCalendar(ctx, uid); err != nil && !isNotConnected(err) {
			s.log.Debug("calendar sync", "user", uid, "err", err)
		}
	}
}

// syncCalendar brings one person's events in line with their renewals and
// says how many it created, moved and removed.
func (s *Server) syncCalendar(ctx context.Context, uid int64) (map[string]int, error) {
	s.calMu.Lock()
	defer s.calMu.Unlock()
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil {
		return nil, err
	}
	list, err := s.store.ListRenewals(ctx, uid, store.RenewalFilter{Archived: true})
	if err != nil {
		return nil, err
	}
	n := map[string]int{"created": 0, "moved": 0, "removed": 0, "failed": 0}
	live := func(r store.Renewal) bool { return st.CalendarSync && !r.Archived && r.ExpiresOn != "" }
	var work []store.Renewal
	for _, r := range list {
		if (live(r) && r.CalSynced != syncedKey(r, st.CalendarID)) || (!live(r) && r.CalEventID != nil) {
			work = append(work, r)
		}
	}
	if len(work) == 0 {
		return n, nil
	}
	conn, err := s.clients(ctx, uid).conn(ctx, appCalendar, scopeCalendarWrite)
	if err != nil {
		return n, err
	}
	for _, r := range work {
		if !live(r) {
			if err := conn.client.DeleteEvent(ctx, *r.CalEventID); err != nil {
				n["failed"]++
				continue
			}
			_ = s.store.SetCalendar(ctx, uid, r.ID, nil, "")
			n["removed"]++
			continue
		}
		title, desc := calendarText(r)
		ev := pocket.Event{CalendarID: st.CalendarID, Title: title, Description: desc, URL: s.cfg.BaseURL + "/r/" + itoa(r.ID), Date: r.ExpiresOn}
		if r.CalEventID != nil {
			err := conn.client.UpdateEvent(ctx, *r.CalEventID, ev)
			if err == nil {
				_ = s.store.SetCalendar(ctx, uid, r.ID, r.CalEventID, syncedKey(r, st.CalendarID))
				n["moved"]++
				continue
			}
			if !errors.Is(err, pocket.ErrNotFound) {
				n["failed"]++
				continue
			}
			// deleted in Calendar: make a new one
		}
		id, err := conn.client.CreateEvent(ctx, ev)
		if err != nil || id == 0 {
			n["failed"]++
			continue
		}
		_ = s.store.SetCalendar(ctx, uid, r.ID, &id, syncedKey(r, st.CalendarID))
		n["created"]++
	}
	if n["failed"] > 0 {
		return n, fmt.Errorf("%d calendar changes failed", n["failed"])
	}
	return n, nil
}

// dropCalendarEvents removes the events of renewals that are gone (best effort).
func (s *Server) dropCalendarEvents(uid int64, list []store.Renewal) {
	var ids []int64
	for _, r := range list {
		if r.CalEventID != nil {
			ids = append(ids, *r.CalEventID)
		}
	}
	if len(ids) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		conn, err := s.clients(ctx, uid).conn(ctx, appCalendar, scopeCalendarWrite)
		if err != nil {
			return
		}
		for _, id := range ids {
			if err := conn.client.DeleteEvent(ctx, id); err != nil {
				s.log.Debug("calendar: removing an event failed", "event", id, "err", err)
			}
		}
	}()
}

// handleCalendarSync runs the sync now (Settings → Connected apps).
func (s *Server) handleCalendarSync(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	n, err := s.syncCalendar(ctx, actorID(ctx))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "counts": n})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "counts": n})
}

// ---------- pickers: what the other apps have ----------

// pick answers a picker: the items, or why the app is not reachable.
func pick(w http.ResponseWriter, items any, err error, app pocketApp) {
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": !isNotConnected(err), "detail": app.name + ": " + strings.TrimPrefix(err.Error(), app.name+" "), "items": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "items": items})
}

func (s *Server) handlePickMoneyAccounts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appMoney, scopeAccountsRead)
	if err != nil {
		pick(w, nil, err, appMoney)
		return
	}
	accts, err := conn.client.Accounts(ctx)
	pick(w, accts, err, appMoney)
}

func (s *Server) handlePickMoneyCategories(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appMoney, scopeAccountsRead)
	if err != nil {
		pick(w, nil, err, appMoney)
		return
	}
	cats, err := conn.client.Categories(ctx, int64(queryInt(r, "account_id")))
	pick(w, cats, err, appMoney)
}

func (s *Server) handlePickCalendars(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appCalendar, scopeCalendarRead)
	if err != nil {
		pick(w, nil, err, appCalendar)
		return
	}
	cals, err := conn.client.Calendars(ctx)
	pick(w, cals, err, appCalendar)
}

func (s *Server) handlePickContacts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"connected": true, "items": []any{}})
		return
	}
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appContacts, scopeContactsRead)
	if err != nil {
		pick(w, nil, err, appContacts)
		return
	}
	cs, err := conn.client.SearchContacts(ctx, q, 12)
	pick(w, cs, err, appContacts)
}

func (s *Server) handlePickDocuments(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appDocuments, scopeDocumentsRead)
	if err != nil {
		pick(w, nil, err, appDocuments)
		return
	}
	docs, err := conn.client.SearchDocuments(ctx, strings.TrimSpace(r.URL.Query().Get("q")), 15)
	pick(w, docs, err, appDocuments)
}
