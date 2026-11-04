package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/config"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/store"
)

type app struct {
	t     *testing.T
	url   string
	s     *Server
	st    *store.Store
	alice string
	bob   string
	ids   map[string]int64
}

func newApp(t *testing.T, tweak func(*config.Config)) *app {
	t.Helper()
	cfg := testConfig(t.TempDir())
	cfg.BaseURL = "http://renewals.test"
	cfg.MaxBodyBytes = 64 << 20
	cfg.NotifyHour = 8
	if tweak != nil {
		tweak(cfg)
	}
	st := openTestStore(t, cfg)
	ctx := context.Background()
	a := &app{t: t, st: st, ids: map[string]int64{}}
	for _, u := range []*store.User{{Email: "alice@example.com", Name: "Alice", Role: "admin"}, {Email: "bob@example.com", Name: "Bob", Role: "member"}} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		tok, _, _ := st.CreateAPIToken(ctx, u.ID, "t")
		a.ids[u.Name] = u.ID
		if u.Name == "Alice" {
			a.alice = tok
		} else {
			a.bob = tok
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	a.s = New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := a.s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(a.s.Router())
	t.Cleanup(ts.Close)
	a.url = ts.URL
	return a
}

func (a *app) call(auth, method, path string, body any) (int, map[string]any, []any) {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		j, _ := json.Marshal(body)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, a.url+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	var l []any
	if json.Unmarshal(raw, &m) != nil {
		_ = json.Unmarshal(raw, &l)
	}
	return res.StatusCode, m, l
}

func (a *app) ok(auth, method, path string, body any) map[string]any {
	a.t.Helper()
	code, m, _ := a.call(auth, method, path, body)
	if code >= 300 {
		a.t.Fatalf("%s %s → %d %v", method, path, code, m)
	}
	return m
}

func (a *app) list(auth, path string) []any {
	a.t.Helper()
	code, m, l := a.call(auth, "GET", path, nil)
	if code != 200 {
		a.t.Fatalf("GET %s → %d %v", path, code, m)
	}
	return l
}

func (a *app) expect(want int, auth, method, path string, body any) map[string]any {
	a.t.Helper()
	code, m, _ := a.call(auth, method, path, body)
	if code != want {
		a.t.Fatalf("%s %s → %d, want %d (%v)", method, path, code, want, m)
	}
	return m
}

func num(m map[string]any, k string) float64 { v, _ := m[k].(float64); return v }
func id(m map[string]any) string             { return itoa(int64(num(m, "id"))) }

func TestPerPersonIsolation(t *testing.T) {
	a := newApp(t, nil)
	r := a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Passport", "kind": "passport", "holder": "Alice", "expires_on": store.AddDays(store.Today(), 100)})
	a.ok(a.alice, "POST", "/api/v1/renewals/"+id(r)+"/renew", map[string]any{"expires_on": store.AddDays(store.Today(), 3650), "cost": 5000, "money": false})
	if n := len(a.list(a.bob, "/api/v1/renewals")); n != 0 {
		t.Fatalf("bob sees %d of alice's renewals", n)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/renewals/" + id(r)}, {"PUT", "/api/v1/renewals/" + id(r)}, {"DELETE", "/api/v1/renewals/" + id(r)},
		{"POST", "/api/v1/renewals/" + id(r) + "/renew"},
	} {
		a.expect(404, a.bob, c.method, c.path, map[string]any{"title": "mine now", "expires_on": "2030-01-01"})
	}
	if n := len(a.list(a.bob, "/api/v1/history")); n != 0 {
		t.Fatalf("bob sees alice's history: %d", n)
	}
	h := a.list(a.alice, "/api/v1/history")
	if len(h) != 1 {
		t.Fatalf("alice's history: %v", h)
	}
	hid := itoa(int64(num(h[0].(map[string]any), "id")))
	a.expect(404, a.bob, "DELETE", "/api/v1/history/"+hid, nil)
	a.expect(404, a.bob, "POST", "/api/v1/history/"+hid+"/money", nil)
	if o := a.ok(a.bob, "GET", "/api/v1/overview", nil); num(o["counts"].(map[string]any), "total") != 0 {
		t.Fatalf("bob's overview counts alice's: %v", o["counts"])
	}
	// the admin does not see members' content either
	b := a.ok(a.bob, "POST", "/api/v1/renewals", map[string]any{"title": "Bob's ID", "kind": "id_card"})
	a.expect(404, a.alice, "GET", "/api/v1/renewals/"+id(b), nil)
}

func TestRenewMovesTheDate(t *testing.T) {
	a := newApp(t, nil)
	end := store.AddDays(store.Today(), 10)
	r := a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Car insurance", "kind": "insurance", "expires_on": end, "cost": 41200})
	if r["renew_n"].(float64) != 1 || r["renew_unit"] != "year" || r["status"] != "soon" {
		t.Fatalf("defaults from the kind: %v", r)
	}
	got := a.ok(a.alice, "GET", "/api/v1/renewals/"+id(r), nil)
	if got["next_expires"] != store.AddInterval(end, 1, "year") {
		t.Fatalf("suggested next date %v", got["next_expires"])
	}
	out := a.ok(a.alice, "POST", "/api/v1/renewals/"+id(r)+"/renew", map[string]any{"money": false})
	rn := out["renewal"].(map[string]any)
	if rn["expires_on"] != store.AddInterval(end, 1, "year") || rn["status"] != "ok" || num(rn, "renewals") != 1 {
		t.Fatalf("after renewing: %v", rn)
	}
	h := out["history"].(map[string]any)
	if h["old_expires"] != end || num(h, "cost") != 41200 {
		t.Fatalf("history: %v", h)
	}
}

func TestStageAndReminders(t *testing.T) {
	today := store.Today()
	mk := func(kind string, days int, remind string, lead int) store.Renewal {
		r := store.Renewal{Kind: kind, ExpiresOn: store.AddDays(today, days), RemindDays: remind, LeadDays: lead}
		r.DaysLeft = days
		return r
	}
	for _, c := range []struct {
		r    store.Renewal
		want int // -2 = none yet
	}{
		{mk("insurance", 100, "", 0), -2}, {mk("insurance", 40, "", 0), 45}, {mk("insurance", 14, "", 0), 14}, {mk("insurance", 2, "", 0), 3},
		{mk("insurance", 0, "", 0), 0}, {mk("insurance", -3, "", 0), -1}, {mk("passport", 200, "", 0), 270}, {mk("other", 50, "60, 10", 0), 60},
		{mk("contract", 40, "", 30), 44}, // lead 30 adds a reminder at 44 days
	} {
		got := c.r.Stage()
		g := -2
		if got != nil {
			g = *got
		}
		if g != c.want {
			t.Errorf("%s %d days (remind %q lead %d): stage %d, want %d", c.r.Kind, c.r.DaysLeft, c.r.RemindDays, c.r.LeadDays, g, c.want)
		}
	}
	if got := store.AddInterval("2027-03-31", -1, "month"); got != "2027-02-28" {
		t.Errorf("a month back from 31 March: %s", got)
	}
	if got := store.ParseRemind("7, 90,30, x, 90"); store.JoinRemind(got) != "90,30,7" {
		t.Errorf("ParseRemind: %v", got)
	}
}

func TestDailyCheckRemindsOncePerStage(t *testing.T) {
	a := newApp(t, nil)
	ctx := context.Background()
	uid := a.ids["Alice"]
	r := a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Domain example.org", "kind": "domain", "expires_on": store.AddDays(store.Today(), 100)})
	// move it into the 30-day reminder as if time had passed
	_, _ = a.st.DB.Exec(`UPDATE renewals SET expires_on = ? WHERE id = ?`, store.AddDays(store.Today(), 25), num(r, "id"))
	st, _ := a.st.GetSettings(ctx, uid)
	body, _ := a.s.dailyCheck(ctx, uid, st, nowAt(8))
	if !strings.Contains(body, "Domain example.org — runs out in 25 days") {
		t.Fatalf("first message: %q", body)
	}
	if body, _ := a.s.dailyCheck(ctx, uid, st, nowAt(8)); body != "" {
		t.Fatalf("the same stage again: %q", body)
	}
	// something renewing by itself rolls on instead of running out
	auto := a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Music", "kind": "subscription", "auto_renews": true, "renew_n": 1, "renew_unit": "month", "expires_on": store.AddDays(store.Today(), 5)})
	_, _ = a.st.DB.Exec(`UPDATE renewals SET expires_on = ? WHERE id = ?`, store.AddDays(store.Today(), -2), num(auto, "id"))
	a.s.dailyCheck(ctx, uid, st, nowAt(8))
	got := a.ok(a.alice, "GET", "/api/v1/renewals/"+id(auto), nil)["renewal"].(map[string]any)
	if got["expires_on"].(string) < store.Today() || num(got, "renewals") != 1 {
		t.Fatalf("auto renewal did not roll on: %v", got)
	}
}

func TestLinkScopes(t *testing.T) {
	req := func(method, path string) *http.Request { return httptest.NewRequest(method, path, nil) }
	read := []string{"renewals:read"}
	write := []string{"renewals:write"}
	for _, c := range []struct {
		scopes []string
		r      *http.Request
		want   bool
	}{
		{nil, req("GET", "/api/v1/auth/me"), true},
		{read, req("GET", "/api/v1/overview"), true},
		{read, req("GET", "/api/v1/due"), true},
		{read, req("GET", "/api/v1/renewals/4"), true},
		{read, req("GET", "/api/v1/day/2026-11-04"), true},
		{read, req("POST", "/api/v1/renewals"), false},
		{write, req("POST", "/api/v1/renewals"), true},
		{write, req("POST", "/api/v1/renewals/3/renew"), true},
		{write, req("PUT", "/api/v1/renewals/3"), true},
		{write, req("GET", "/api/v1/overview"), false},
		{append(read, write...), req("POST", "/api/v1/scan"), false},
		{append(read, write...), req("PUT", "/api/v1/settings"), false},
		{append(read, write...), req("GET", "/api/v1/export.json"), false},
		{append(read, write...), req("GET", "/api/v1/backup"), false},
		{append(read, write...), req("POST", "/api/v1/tokens"), false},
		{append(read, write...), req("GET", "/api/v1/links/money/accounts"), false},
	} {
		if got := linkAllows(c.scopes, c.r); got != c.want {
			t.Errorf("%v %s %s = %v, want %v", c.scopes, c.r.Method, c.r.URL.Path, got, c.want)
		}
	}
}

func TestExportImportIdempotent(t *testing.T) {
	a := newApp(t, nil)
	r := a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Passport", "kind": "passport", "expires_on": store.AddDays(store.Today(), 30)})
	a.ok(a.alice, "POST", "/api/v1/renewals/"+id(r)+"/renew", map[string]any{"money": false})
	a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Gym", "kind": "membership", "expires_on": store.AddDays(store.Today(), 60)})
	code, doc, _ := a.call(a.alice, "GET", "/api/v1/export.json", nil)
	if code != 200 || doc["app"] != "pocket-renewals" {
		t.Fatalf("export: %d %v", code, doc["app"])
	}
	first := a.ok(a.bob, "POST", "/api/v1/import", doc)
	if num(first, "renewals") != 2 || num(first, "history") != 1 {
		t.Fatalf("first import: %v", first)
	}
	second := a.ok(a.bob, "POST", "/api/v1/import", doc)
	if num(second, "renewals") != 0 || num(second, "history") != 0 {
		t.Fatalf("second import added: %v", second)
	}
	if n := len(a.list(a.bob, "/api/v1/renewals")); n != 2 {
		t.Fatalf("bob has %d renewals", n)
	}
	a.expect(400, a.bob, "POST", "/api/v1/import", map[string]any{"app": "pocket-home"})
}

func TestSettingsPerPerson(t *testing.T) {
	a := newApp(t, nil)
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"currency": "BAM", "notify_url": "http://apprise/notify/alice", "money_mode": "direct", "money_token": "secret-token-1234", "title": "Our papers", "scan_sources": map[string]bool{"email": false}})
	b := a.ok(a.bob, "GET", "/api/v1/settings", nil)
	if b["currency"] != "EUR" || b["notify_url"] != "" || b["money_token"] != "" || b["money_mode"] != "direct" && b["money_mode"] != "core" {
		t.Fatalf("bob got alice's settings: %v", b)
	}
	if b["title"] != "Our papers" {
		t.Fatalf("the admin's title is box-wide, bob sees %v", b["title"])
	}
	a.ok(a.bob, "PUT", "/api/v1/settings", map[string]any{"title": "Bob's"})
	al := a.ok(a.alice, "GET", "/api/v1/settings", nil)
	if al["title"] != "Our papers" || al["money_token"] != "secr••••1234" || al["currency"] != "BAM" || al["scan_sources"].(map[string]any)["email"] != false {
		t.Fatalf("alice after bob's change: %v", al)
	}
	a.expect(400, a.alice, "PUT", "/api/v1/settings", map[string]any{"currency": "euro"})
}

func TestDemoSeeds(t *testing.T) {
	a := newApp(t, nil)
	if err := a.s.seedDemo(context.Background(), a.ids["Alice"]); err != nil {
		t.Fatal(err)
	}
	o := a.ok(a.alice, "GET", "/api/v1/overview", nil)
	c := o["counts"].(map[string]any)
	if num(c, "expired") < 2 || num(c, "soon") < 5 || num(c, "check") < 3 || len(o["recent"].([]any)) == 0 {
		t.Fatalf("demo overview: %v", c)
	}
	if err := a.s.seedDemo(context.Background(), a.ids["Alice"]); err != nil {
		t.Fatal(err)
	}
	if n := num(a.ok(a.alice, "GET", "/api/v1/overview", nil)["counts"].(map[string]any), "total"); n != num(c, "total") {
		t.Fatalf("demo seeded twice: %v", n)
	}
}

func TestGuessing(t *testing.T) {
	for in, want := range map[string]string{"Putovnica Ana": "passport", "Polica osiguranja kuće": "insurance", "Garancija za frižider": "warranty",
		"Lična karta": "id_card", "Ugovor o najmu stana": "contract", "Vozačka dozvola": "driving_licence", "Invoice 2026-11": ""} {
		if got := guessKind(in); got != want {
			t.Errorf("guessKind(%q) = %q, want %q", in, got, want)
		}
	}
	for name, want := range map[string]bool{"Valid until": true, "Expiry date": true, "Važi do": true, "Due date": false, "Issued on": false, "Payment due": false} {
		if got := isExpiryField(name); got != want {
			t.Errorf("isExpiryField(%q) = %v", name, got)
		}
	}
}

// fakeSiblings is one server answering as Pocket Documents and Pocket Home.
func fakeSiblings(t *testing.T, until string) *httptest.Server {
	mux := http.NewServeMux()
	j := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { j(w, map[string]any{"status": "ok", "version": "1"}) })
	mux.HandleFunc("/api/v1/auth/me", func(w http.ResponseWriter, r *http.Request) { j(w, map[string]any{"id": 1}) })
	mux.HandleFunc("/api/v1/custom_fields", func(w http.ResponseWriter, r *http.Request) {
		j(w, []map[string]any{{"id": 1, "name": "Valid until", "data_type": "date"}, {"id": 2, "name": "Due date", "data_type": "date"}})
	})
	mux.HandleFunc("/api/v1/documents", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") != "0" {
			j(w, map[string]any{"results": []any{}})
			return
		}
		j(w, map[string]any{"results": []map[string]any{
			{"id": 10, "title": "Putovnica", "document_type_name": "Passport", "status": "done", "custom_fields": []map[string]any{{"field": 1, "value": until}}},
			{"id": 11, "title": "Electricity bill", "document_type_name": "Invoice", "status": "done", "custom_fields": []map[string]any{{"field": 2, "value": until}}},
			{"id": 12, "title": "Holiday photo", "status": "done"},
		}})
	})
	mux.HandleFunc("/api/v1/things", func(w http.ResponseWriter, r *http.Request) {
		j(w, []map[string]any{{"id": 4, "name": "Dishwasher", "home_name": "Flat", "warranty_until": until, "vendor": "Bosch"}, {"id": 5, "name": "Roof"}})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestScansAddOnceAndRememberDeletes(t *testing.T) {
	a := newApp(t, nil)
	ctx := context.Background()
	uid := a.ids["Alice"]
	until := store.AddDays(store.Today(), 400)
	ts := fakeSiblings(t, until)
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"documents_mode": "direct", "documents_url": ts.URL, "documents_token": "tok-docs-123456",
		"home_mode": "direct", "home_url": ts.URL, "home_token": "tok-home-123456"})
	docs, _ := sourceByName("documents")
	sc, err := a.s.runScan(ctx, uid, docs, "manual", 0)
	if err != nil || sc.Looked != 3 || sc.Added != 1 {
		t.Fatalf("documents scan: %+v %v", sc, err)
	}
	home, _ := sourceByName("home")
	if sc, err := a.s.runScan(ctx, uid, home, "manual", 0); err != nil || sc.Added != 1 {
		t.Fatalf("home scan: %+v %v", sc, err)
	}
	list := a.list(a.alice, "/api/v1/renewals?status=check")
	if len(list) != 2 {
		t.Fatalf("found: %v", list)
	}
	var passport map[string]any
	for _, x := range list {
		if m := x.(map[string]any); m["kind"] == "passport" {
			passport = m
		}
	}
	if passport == nil || passport["expires_on"] != until || passport["source"] != "documents" || num(passport, "document_id") != 10 || !strings.HasPrefix(passport["source_url"].(string), ts.URL+"/documents/10") {
		t.Fatalf("the passport: %v", passport)
	}
	// again: nothing new
	if sc, _ := a.s.runScan(ctx, uid, docs, "manual", 0); sc.Added != 0 || sc.Updated != 0 {
		t.Fatalf("second scan: %+v", sc)
	}
	// deleted stays deleted
	a.expect(204, a.alice, "DELETE", "/api/v1/renewals/"+id(passport), nil)
	if sc, _ := a.s.runScan(ctx, uid, docs, "manual", 0); sc.Added != 0 {
		t.Fatalf("a deleted renewal came back: %+v", sc)
	}
	if n := len(a.list(a.alice, "/api/v1/dismissed")); n != 1 {
		t.Fatalf("dismissed: %d", n)
	}
	// Bob's scans see nothing of Alice's connections
	if _, err := a.s.runScan(ctx, a.ids["Bob"], docs, "manual", 0); err == nil {
		t.Fatalf("bob scanned with alice's connection")
	}
	// email needs AI
	email, _ := sourceByName("email")
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"email_mode": "direct", "email_url": ts.URL, "email_token": "tok-mail-123456"})
	if _, err := a.s.runScan(ctx, uid, email, "manual", 0); err == nil || !strings.Contains(err.Error(), "AI") {
		t.Fatalf("email without AI: %v", err)
	}
}

func TestUpsertFoundMovesToALaterDate(t *testing.T) {
	a := newApp(t, nil)
	ctx := context.Background()
	uid := a.ids["Alice"]
	f := store.Found{Ref: "home:thing:1", Source: "home", Title: "Warranty: Fridge", Kind: "warranty", ExpiresOn: store.AddDays(store.Today(), 20)}
	if res, _, err := a.st.UpsertFound(ctx, uid, f); err != nil || res != store.UpsertAdded {
		t.Fatalf("add: %s %v", res, err)
	}
	f.ExpiresOn = store.AddDays(store.Today(), 10)
	if res, _, _ := a.st.UpsertFound(ctx, uid, f); res != store.UpsertSame {
		t.Fatalf("an earlier date must not move it back: %s", res)
	}
	f.ExpiresOn = store.AddDays(store.Today(), 750)
	res, r, _ := a.st.UpsertFound(ctx, uid, f)
	if res != store.UpsertUpdated || r.ExpiresOn != f.ExpiresOn || r.Renewals != 1 {
		t.Fatalf("a later date: %s %+v", res, r)
	}
}

func nowAt(hour int) time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), hour, 0, 0, 0, n.Location())
}

func TestLiveEventsRescanTheApp(t *testing.T) {
	liveDelay = 10 * time.Millisecond
	a := newApp(t, nil)
	until := store.AddDays(store.Today(), 300)
	ts := fakeSiblings(t, until)
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"home_mode": "direct", "home_url": ts.URL, "home_token": "tok-home-123456"})
	ev := func(topic, email string) core.Event {
		p, _ := json.Marshal(map[string]any{"user_email": email, "thing_id": 4, "name": "Dishwasher", "warranty_until": until})
		return core.Event{ID: 1, Topic: topic, Payload: p, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	}
	ctx := context.Background()
	a.s.handleEvent(ctx, ev("home.something_else", "alice@example.com")) // not followed
	a.s.handleEvent(ctx, ev("home.thing_saved", "nobody@example.com"))   // not a person here
	a.s.handleEvent(ctx, ev("home.thing_saved", "alice@example.com"))
	a.s.handleEvent(ctx, ev("home.thing_saved", "alice@example.com")) // joins the same scan
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if l := a.list(a.alice, "/api/v1/renewals?source=home"); len(l) == 1 {
			if n := len(a.list(a.bob, "/api/v1/renewals")); n != 0 {
				t.Fatalf("bob got alice's event: %d", n)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the warranty did not arrive: %v", a.list(a.alice, "/api/v1/renewals"))
}
