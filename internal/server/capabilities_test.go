package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/config"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/store"
)

// fakeCapCore is a Pocket Core that vouches for link tokens
// pcx_<alice|bob>_<search|day|widget|capture|read|write> from Pocket Hub
// and remembers the manifest Renewals registers with.
type fakeCapCore struct {
	*httptest.Server
	mu       sync.Mutex
	manifest map[string]any
}

func newCapApp(t *testing.T) (*app, *fakeCapCore) {
	t.Helper()
	fc := &fakeCapCore{}
	scopes := map[string]string{"search": core.ScopeSearch, "day": core.ScopeDay, "widget": core.ScopeWidget, "capture": core.ScopeCapture,
		"read": "renewals:read", "write": "renewals:write"}
	fc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/register":
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			fc.mu.Lock()
			fc.manifest = m
			fc.mu.Unlock()
			io.WriteString(w, `{}`)
		case "/api/v1/auth/introspect":
			var in struct{ Token string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			parts := strings.Split(in.Token, "_")
			if len(parts) != 3 || parts[0] != "pcx" || scopes[parts[2]] == "" {
				io.WriteString(w, `{"active":false}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "kind": "delegated", "audience": "renewals", "scope": scopes[parts[2]],
				"user": map[string]any{"id": 1, "sub": parts[1] + "@example.com", "email": parts[1] + "@example.com", "name": parts[1], "role": "member"},
				"app":  map[string]any{"id": 9, "slug": "hub", "name": "Pocket Hub"}})
		default:
			io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(fc.Close)
	return newApp(t, func(c *config.Config) { c.CoreURL, c.CoreRegistrationKey = fc.URL, "key" }), fc
}

// items are the rows of a capability answer.
func items(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	l, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("no items list in %v", m)
	}
	out := []map[string]any{}
	for _, v := range l {
		out = append(out, v.(map[string]any))
	}
	return out
}

// item finds the row with that title.
func item(t *testing.T, m map[string]any, title string) map[string]any {
	t.Helper()
	for _, it := range items(t, m) {
		if it["title"] == title {
			return it
		}
	}
	t.Fatalf("no %q in %v", title, m["items"])
	return nil
}

// seedCap gives alice a few renewals: car insurance in 30 days, a domain
// that renews by itself in 200 days, an old passport that is archived.
func (a *app) seedCap() map[string]string {
	a.t.Helper()
	today := store.Today()
	car := a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Car insurance", "kind": "insurance", "provider": "Generali", "holder": "the Golf",
		"expires_on": store.AddDays(today, 30), "cost": 32000, "currency": "EUR", "notes": "Green card in the glovebox"})
	dom := a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "example.org", "kind": "domain", "provider": "Namecheap",
		"expires_on": store.AddDays(today, 200), "auto_renews": true, "cost": 1398, "currency": "USD"})
	old := a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Old passport", "kind": "passport", "expires_on": store.AddDays(today, -400), "archived": true})
	return map[string]string{"car": id(car), "domain": id(dom), "old": id(old)}
}

func TestPocketSearch(t *testing.T) {
	a := newApp(t, nil)
	ids := a.seedCap()
	today := store.Today()
	res := a.ok(a.alice, "GET", "/api/v1/pocket/search?q=insurance", nil)
	car := item(t, res, "Car insurance (the Golf)")
	if car["id"] != ids["car"] || car["kind"] != "insurance" || car["url"] != "/r/"+ids["car"] || car["date"] != store.AddDays(today, 30) ||
		car["snippet"] != "Expires on "+store.AddDays(today, 30)+" · 320 EUR a year" {
		t.Fatalf("car insurance: %v", car)
	}
	// by kind: the domain's name does not say "domain"
	dom := item(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q=domain", nil), "example.org")
	if dom["kind"] != "domain" || dom["snippet"] != "Renews on "+store.AddDays(today, 200)+" · 13.98 USD a year" || dom["url"] != "/r/"+ids["domain"] {
		t.Fatalf("domain: %v", dom)
	}
	// by provider and by notes
	if got := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q=generali", nil)); len(got) != 1 || got[0]["id"] != ids["car"] {
		t.Fatalf("by provider: %v", got)
	}
	if got := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q=glovebox", nil)); len(got) != 1 || got[0]["id"] != ids["car"] {
		t.Fatalf("by notes: %v", got)
	}
	// archived ones too, last
	a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Passport", "kind": "passport", "expires_on": store.AddDays(today, 900)})
	got := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q=passport", nil))
	if len(got) != 2 || got[0]["title"] != "Passport" || got[0]["kind"] != "passport" || got[1]["id"] != ids["old"] ||
		got[1]["snippet"] != "Expired on "+store.AddDays(today, -400) {
		t.Fatalf("passports: %v", got)
	}
	if got := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q=passport&limit=1", nil)); len(got) != 1 {
		t.Fatalf("limit 1: %v", got)
	}
	// no words, or no letter or digit: nothing
	for _, q := range []string{"", "%3F%3F", "*", "%25"} {
		if got := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q="+q, nil)); len(got) != 0 {
			t.Fatalf("q=%q: %v", q, got)
		}
	}
	// another person's renewals never appear
	for _, q := range []string{"insurance", "domain", "generali", "passport", "example"} {
		if got := items(t, a.ok(a.bob, "GET", "/api/v1/pocket/search?q="+q, nil)); len(got) != 0 {
			t.Fatalf("bob finds alice's %q: %v", q, got)
		}
	}
	a.expect(401, "", "GET", "/api/v1/pocket/search?q=insurance", nil)
}

func TestPocketDay(t *testing.T) {
	a := newApp(t, nil)
	today := store.Today()
	// nothing yet
	if d := a.ok(a.alice, "GET", "/api/v1/pocket/day/"+today, nil); d["summary"] != nil || len(items(t, d)) != 0 {
		t.Fatalf("an empty day: %v", d)
	}
	home := a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Home insurance", "kind": "insurance", "expires_on": today, "auto_renews": true, "cost": 18900})
	day := a.ok(a.alice, "GET", "/api/v1/pocket/day/"+today, nil)
	if day["date"] != today || day["summary"] != "Home insurance renews today, 189 EUR" || len(items(t, day)) != 1 {
		t.Fatalf("one renewal: %v", day)
	}
	if it := item(t, day, "Home insurance"); it["id"] != "renewal:"+id(home) || it["kind"] != "renews" || it["sub"] != "Renews today" || num(it, "amount") != 18900 ||
		it["currency"] != "EUR" || it["url"] != "/r/"+id(home) || it["done"] != nil {
		t.Fatalf("home insurance: %v", it)
	}

	// a passport that ran out ten days ago, renewed today
	past := store.AddDays(today, -10)
	pass := a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Passport", "kind": "passport", "holder": "Ana", "expires_on": past, "cost": 5500})
	if d := a.ok(a.alice, "GET", "/api/v1/pocket/day/"+past, nil); d["summary"] != "Passport (Ana) expired, 55 EUR" || item(t, d, "Passport (Ana)")["sub"] != "Expired" {
		t.Fatalf("before renewing: %v", d)
	}
	a.ok(a.alice, "POST", "/api/v1/renewals/"+id(pass)+"/renew", map[string]any{"expires_on": store.AddInterval(today, 10, "year"), "cost": 6000, "money": false})
	day = a.ok(a.alice, "GET", "/api/v1/pocket/day/"+today, nil)
	if day["summary"] != "1 renewal due today, 1 done" || len(items(t, day)) != 2 {
		t.Fatalf("today: %v", day)
	}
	if it := item(t, day, "Passport (Ana)"); it["kind"] != "renewed" || it["done"] != true || it["sub"] != "Renewed until "+store.AddInterval(today, 10, "year") ||
		num(it, "amount") != 6000 || it["url"] != "/r/"+id(pass) || !strings.HasPrefix(it["id"].(string), "history:") {
		t.Fatalf("renewed: %v", it)
	}
	// the day it ran out still says so
	d := a.ok(a.alice, "GET", "/api/v1/pocket/day/"+past, nil)
	if it := item(t, d, "Passport (Ana)"); d["summary"] != "Passport (Ana) expired" || it["kind"] != "expired" || it["sub"] != "Expired, renewed on "+today || it["amount"] != nil {
		t.Fatalf("the day it ran out: %v", d)
	}
	// renewed ahead of its day: nothing ran out that day
	soon := store.AddDays(today, 5)
	id2 := a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "ID card", "kind": "id_card", "expires_on": soon})
	a.ok(a.alice, "POST", "/api/v1/renewals/"+id(id2)+"/renew", map[string]any{"expires_on": store.AddInterval(soon, 10, "year"), "money": false})
	if d := a.ok(a.alice, "GET", "/api/v1/pocket/day/"+soon, nil); len(items(t, d)) != 0 {
		t.Fatalf("renewed ahead: %v", d)
	}
	// a day still to come, two things due
	a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Gym", "kind": "membership", "expires_on": store.AddDays(today, 7)})
	a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Netflix", "kind": "subscription", "expires_on": store.AddDays(today, 7), "auto_renews": true})
	if d := a.ok(a.alice, "GET", "/api/v1/pocket/day/"+store.AddDays(today, 7), nil); d["summary"] != "2 renewals due" || item(t, d, "Gym")["sub"] != "Expires" ||
		item(t, d, "Netflix")["sub"] != "Renews" {
		t.Fatalf("a day to come: %v", d)
	}
	// somebody else's day, and a bad date
	if d := a.ok(a.bob, "GET", "/api/v1/pocket/day/"+today+"?tz=Europe/Sarajevo", nil); d["summary"] != nil || len(items(t, d)) != 0 {
		t.Fatalf("bob sees alice's day: %v", d)
	}
	a.expect(400, a.alice, "GET", "/api/v1/pocket/day/yesterday", nil)
	// the old route answers as before
	if old := a.ok(a.alice, "GET", "/api/v1/day/"+today, nil); len(old["expires"].([]any)) != 1 || len(old["renewed"].([]any)) != 2 {
		t.Fatalf("/day changed: %v", old)
	}
}

func TestPocketWidget(t *testing.T) {
	a := newApp(t, nil)
	if w := a.ok(a.bob, "GET", "/api/v1/pocket/widget", nil); w["line"] != nil || w["metrics"] != nil || len(items(t, w)) != 0 {
		t.Fatalf("without renewals: %v", w)
	}
	today := store.Today()
	// far away: nothing close, the line stays empty
	a.ok(a.bob, "POST", "/api/v1/renewals", map[string]any{"title": "Passport", "kind": "passport", "expires_on": store.AddDays(today, 900), "renew_n": 10, "renew_unit": "year", "cost": 5500})
	w := a.ok(a.bob, "GET", "/api/v1/pocket/widget", nil)
	if w["line"] != nil || len(items(t, w)) != 0 || w["url"] != "/" {
		t.Fatalf("nothing close: %v", w)
	}
	if ms := w["metrics"].([]any); len(ms) != 3 || ms[2].(map[string]any)["label"] != "A year" || ms[2].(map[string]any)["value"] != "6 EUR" {
		t.Fatalf("bob's metrics: %v", ms)
	}

	ids := a.seedCap()
	pass := a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Passport", "kind": "passport", "expires_on": store.AddDays(today, 40)})
	w = a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil)
	if w["line"] != "Car insurance (the Golf) expires in 30 days, 1 more coming up" {
		t.Fatalf("line: %v", w)
	}
	rows := items(t, w)
	if len(rows) != 2 || rows[0]["id"] != ids["car"] || rows[0]["detail"] != "in 30 days · 320 EUR" || rows[0]["url"] != "/r/"+ids["car"] ||
		rows[1]["id"] != id(pass) || rows[1]["detail"] != "in 40 days" {
		t.Fatalf("rows: %v", rows)
	}
	// what has run out comes first
	late := a.ok(a.alice, "POST", "/api/v1/renewals", map[string]any{"title": "Vignette", "kind": "vehicle", "expires_on": store.AddDays(today, -3), "cost": 10340})
	w = a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil)
	rows = items(t, w)
	if w["line"] != "Vignette expired 3 days ago, 2 more coming up" || len(rows) != 3 || rows[0]["id"] != id(late) || rows[0]["detail"] != "expired 3 days ago · 103.40 EUR" {
		t.Fatalf("expired first: %v", w)
	}
	ms := w["metrics"].([]any)
	for i, want := range [][2]string{{"Expired", "1"}, {"Next 60 days", "2"}, {"A year", "423 EUR + 14 USD"}} {
		if m := ms[i].(map[string]any); len(ms) != 3 || m["label"] != want[0] || m["value"] != want[1] {
			t.Fatalf("metrics: %v", ms)
		}
	}
	// bob still sees only his own
	if w := a.ok(a.bob, "GET", "/api/v1/pocket/widget", nil); len(items(t, w)) != 0 || w["line"] != nil {
		t.Fatalf("bob's widget: %v", w)
	}
}

// capture posts a quick line and returns the answer.
func capture(a *app, tok, requestID, text string, more map[string]any) (int, map[string]any) {
	body := map[string]any{"request_id": requestID, "kind": "renewal", "text": text}
	for k, v := range more {
		body[k] = v
	}
	code, m, _ := a.call(tok, "POST", "/api/v1/pocket/capture", body)
	return code, m
}

func TestPocketCapture(t *testing.T) {
	a := newApp(t, nil)
	keep := func(tok, requestID, text string, more map[string]any) map[string]any {
		t.Helper()
		code, m := capture(a, tok, requestID, text, more)
		if code != 201 {
			t.Fatalf("capture %q → %d %v", text, code, m)
		}
		return a.ok(tok, "GET", "/api/v1/renewals/"+m["id"].(string), nil)
	}
	code, first := capture(a, a.alice, "req-0000000000000001", "Domain bjelke.org 2027-03-01", nil)
	if code != 201 || first["title"] != "Domain bjelke.org" || first["url"] != "/r/"+first["id"].(string) {
		t.Fatalf("capture: %d %v", code, first)
	}
	got := a.ok(a.alice, "GET", "/api/v1/renewals/"+first["id"].(string), nil)
	rn, _ := got["renewal"].(map[string]any)
	if rn == nil {
		rn = got
	}
	if rn["kind"] != "domain" || rn["expires_on"] != "2027-03-01" || num(rn, "renew_n") != 1 || rn["renew_unit"] != "year" || rn["source"] != "web" || rn["currency"] != "EUR" {
		t.Fatalf("the renewal: %v", rn)
	}
	// the same request again: the same answer, nothing added
	if code, again := capture(a, a.alice, "req-0000000000000001", "Domain bjelke.org 2027-03-01", nil); code != 201 || again["id"] != first["id"] ||
		again["url"] != first["url"] || len(a.list(a.alice, "/api/v1/renewals")) != 1 {
		t.Fatalf("a retry: %d %v", code, again)
	}
	// the same id with other words
	if code, m := capture(a, a.alice, "req-0000000000000001", "Something else", nil); code != 400 || m["error"] != "this was already kept with other words" {
		t.Fatalf("other words: %d %v", code, m)
	}
	// the date field, a title and notes, no date at all
	r := keep(a.alice, "req-0000000000000002", "Car insurance\nGenerali, policy AO-778812", map[string]any{"date": "2027-05-01"})
	if r = unwrap(r); r["title"] != "Car insurance" || r["kind"] != "insurance" || r["expires_on"] != "2027-05-01" || r["notes"] != "Generali, policy AO-778812" {
		t.Fatalf("with the date field: %v", r)
	}
	if r := unwrap(keep(a.alice, "req-0000000000000003", "Netflix", nil)); r["kind"] != "other" || r["expires_on"] != "" || r["status"] != "undated" {
		t.Fatalf("no date: %v", r)
	}
	if r := unwrap(keep(a.alice, "req-0000000000000004", "", map[string]any{"title": "Gym 2026-12-31", "date": "2027-01-31"})); r["title"] != "Gym" || r["expires_on"] != "2027-01-31" {
		t.Fatalf("the date field wins: %v", r)
	}
	// a line that is only a date: no name to give it, nothing kept
	before := len(a.list(a.alice, "/api/v1/renewals"))
	if code, m := capture(a, a.alice, "req-0000000000000005", "2027-03-01", nil); code != 400 || m["error"] != `give it a name, e.g. "Domain bjelke.org 2027-03-01"` {
		t.Fatalf("only a date: %d %v", code, m)
	}
	if code, m := capture(a, a.alice, "req-0000000000000006", "   ", map[string]any{"title": " "}); code != 400 {
		t.Fatalf("nothing: %d %v", code, m)
	}
	if n := len(a.list(a.alice, "/api/v1/renewals")); n != before {
		t.Fatalf("a refused line was kept: %d, was %d", n, before)
	}
	// what the route checks itself
	a.expect(400, a.alice, "POST", "/api/v1/pocket/capture", map[string]any{"request_id": "short", "text": "x"})
	a.expect(400, a.alice, "POST", "/api/v1/pocket/capture", map[string]any{"request_id": "req-0000000000000009", "kind": "task", "text": "x"})
	a.expect(400, a.alice, "POST", "/api/v1/pocket/capture", map[string]any{"request_id": "req-0000000000000009", "text": "x", "date": "tomorrow"})
	// per person: a request id is each person's own
	mine := len(a.list(a.alice, "/api/v1/renewals"))
	if code, m := capture(a, a.bob, "req-0000000000000001", "Domain bjelke.org 2027-03-01", nil); code != 201 || m["id"] == first["id"] {
		t.Fatalf("bob's line: %d %v", code, m)
	}
	if len(a.list(a.bob, "/api/v1/renewals")) != 1 || len(a.list(a.alice, "/api/v1/renewals")) != mine {
		t.Fatal("bob's capture reached alice")
	}
}

// unwrap takes a renewal out of GET /renewals/{id}'s answer.
func unwrap(m map[string]any) map[string]any {
	if r, ok := m["renewal"].(map[string]any); ok {
		return r
	}
	return m
}

// A kept line tells the Core bus once, a retry not again.
func TestPocketCaptureEvent(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.CoreURL = "http://core.invalid"
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	ctx := context.Background()
	if err := s.core.Init(ctx); err != nil {
		t.Fatal(err)
	}
	u := &store.User{Email: "ana@example.com", Name: "Ana", Role: "admin"}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	tok, _, _ := st.CreateAPIToken(ctx, u.ID, "t")
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)
	a := &app{t: t, url: ts.URL, s: s, st: st}
	for range 2 {
		if code, m := capture(a, tok, "req-0000000000000001", "Passport 2030-01-01", nil); code != 201 {
			t.Fatalf("capture → %d %v", code, m)
		}
	}
	rows, err := st.DB.QueryContext(ctx, `SELECT body FROM pocket_outbox ORDER BY created_at, rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	added := 0
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		var ev struct {
			Topic   string
			Payload map[string]any
		}
		if err := json.Unmarshal(b, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Topic != "renewals.added" {
			continue
		}
		added++
		if ev.Payload["title"] != "Passport" || ev.Payload["kind"] != "passport" || ev.Payload["expires_on"] != "2030-01-01" || ev.Payload["user_email"] != "ana@example.com" {
			t.Errorf("payload = %v", ev.Payload)
		}
	}
	if added != 1 {
		t.Fatalf("renewals.added events = %d, want 1", added)
	}
}

// A link token with one pocket:* scope reaches its one route and nothing else.
func TestPocketLinkTokens(t *testing.T) {
	a, _ := newCapApp(t)
	ids := a.seedCap()
	today := store.Today()
	line := func(requestID string) map[string]any {
		return map[string]any{"request_id": requestID, "kind": "renewal", "text": "Domain bjelke.org 2027-03-01"}
	}
	for _, c := range []struct {
		tok, method, path string
		body              any
		want              int
	}{
		{"pcx_alice_search", "GET", "/api/v1/pocket/search?q=insurance", nil, 200},
		{"pcx_alice_day", "GET", "/api/v1/pocket/day/" + today, nil, 200},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/widget", nil, 200},
		{"pcx_alice_capture", "POST", "/api/v1/pocket/capture", line("req-0000000000000001"), 201},
		{"pcx_alice_widget", "GET", "/api/v1/auth/me", nil, 200},
		// another capability's route
		{"pcx_alice_search", "GET", "/api/v1/pocket/day/" + today, nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_search", "POST", "/api/v1/pocket/capture", line("req-0000000000000002"), 403},
		{"pcx_alice_day", "GET", "/api/v1/pocket/search?q=insurance", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/search?q=insurance", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/day/" + today, nil, 403},
		{"pcx_alice_widget", "POST", "/api/v1/pocket/capture", line("req-0000000000000003"), 403},
		{"pcx_alice_capture", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_capture", "GET", "/api/v1/pocket/search?q=insurance", nil, 403},
		// the rest of the app
		{"pcx_alice_widget", "GET", "/api/v1/overview", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/due", nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/search?q=insurance", nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/renewals", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/day/" + today, nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/history", nil, 403},
		{"pcx_alice_capture", "POST", "/api/v1/renewals", map[string]any{"title": "x"}, 403},
		{"pcx_alice_capture", "PUT", "/api/v1/renewals/" + ids["car"], map[string]any{"title": "x"}, 403},
		{"pcx_alice_capture", "DELETE", "/api/v1/renewals/" + ids["car"], nil, 403},
		{"pcx_alice_capture", "POST", "/api/v1/renewals/" + ids["car"] + "/renew", map[string]any{}, 403},
		{"pcx_alice_capture", "GET", "/api/v1/settings", nil, 403},
		// the app's own scopes do not open the new routes
		{"pcx_alice_read", "GET", "/api/v1/pocket/search?q=insurance", nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/pocket/day/" + today, nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_write", "POST", "/api/v1/pocket/capture", line("req-0000000000000004"), 403},
		{"pcx_alice_read", "GET", "/api/v1/search?q=insurance", nil, 200},
		{"pcx_nobody", "GET", "/api/v1/pocket/widget", nil, 401},
	} {
		if code, m, _ := a.call(c.tok, c.method, c.path, c.body); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%v)", c.tok, c.method, c.path, code, c.want, m)
		}
	}
	// the token acts for its person only
	if got := items(t, a.ok("pcx_alice_search", "GET", "/api/v1/pocket/search?q=insurance", nil)); len(got) != 1 || got[0]["id"] != ids["car"] {
		t.Fatalf("alice through the link: %v", got)
	}
	if got := items(t, a.ok("pcx_bob_search", "GET", "/api/v1/pocket/search?q=insurance", nil)); len(got) != 0 {
		t.Fatalf("bob through the link finds alice's: %v", got)
	}
	if w := a.ok("pcx_bob_widget", "GET", "/api/v1/pocket/widget", nil); len(items(t, w)) != 0 {
		t.Fatalf("bob's widget through the link: %v", w)
	}
	if d := a.ok("pcx_bob_day", "GET", "/api/v1/pocket/day/"+store.AddDays(today, 30), nil); len(items(t, d)) != 0 {
		t.Fatalf("bob's day through the link: %v", d)
	}
	if d := a.ok("pcx_alice_day", "GET", "/api/v1/pocket/day/"+store.AddDays(today, 30), nil); len(items(t, d)) != 1 {
		t.Fatalf("alice's day through the link: %v", d)
	}
	// only the one allowed capture landed, as the person's own (source web); a retry adds nothing
	if code, m := capture(a, "pcx_alice_capture", "req-0000000000000001", "Domain bjelke.org 2027-03-01", nil); code != 201 {
		t.Fatalf("retry through the link: %d %v", code, m)
	}
	var kept []map[string]any
	for _, v := range a.list(a.alice, "/api/v1/renewals?q=bjelke") {
		kept = append(kept, v.(map[string]any))
	}
	if len(kept) != 1 || kept[0]["source"] != "web" || kept[0]["kind"] != "domain" {
		t.Fatalf("alice's captured renewal: %v", kept)
	}
}

func TestPocketLinkScopes(t *testing.T) {
	req := func(m, p string) *http.Request { return httptest.NewRequest(m, p, nil) }
	for _, c := range []struct {
		scope string
		r     *http.Request
		want  bool
	}{
		{core.ScopeSearch, req("GET", "/api/v1/pocket/search?q=x"), true},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/2026-01-02"), true},
		{core.ScopeWidget, req("GET", "/api/v1/pocket/widget"), true},
		{core.ScopeCapture, req("POST", "/api/v1/pocket/capture"), true},
		{core.ScopeWidget, req("GET", "/api/v1/auth/me"), true},
		{core.ScopeSearch, req("POST", "/api/v1/pocket/search"), false},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/today"), false},
		{core.ScopeDay, req("GET", "/api/v1/day/2026-01-02"), false},
		{core.ScopeWidget, req("GET", "/api/v1/due"), false},
		{core.ScopeCapture, req("GET", "/api/v1/pocket/capture"), false},
		{core.ScopeCapture, req("POST", "/api/v1/renewals"), false},
		{"renewals:read", req("GET", "/api/v1/pocket/widget"), false},
		{"renewals:write", req("POST", "/api/v1/pocket/capture"), false},
	} {
		if got := linkAllows([]string{c.scope}, c.r); got != c.want {
			t.Errorf("%s %s %s = %v, want %v", c.scope, c.r.Method, c.r.URL.Path, got, c.want)
		}
	}
}

// Renewals tells Core what it can do, so Search, Day and Hub find it on their own.
func TestManifestCapabilities(t *testing.T) {
	a, fc := newCapApp(t)
	if err := a.s.registerWithCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	fc.mu.Lock()
	m := fc.manifest
	fc.mu.Unlock()
	if m["slug"] != "renewals" || m["category"] != "Money" {
		t.Fatalf("manifest: %v %v", m["slug"], m["category"])
	}
	search, _ := m["search"].(map[string]any)
	day, _ := m["day"].(map[string]any)
	widget, _ := m["widget"].(map[string]any)
	cap, _ := m["capture"].(map[string]any)
	if search == nil || day == nil || day["label"] != "Renewals" || day["one"] != "renewal" || day["many"] != "renewals" || widget == nil || widget["title"] != "Renewals" || cap == nil {
		t.Fatalf("capabilities: %v %v %v %v", search, day, widget, cap)
	}
	if b := search["bangs"].([]any); len(b) != 3 || b[0] != "renewals" || b[1] != "renew" || b[2] != "expires" {
		t.Fatalf("bangs: %v", b)
	}
	if k := cap["kinds"].([]any)[0].(map[string]any); k["kind"] != "renewal" || len(k["fields"].([]any)) != 1 || k["fields"].([]any)[0] != "date" {
		t.Fatalf("capture kinds: %v", cap)
	}
	offered := map[string]bool{}
	for _, o := range m["offers"].([]any) {
		offered[o.(map[string]any)["scope"].(string)] = true
	}
	for _, sc := range []string{"renewals:read", "renewals:write", core.ScopeSearch, core.ScopeDay, core.ScopeWidget, core.ScopeCapture} {
		if !offered[sc] {
			t.Errorf("%s is not offered", sc)
		}
	}
}
