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

// fakeWallet answers as one person's Pocket Wallet; its cards can change
// between scans.
type fakeWallet struct {
	mu    sync.Mutex
	cards []map[string]any
	ts    *httptest.Server
}

func newFakeWallet(t *testing.T) *fakeWallet {
	fw := &fakeWallet{}
	mux := http.NewServeMux()
	j := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { j(w, map[string]any{"status": "ok", "version": "0.1.0"}) })
	mux.HandleFunc("/api/v1/cards", func(w http.ResponseWriter, r *http.Request) {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		out := []map[string]any{}
		if fw.cards != nil {
			out = fw.cards
		}
		j(w, out)
	})
	fw.ts = httptest.NewServer(mux)
	t.Cleanup(fw.ts.Close)
	return fw
}

func (fw *fakeWallet) set(cards []map[string]any) {
	fw.mu.Lock()
	fw.cards = cards
	fw.mu.Unlock()
}

func walletFixture(today string) []map[string]any {
	day := func(n int) string { return store.AddDays(today, n) }
	return []map[string]any{
		{"id": 1, "kind": "passport", "group": "ids", "name": "Passport", "issuer": "MUP", "number": "P1234567", "holder": "Ana Horvat", "valid_from": day(-3250), "expires_on": day(400)},
		{"id": 2, "kind": "id", "group": "ids", "name": "ID card", "number": "ID998", "holder": "Ana Horvat", "expires_on": day(30)},
		{"id": 3, "kind": "licence", "group": "ids", "name": "Driving licence", "number": "DL-55", "holder": "Ana Horvat", "expires_on": day(800)},
		{"id": 4, "kind": "insurance", "group": "ids", "name": "EHIC", "issuer": "HZZO", "number": "80385", "holder": "Ana Horvat", "expires_on": day(200)},
		{"id": 5, "kind": "membership", "group": "cards", "name": "FitInn gym", "issuer": "FitInn", "number": "M-42", "expires_on": day(60)},
		{"id": 6, "kind": "gift", "group": "cards", "name": "IKEA", "issuer": "IKEA", "number": "G-1", "expires_on": day(90), "balance": 2550, "currency": "EUR"},
		{"id": 7, "kind": "gift", "group": "cards", "name": "Zara", "expires_on": day(50), "balance": 0, "currency": "EUR"}, // spent
		{"id": 8, "kind": "gift", "group": "cards", "name": "Cinema voucher", "expires_on": day(120)},                       // no balance kept
		{"id": 9, "kind": "loyalty", "group": "cards", "name": "Lidl Plus", "expires_on": day(100)},                         // loyalty: not a renewal
		{"id": 10, "kind": "boarding", "group": "passes", "name": "ZAG → AMS", "expires_on": day(10)},                       // a pass
		{"id": 11, "kind": "passport", "group": "ids", "name": "Old passport", "expires_on": day(20), "archived": true},     // archived
		{"id": 12, "kind": "id", "group": "ids", "name": "Library ID", "expires_on": ""},                                    // no end date
		{"id": 13, "kind": "membership", "group": "cards", "name": "Ski club", "expires_on": day(-400)},                     // long gone
	}
}

func walletByRef(t *testing.T, a *app, auth string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, x := range a.list(auth, "/api/v1/renewals?source=wallet") {
		m := x.(map[string]any)
		out[m["source_ref"].(string)] = m
	}
	return out
}

func TestWalletScan(t *testing.T) {
	a := newApp(t, nil)
	ctx := context.Background()
	uid := a.ids["Alice"]
	today := store.Today()
	fw := newFakeWallet(t)
	fw.set(walletFixture(today))
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"wallet_mode": "direct", "wallet_url": fw.ts.URL, "wallet_token": "tok-wallet-123456"})
	src, ok := sourceByName("wallet")
	if !ok || src.App.slug != "wallet" {
		t.Fatalf("no wallet source")
	}
	sc, err := a.s.runScan(ctx, uid, src, "manual", 0)
	if err != nil || sc.Added != 7 {
		t.Fatalf("wallet scan: %+v %v", sc, err)
	}
	got := walletByRef(t, a, a.alice)
	if len(got) != 7 {
		t.Fatalf("found: %v", got)
	}
	pp := got["wallet:card:1"]
	if pp == nil || pp["kind"] != "passport" || pp["group"] != "identity" || pp["title"] != "Passport" || pp["holder"] != "Ana Horvat" || pp["number"] != "P1234567" ||
		pp["provider"] != "MUP" || pp["issued_on"] != store.AddDays(today, -3250) || pp["expires_on"] != store.AddDays(today, 400) ||
		pp["source_url"] != fw.ts.URL+"/cards/1" || pp["needs_check"] != true || pp["found_by"] != "a passport in Pocket Wallet" {
		t.Fatalf("the passport: %v", pp)
	}
	for ref, kind := range map[string]string{"wallet:card:2": "id_card", "wallet:card:3": "driving_licence", "wallet:card:4": "insurance", "wallet:card:5": "membership", "wallet:card:6": "other", "wallet:card:8": "other"} {
		if got[ref] == nil || got[ref]["kind"] != kind {
			t.Fatalf("%s should be a %s: %v", ref, kind, got[ref])
		}
	}
	gift := got["wallet:card:6"]
	if gift["title"] != "Gift card: IKEA" || !strings.Contains(gift["found_by"].(string), "25.50 EUR left") || gift["provider"] != "IKEA" {
		t.Fatalf("the gift card: %v", gift)
	}
	if v := got["wallet:card:8"]; v["title"] != "Gift card: Cinema voucher" || strings.Contains(v["found_by"].(string), "left") {
		t.Fatalf("the voucher: %v", v)
	}
	for _, ref := range []string{"7", "9", "10", "11", "12", "13"} { // spent, loyalty, a pass, archived, no date, long gone
		if got["wallet:card:"+ref] != nil {
			t.Fatalf("card %s should not be there: %v", ref, got["wallet:card:"+ref])
		}
	}

	// again: nothing new
	if sc, _ := a.s.runScan(ctx, uid, src, "manual", 0); sc.Added != 0 || sc.Updated != 0 {
		t.Fatalf("second scan: %+v", sc)
	}

	// a new passport date in the wallet moves it on here
	cards := walletFixture(today)
	cards[0]["expires_on"] = store.AddDays(today, 3650)
	fw.set(cards)
	if sc, err := a.s.runScan(ctx, uid, src, "manual", 0); err != nil || sc.Added != 0 || sc.Updated != 1 {
		t.Fatalf("after the new passport: %+v %v", sc, err)
	}
	if got := walletByRef(t, a, a.alice); len(got) != 7 || got["wallet:card:1"]["expires_on"] != store.AddDays(today, 3650) {
		t.Fatalf("after the new passport: %v", got)
	}

	// Bob has no Wallet connection of his own and sees none of Alice's
	if _, err := a.s.runScan(ctx, a.ids["Bob"], src, "manual", 0); err == nil {
		t.Fatalf("bob scanned with alice's connection")
	}
	if n := len(a.list(a.bob, "/api/v1/renewals")); n != 0 {
		t.Fatalf("bob sees %d renewals", n)
	}
}

func TestWalletEventsRescan(t *testing.T) {
	liveDelay = 10 * time.Millisecond
	a := newApp(t, nil)
	today := store.Today()
	fw := newFakeWallet(t)
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"wallet_mode": "direct", "wallet_url": fw.ts.URL, "wallet_token": "tok-wallet-123456"})
	// Bob has his own Wallet, with nothing in it
	empty := newFakeWallet(t)
	a.ok(a.bob, "PUT", "/api/v1/settings", map[string]any{"wallet_mode": "direct", "wallet_url": empty.ts.URL, "wallet_token": "tok-wallet-bob-1234"})
	fw.set(walletFixture(today))
	ev := func(topic, email string) core.Event {
		p, _ := json.Marshal(map[string]any{"user_email": email, "card_id": 1, "kind": "passport", "group": "ids", "name": "Passport", "expires_on": store.AddDays(today, 400), "path": "/cards/1"})
		return core.Event{ID: 1, Topic: topic, Payload: p, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	}
	for _, topic := range []string{"wallet.card_saved", "wallet.card_removed"} {
		if eventSources[topic] != "wallet" {
			t.Fatalf("%s is not followed", topic)
		}
	}
	if !strings.Contains(streamTopics(), "wallet.card_saved") {
		t.Fatalf("stream topics: %s", streamTopics())
	}
	ctx := context.Background()
	a.s.handleEvent(ctx, ev("wallet.pass_today", "alice@example.com"))  // not followed
	a.s.handleEvent(ctx, ev("wallet.card_saved", "nobody@example.com")) // not a person here
	a.s.handleEvent(ctx, ev("wallet.card_saved", "alice@example.com"))
	a.s.handleEvent(ctx, ev("wallet.card_removed", "alice@example.com")) // joins the same scan
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if l := a.list(a.alice, "/api/v1/renewals?source=wallet"); len(l) == 7 {
			if n := len(a.list(a.bob, "/api/v1/renewals")); n != 0 {
				t.Fatalf("bob got alice's event: %d", n)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the cards did not arrive: %v", a.list(a.alice, "/api/v1/renewals"))
}

func TestWalletLinkDeclared(t *testing.T) {
	app, ok := appBySlug("wallet")
	if !ok || len(app.scopes) != 2 || app.scopes[0] != scopeWalletRead || app.scopes[1] != scopeEventsRead || app.reason == "" {
		t.Fatalf("wallet app: %+v", app)
	}
	for _, u := range linkUses() {
		if u.App == "wallet" {
			return
		}
	}
	t.Fatalf("wallet is not in the manifest's Uses")
}
