package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-renewals/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/store"
)

// pushes runs every minute; once a day, at each person's own hour, it rolls
// on what renews by itself and sends one message with the reminders that
// fell due, what ran out, and what was found in other apps since yesterday.
func (s *Server) pushes(ctx context.Context) {
	ids, err := s.store.SettingsUsers(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	for _, uid := range ids {
		st, err := s.store.GetSettings(ctx, uid)
		if err != nil || now.Hour() != st.NotifyHour || now.Minute() > 10 {
			continue
		}
		key := fmt.Sprintf("push:%d:%s", uid, store.Today())
		if _, _, done := s.store.CacheGet(ctx, key); done {
			continue
		}
		_ = s.store.CachePut(ctx, key, "1")
		s.dailyCheck(ctx, uid, st, now)
	}
}

// rollOn moves what renews by itself past today, with a history entry.
func (s *Server) rollOn(ctx context.Context, uid int64, list []store.Renewal) []store.Renewal {
	today := store.Today()
	for i, r := range list {
		if !r.AutoRenews || r.RenewN <= 0 || r.ExpiresOn == "" || r.ExpiresOn >= today || r.Archived {
			continue
		}
		next := r.ExpiresOn
		for n := 0; next < today && n < 1000; n++ {
			next = store.AddInterval(next, r.RenewN, r.RenewUnit)
		}
		h := &store.History{RenewedOn: r.ExpiresOn, NewExpires: next, Note: "Renewed by itself", Source: "auto"}
		if r.Cost != nil {
			h.Cost = *r.Cost
		}
		rr := r
		if err := s.store.Renew(ctx, uid, &rr, h); err == nil {
			list[i] = rr
			p := renewalPayload(&rr)
			p["old_expires"], p["auto"] = h.OldExpires, true
			s.publish(ctx, uid, "renewals.renewed", "renewal:"+itoa(rr.ID), p)
		}
	}
	return list
}

// whenText is "in 30 days (3 Mar 2027)", "today", "2 days ago".
func whenText(r store.Renewal) string {
	switch d := r.DaysLeft; {
	case d < -1:
		return fmt.Sprintf("ran out %d days ago, on %s", -d, r.ExpiresOn)
	case d == -1:
		return "ran out yesterday"
	case d == 0:
		return "runs out today"
	case d == 1:
		return "runs out tomorrow"
	default:
		return fmt.Sprintf("runs out in %d days, on %s", d, r.ExpiresOn)
	}
}

func displayName(r store.Renewal) string {
	if r.Holder != "" && !strings.Contains(strings.ToLower(r.Title), strings.ToLower(r.Holder)) {
		return r.Title + " (" + r.Holder + ")"
	}
	return r.Title
}

// dailyCheck builds and sends one person's daily message; renewals.due and
// renewals.expired are published whether notifications are on or not.
func (s *Server) dailyCheck(ctx context.Context, uid int64, st store.Settings, now time.Time) (string, bool) {
	today := store.Today()
	all, _ := s.store.ListRenewals(ctx, uid, store.RenewalFilter{})
	all = s.rollOn(ctx, uid, all)
	var lines []string
	worst := "info"
	for _, r := range all {
		at := r.Stage()
		if at == nil || (r.NotifiedDays != nil && *at >= *r.NotifiedDays) {
			continue
		}
		_ = s.store.SetNotified(ctx, uid, r.ID, *at, today)
		p := renewalPayload(&r)
		if *at < 0 {
			s.publish(ctx, uid, "renewals.expired", "renewal:"+itoa(r.ID), p)
			if st.NotifyExpired {
				lines = append(lines, "• "+displayName(r)+" — "+whenText(r))
				worst = "warning"
			}
			continue
		}
		s.publish(ctx, uid, "renewals.due", "renewal:"+itoa(r.ID), p)
		if !st.NotifyReminders && !(st.NotifyExpired && r.DaysLeft == 0) {
			continue
		}
		line := "• " + displayName(r) + " — " + whenText(r)
		if r.ActBy != "" && r.ActBy >= today {
			line += fmt.Sprintf("; act by %s", r.ActBy)
		}
		if r.AutoRenews {
			line += " (renews by itself)"
		}
		lines = append(lines, line)
		if r.DaysLeft <= 7 {
			worst = "warning"
		}
	}
	if st.NotifyFound {
		since := now.Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)
		n := 0
		for _, r := range all {
			if r.NeedsCheck && r.CreatedAt >= since && r.Source != "demo" {
				n++
			}
		}
		if n > 0 {
			lines = append(lines, fmt.Sprintf("• %d new %s found in your other apps — have a look", n, plural2(n, "date", "dates")))
		}
	}
	if len(lines) == 0 || st.NotifyURL == "" {
		return strings.Join(lines, "\n"), false
	}
	body := strings.Join(lines, "\n")
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: "What runs out", Body: body, Priority: worst, Tags: []string{"hourglass"}, Link: s.cfg.BaseURL + "/"}); err != nil {
		s.log.Warn("notify", "user", uid, "err", err)
		return body, false
	}
	return body, true
}

func plural2(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: "Pocket Renewals", Body: "Notifications work. Reminders before something runs out will arrive here, once a day.", Priority: "info", Tags: []string{"hourglass"}}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
