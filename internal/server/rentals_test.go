package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/store"
)

// fakeRentals answers as one person's Pocket Rentals; its leases can change
// between scans.
type fakeRentals struct {
	mu     sync.Mutex
	leases []map[string]any
	asked  []string
	ts     *httptest.Server
}

func newFakeRentals(t *testing.T) *fakeRentals {
	fr := &fakeRentals{}
	mux := http.NewServeMux()
	j := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { j(w, map[string]any{"status": "ok", "version": "0.1.0"}) })
	mux.HandleFunc("/api/v1/leases", func(w http.ResponseWriter, r *http.Request) {
		fr.mu.Lock()
		defer fr.mu.Unlock()
		fr.asked = append(fr.asked, r.URL.Query().Get("status"))
		out := []map[string]any{}
		for _, l := range fr.leases {
			if st := r.URL.Query().Get("status"); st == "" || l["status"] == st {
				out = append(out, l)
			}
		}
		j(w, out)
	})
	fr.ts = httptest.NewServer(mux)
	t.Cleanup(fr.ts.Close)
	return fr
}

func (fr *fakeRentals) set(leases []map[string]any) {
	fr.mu.Lock()
	fr.leases = leases
	fr.mu.Unlock()
}

func rentalsFixture(today string) []map[string]any {
	day := func(n int) string { return store.AddDays(today, n) }
	ana := []map[string]any{{"name": "Ana Horvat", "email": "ana@example.com"}, {"name": "Ivo Horvat", "email": ""}}
	return []map[string]any{
		{"id": 1, "status": "active", "start_date": day(-200), "end_date": day(165), "notice_days": 60, "rent": 65000, "currency": "EUR", "frequency": "monthly",
			"unit_name": "Flat 2", "property_id": 1, "property_name": "Ilica 10", "tenants": ana, "title": "Flat 2, Ilica 10 — Ana Horvat"},
		{"id": 2, "status": "active", "start_date": day(-100), "end_date": day(80), "notice_days": 0, "rent": 120000, "currency": "EUR", "frequency": "monthly",
			"unit_name": "Whole place", "property_id": 2, "property_name": "Sea house", "tenants": []map[string]any{}},
		{"id": 3, "status": "active", "start_date": day(-400), "end_date": "", "notice_days": 30, "unit_name": "Room", "property_name": "Ilica 10"},         // until notice
		{"id": 4, "status": "ended", "start_date": day(-900), "end_date": day(-30), "notice_days": 30, "unit_name": "Flat 1", "property_name": "Ilica 10"},  // ended
		{"id": 5, "status": "draft", "start_date": day(10), "end_date": day(375), "notice_days": 30, "unit_name": "Flat 3", "property_name": "Ilica 10"},    // a draft
		{"id": 6, "status": "active", "start_date": day(-800), "end_date": day(-400), "notice_days": 30, "unit_name": "Shed", "property_name": "Sea house"}, // long gone
	}
}

func rentalsByRef(t *testing.T, a *app, auth string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, x := range a.list(auth, "/api/v1/renewals?source=rentals") {
		m := x.(map[string]any)
		out[m["source_ref"].(string)] = m
	}
	return out
}

func TestRentalsScan(t *testing.T) {
	a := newApp(t, nil)
	ctx := context.Background()
	uid := a.ids["Alice"]
	today := store.Today()
	fr := newFakeRentals(t)
	fr.set(rentalsFixture(today))
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"rentals_mode": "direct", "rentals_url": fr.ts.URL, "rentals_token": "tok-rentals-123456"})
	src, ok := sourceByName("rentals")
	if !ok || src.App.slug != "rentals" {
		t.Fatalf("no rentals source")
	}
	sc, err := a.s.runScan(ctx, uid, src, "manual", 0)
	if err != nil || sc.Added != 2 {
		t.Fatalf("rentals scan: %+v %v", sc, err)
	}
	if len(fr.asked) == 0 || fr.asked[0] != "active" {
		t.Fatalf("asked for leases with status %v", fr.asked)
	}
	got := rentalsByRef(t, a, a.alice)
	if len(got) != 2 {
		t.Fatalf("found: %v", got)
	}
	l := got["rentals:lease:1"]
	if l == nil || l["kind"] != "contract" || l["title"] != "Lease ends — Flat 2, Ilica 10 (Ana Horvat, Ivo Horvat)" || l["holder"] != "Ana Horvat, Ivo Horvat" ||
		l["provider"] != "Ilica 10" || l["expires_on"] != store.AddDays(today, 165) || l["issued_on"] != store.AddDays(today, -200) ||
		l["lead_days"] != float64(60) || l["act_by"] != store.AddDays(today, 105) || l["source_url"] != fr.ts.URL+"/leases/1" ||
		l["needs_check"] != true || l["found_by"] != "the end of a lease in Pocket Rentals, with 60 days' notice" || l["auto_renews"] == true {
		t.Fatalf("the lease: %v", l)
	}
	if l["renew_unit"] != "year" || l["renew_n"] != float64(1) {
		t.Fatalf("the lease's term: %v %v", l["renew_n"], l["renew_unit"])
	}
	if w := got["rentals:lease:2"]; w["title"] != "Lease ends — Sea house" || w["lead_days"] != float64(0) || w["renew_unit"] != "month" || w["renew_n"] != float64(6) {
		t.Fatalf("the whole place: %v", w)
	}

	// again: nothing new
	if sc, _ := a.s.runScan(ctx, uid, src, "manual", 0); sc.Added != 0 || sc.Updated != 0 {
		t.Fatalf("second scan: %+v", sc)
	}

	// renewed in Rentals: the later end moves it on here
	leases := rentalsFixture(today)
	leases[0]["end_date"] = store.AddDays(today, 530)
	fr.set(leases)
	if sc, err := a.s.runScan(ctx, uid, src, "manual", 0); err != nil || sc.Added != 0 || sc.Updated != 1 {
		t.Fatalf("after the renewal: %+v %v", sc, err)
	}
	if got := rentalsByRef(t, a, a.alice); len(got) != 2 || got["rentals:lease:1"]["expires_on"] != store.AddDays(today, 530) {
		t.Fatalf("after the renewal: %v", got)
	}

	// Bob has no Rentals connection of his own and sees none of Alice's
	if _, err := a.s.runScan(ctx, a.ids["Bob"], src, "manual", 0); err == nil {
		t.Fatalf("bob scanned with alice's connection")
	}
	if n := len(a.list(a.bob, "/api/v1/renewals")); n != 0 {
		t.Fatalf("bob sees %d renewals", n)
	}
}

func TestRentalsEventsRescan(t *testing.T) {
	liveDelay = 10 * time.Millisecond
	a := newApp(t, nil)
	today := store.Today()
	fr := newFakeRentals(t)
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"rentals_mode": "direct", "rentals_url": fr.ts.URL, "rentals_token": "tok-rentals-123456"})
	fr.set(rentalsFixture(today))
	if eventSources["rentals.lease_started"] != "rentals" || !strings.Contains(streamTopics(), "rentals.lease_started") {
		t.Fatalf("rentals.lease_started is not followed: %s", streamTopics())
	}
	ev := func(topic, email string) core.Event {
		p, _ := json.Marshal(map[string]any{"user_email": email, "lease_id": 1, "url": "/leases/1"})
		return core.Event{ID: 1, Topic: topic, Payload: p, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	}
	ctx := context.Background()
	a.s.handleEvent(ctx, ev("rentals.rent_due", "alice@example.com"))       // not followed
	a.s.handleEvent(ctx, ev("rentals.lease_started", "nobody@example.com")) // not a person here
	a.s.handleEvent(ctx, ev("rentals.lease_started", "alice@example.com"))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if l := a.list(a.alice, "/api/v1/renewals?source=rentals"); len(l) == 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the leases did not arrive: %v", a.list(a.alice, "/api/v1/renewals"))
}

func TestRentalsLinkDeclared(t *testing.T) {
	app, ok := appBySlug("rentals")
	if !ok || len(app.scopes) != 2 || app.scopes[0] != scopeRentalsRead || app.scopes[1] != scopeEventsRead || app.reason == "" {
		t.Fatalf("rentals app: %+v", app)
	}
	for _, u := range linkUses() {
		if u.App == "rentals" {
			return
		}
	}
	t.Fatalf("rentals is not in the manifest's Uses")
}
