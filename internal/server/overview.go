package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-renewals/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/store"
)

// handleOverview is the start page: what has run out, what is coming up,
// what was found and needs a look, trips a document does not cover, and
// what was renewed lately.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	all, err := s.store.ListRenewals(ctx, uid, store.RenewalFilter{})
	if err != nil {
		s.fail(w, err, "overview")
		return
	}
	window := st.SoonDays
	if window <= 0 {
		window = 90
	}
	var expired, soon, check, later []store.Renewal
	counts := map[string]int{"total": len(all), "expired": 0, "soon": 0, "check": 0, "undated": 0, "auto": 0}
	groups := map[string]int{}
	yearCost := map[string]int64{}
	for _, rn := range all {
		groups[rn.Group]++
		if rn.NeedsCheck {
			check = append(check, rn)
			counts["check"]++
		}
		if rn.AutoRenews {
			counts["auto"]++
		}
		switch {
		case rn.Status == store.StatusExpired:
			expired = append(expired, rn)
			counts["expired"]++
		case rn.Status == store.StatusUndated:
			counts["undated"]++
		case rn.DaysLeft <= window || rn.Status == store.StatusSoon:
			soon = append(soon, rn)
			counts["soon"]++
		case rn.DaysLeft <= 366 && len(later) < 8:
			later = append(later, rn)
		}
		if rn.Cost != nil && rn.RenewN > 0 && !rn.Archived {
			yearCost[rn.Currency] += perYear(*rn.Cost, rn.RenewN, rn.RenewUnit)
		}
	}
	if len(check) > 12 {
		check = check[:12]
	}
	recent, _ := s.store.ListHistory(ctx, uid, 0, 6)
	writeJSON(w, http.StatusOK, map[string]any{
		"counts": counts, "groups": groups, "window": window,
		"expired": s.dress(ctx, uid, expired), "soon": s.dress(ctx, uid, soon), "check": s.dress(ctx, uid, check), "later": s.dress(ctx, uid, later),
		"recent": recent, "travel": s.travelWarnings(ctx, uid, all), "year_cost": yearCost,
	})
}

// perYear is what a cost every n units comes to in a year (minor units).
func perYear(cost int64, n int, unit string) int64 {
	days := map[string]float64{"day": 1, "week": 7, "month": 30.4375, "year": 365.25}[unit] * float64(n)
	if days <= 0 {
		return 0
	}
	return int64(float64(cost) * 365.25 / days)
}

// handleDue lists what runs out within ?days= (default 30) plus what has run out.
func (s *Server) handleDue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	days := queryInt(r, "days")
	if days <= 0 || days > 3650 {
		days = 30
	}
	list, err := s.store.ListRenewals(ctx, actorID(ctx), store.RenewalFilter{Before: store.AddDays(store.Today(), days)})
	if err != nil {
		s.fail(w, err, "due")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "renewals": s.dress(ctx, actorID(ctx), list)})
}

// handleDay is one date for a journal: what ran out and what was renewed.
func (s *Server) handleDay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	date := chi.URLParam(r, "date")
	if !validDate(date) {
		writeErr(w, http.StatusBadRequest, "the date is YYYY-MM-DD")
		return
	}
	ends, _ := s.store.ListRenewals(ctx, uid, store.RenewalFilter{After: date, Before: date, Archived: true})
	hist, _ := s.store.ListHistory(ctx, uid, 0, 500)
	renewed := []store.History{}
	for _, h := range hist {
		if h.RenewedOn == date {
			renewed = append(renewed, h)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": date, "expires": s.dress(ctx, uid, ends), "renewed": renewed})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	list, err := s.store.ListRenewals(ctx, actorID(ctx), store.RenewalFilter{Q: q, Archived: true})
	if err != nil {
		s.fail(w, err, "search")
		return
	}
	if len(list) > 30 {
		list = list[:30]
	}
	writeJSON(w, http.StatusOK, s.dress(ctx, actorID(ctx), list))
}

// ---- Pocket Travel: is the passport good for the trip? ----

// travelWarning: a trip that a travel document does not cover well.
type travelWarning struct {
	TripID      int64  `json:"trip_id"`
	Trip        string `json:"trip"`
	Destination string `json:"destination"`
	Start       string `json:"start"`
	End         string `json:"end"`
	TripURL     string `json:"trip_url,omitempty"`
	RenewalID   int64  `json:"renewal_id"`
	Title       string `json:"title"`
	Holder      string `json:"holder"`
	Kind        string `json:"kind"`
	ExpiresOn   string `json:"expires_on"`
	Level       string `json:"level"` // before (runs out before coming home) | margin (within the months many countries ask for)
	Message     string `json:"message"`
}

// trips are the person's coming trips (cached ten minutes; none when Travel is not connected).
func (s *Server) trips(ctx context.Context, uid int64) ([]pocket.Trip, string) {
	key := fmt.Sprintf("trips:%d", uid)
	if v, at, ok := s.store.CacheGet(ctx, key); ok {
		if t, err := time.Parse(time.RFC3339Nano, at); err == nil && time.Since(t) < 10*time.Minute {
			var out []pocket.Trip
			if json.Unmarshal([]byte(v), &out) == nil {
				return out, s.siblingLink(ctx, uid, appTravel, "")
			}
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, err := s.clients(cctx, uid).conn(cctx, appTravel, scopeTripsRead)
	if err != nil {
		return nil, ""
	}
	all, err := conn.client.Trips(cctx)
	if err != nil {
		return nil, ""
	}
	today := store.Today()
	out := []pocket.Trip{}
	for _, t := range all {
		end := firstNonEmpty(t.EndDate, t.StartDate)
		if t.Archived || end == "" || end < today || t.StartDate > store.AddDays(today, 400) {
			continue
		}
		out = append(out, t)
	}
	b, _ := json.Marshal(out)
	_ = s.store.CachePut(ctx, key, string(b))
	return out, strings.TrimRight(conn.public, "/")
}

func (s *Server) travelCheck(ctx context.Context, uid int64, docs []store.Renewal) []travelWarning {
	out := []travelWarning{}
	var travel []store.Renewal
	for _, d := range docs {
		if !d.Archived && d.ExpiresOn != "" && store.KindOf(d.Kind).Travel {
			travel = append(travel, d)
		}
	}
	if len(travel) == 0 {
		return out
	}
	trips, base := s.trips(ctx, uid)
	if len(trips) == 0 {
		return out
	}
	st, _ := s.store.GetSettings(ctx, uid)
	months := st.TravelMonths
	if months < 0 {
		months = 0
	}
	for _, t := range trips {
		end := firstNonEmpty(t.EndDate, t.StartDate)
		margin := store.AddInterval(end, months, "month")
		for _, d := range travel {
			w := travelWarning{TripID: t.ID, Trip: t.Name, Destination: t.Destination, Start: t.StartDate, End: end, RenewalID: d.ID, Title: d.Title,
				Holder: d.Holder, Kind: d.Kind, ExpiresOn: d.ExpiresOn}
			if base != "" {
				w.TripURL = base + "/trips/" + itoa(t.ID)
			}
			whose := d.Title
			if d.Holder != "" {
				whose = d.Holder + "’s " + strings.ToLower(store.KindOf(d.Kind).Label)
			}
			switch {
			case d.ExpiresOn < end:
				w.Level, w.Message = "before", fmt.Sprintf("%s runs out on %s, before you are back from %s.", whose, d.ExpiresOn, t.Name)
			case months > 0 && d.ExpiresOn < margin:
				w.Level, w.Message = "margin", fmt.Sprintf("%s runs out on %s, less than %d months after %s. Many countries ask for %d months left.", whose, d.ExpiresOn, months, t.Name, months)
			default:
				continue
			}
			out = append(out, w)
		}
	}
	return out
}

func (s *Server) travelWarnings(ctx context.Context, uid int64, all []store.Renewal) []travelWarning {
	return s.travelCheck(ctx, uid, all)
}

func (s *Server) travelWarningsFor(ctx context.Context, uid int64, r *store.Renewal) []travelWarning {
	return s.travelCheck(ctx, uid, []store.Renewal{*r})
}

func (s *Server) handleTravel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all, err := s.store.ListRenewals(ctx, actorID(ctx), store.RenewalFilter{})
	if err != nil {
		s.fail(w, err, "travel")
		return
	}
	trips, _ := s.trips(ctx, actorID(ctx))
	writeJSON(w, http.StatusOK, map[string]any{"trips": trips, "warnings": s.travelWarnings(ctx, actorID(ctx), all)})
}
