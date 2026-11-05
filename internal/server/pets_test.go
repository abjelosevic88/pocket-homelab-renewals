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

// fakePets answers as one person's Pocket Pets; its pets and health log can
// change between scans.
type fakePets struct {
	mu      sync.Mutex
	pets    []map[string]any
	records []map[string]any
	ts      *httptest.Server
}

func newFakePets(t *testing.T) *fakePets {
	fp := &fakePets{}
	mux := http.NewServeMux()
	j := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { j(w, map[string]any{"status": "ok", "version": "0.1.0"}) })
	mux.HandleFunc("/api/v1/pets", func(w http.ResponseWriter, r *http.Request) {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		j(w, fp.pets)
	})
	mux.HandleFunc("/api/v1/records", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("kind") != "vaccine" {
			http.Error(w, "want kind=vaccine", http.StatusBadRequest)
			return
		}
		fp.mu.Lock()
		defer fp.mu.Unlock()
		j(w, map[string]any{"records": fp.records, "total": len(fp.records)})
	})
	fp.ts = httptest.NewServer(mux)
	t.Cleanup(fp.ts.Close)
	return fp
}

func (fp *fakePets) set(pets, records []map[string]any) {
	fp.mu.Lock()
	fp.pets, fp.records = pets, records
	fp.mu.Unlock()
}

func petsFixture(today string) (pets, records []map[string]any) {
	day := func(n int) string { return store.AddDays(today, n) }
	pets = []map[string]any{
		{"id": 1, "name": "Luna", "species": "dog", "insurer": "Allianz", "policy_no": "PET-778", "insured_until": day(200), "currency": "EUR"},
		{"id": 2, "name": "Miša", "species": "cat", "insured_until": ""},
		{"id": 3, "name": "Rex", "species": "dog", "insurer": "Uniqa", "insured_until": day(100), "passed_on": day(-30)},
		{"id": 4, "name": "Old Tom", "species": "cat", "insurer": "Uniqa", "insured_until": day(90), "archived": true},
	}
	records = []map[string]any{ // newest first, as Pets answers
		{"id": 21, "pet_id": 1, "pet_name": "Luna", "kind": "vaccine", "title": "Rabies", "date": day(-10), "valid_until": day(355), "cost": 2500, "currency": "EUR", "pro_name": "Vet Zoo"},
		{"id": 20, "pet_id": 2, "pet_name": "Miša", "kind": "vaccine", "title": "Cat flu", "date": day(-20), "valid_until": ""},
		{"id": 15, "pet_id": 3, "pet_name": "Rex", "kind": "vaccine", "title": "Rabies", "date": day(-40), "valid_until": day(325)},
		{"id": 12, "pet_id": 1, "pet_name": "Luna", "kind": "vaccine", "title": "DHPPi", "date": day(-300), "valid_until": day(65)},
		{"id": 10, "pet_id": 1, "pet_name": "Luna", "kind": "vaccine", "title": "rabies ", "date": day(-375), "valid_until": day(-10)},
	}
	return
}

func petsByTitle(t *testing.T, a *app, auth string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, x := range a.list(auth, "/api/v1/renewals?source=pets") {
		m := x.(map[string]any)
		out[m["title"].(string)] = m
	}
	return out
}

func TestPetsScan(t *testing.T) {
	a := newApp(t, nil)
	ctx := context.Background()
	uid := a.ids["Alice"]
	today := store.Today()
	fp := newFakePets(t)
	fp.set(petsFixture(today))
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"pets_mode": "direct", "pets_url": fp.ts.URL, "pets_token": "tok-pets-123456"})
	src, ok := sourceByName("pets")
	if !ok || src.App.slug != "pets" {
		t.Fatalf("no pets source")
	}
	sc, err := a.s.runScan(ctx, uid, src, "manual", 0)
	if err != nil || sc.Added != 3 {
		t.Fatalf("pets scan: %+v %v", sc, err)
	}
	got := petsByTitle(t, a, a.alice)
	if len(got) != 3 {
		t.Fatalf("found: %v", got)
	}
	ins := got["Luna: Pet insurance"]
	if ins == nil || ins["kind"] != "insurance" || ins["group"] != "insurance" || ins["holder"] != "Luna" || ins["provider"] != "Allianz" || ins["number"] != "PET-778" ||
		ins["expires_on"] != store.AddDays(today, 200) || ins["source_url"] != fp.ts.URL+"/pets/1" || ins["needs_check"] != true {
		t.Fatalf("the insurance: %v", ins)
	}
	rab := got["Luna: Rabies vaccine"]
	if rab == nil || rab["kind"] != "health" || rab["group"] != "health" || rab["holder"] != "Luna" || rab["expires_on"] != store.AddDays(today, 355) ||
		rab["source_url"] != fp.ts.URL+"/log/21" || num(rab, "cost") != 2500 || rab["provider"] != "Vet Zoo" {
		t.Fatalf("the latest rabies vaccine: %v", rab)
	}
	if got["Luna: DHPPi vaccine"] == nil {
		t.Fatalf("the other vaccine: %v", got)
	}
	for title := range got { // Rex passed away, Old Tom is archived, Miša's vaccine has no end date
		if strings.Contains(title, "Rex") || strings.Contains(title, "Tom") || strings.Contains(title, "Miša") {
			t.Fatalf("%q should not be there", title)
		}
	}

	// again: nothing new
	if sc, _ := a.s.runScan(ctx, uid, src, "manual", 0); sc.Added != 0 || sc.Updated != 0 {
		t.Fatalf("second scan: %+v", sc)
	}

	// a booster: the newer record moves the date and the link on, no second renewal
	pets, recs := petsFixture(today)
	recs = append([]map[string]any{{"id": 30, "pet_id": 1, "pet_name": "Luna", "kind": "vaccine", "title": "RABIES", "date": today, "valid_until": store.AddDays(today, 3*365)}}, recs...)
	pets[0]["insured_until"] = store.AddDays(today, 565)
	fp.set(pets, recs)
	if sc, err := a.s.runScan(ctx, uid, src, "manual", 0); err != nil || sc.Added != 0 || sc.Updated != 2 {
		t.Fatalf("after the booster: %+v %v", sc, err)
	}
	got = petsByTitle(t, a, a.alice)
	if len(got) != 3 || got["Luna: Rabies vaccine"]["expires_on"] != store.AddDays(today, 3*365) || got["Luna: Rabies vaccine"]["source_url"] != fp.ts.URL+"/log/30" ||
		got["Luna: Pet insurance"]["expires_on"] != store.AddDays(today, 565) {
		t.Fatalf("after the booster: %v", got)
	}

	// Bob has no Pets connection of his own and sees none of Alice's
	if _, err := a.s.runScan(ctx, a.ids["Bob"], src, "manual", 0); err == nil {
		t.Fatalf("bob scanned with alice's connection")
	}
	if n := len(a.list(a.bob, "/api/v1/renewals")); n != 0 {
		t.Fatalf("bob sees %d renewals", n)
	}
}

func TestPetsEventsRescan(t *testing.T) {
	liveDelay = 10 * time.Millisecond
	a := newApp(t, nil)
	today := store.Today()
	fp := newFakePets(t)
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"pets_mode": "direct", "pets_url": fp.ts.URL, "pets_token": "tok-pets-123456"})
	// Bob has his own Pets, with nothing in it
	empty := newFakePets(t)
	a.ok(a.bob, "PUT", "/api/v1/settings", map[string]any{"pets_mode": "direct", "pets_url": empty.ts.URL, "pets_token": "tok-pets-bob-1234"})
	fp.set(petsFixture(today))
	ev := func(topic, email string) core.Event {
		p, _ := json.Marshal(map[string]any{"user_email": email, "record_id": 21, "pet_id": 1, "kind": "vaccine", "title": "Rabies", "pet": "Luna", "valid_until": store.AddDays(today, 355)})
		return core.Event{ID: 1, Topic: topic, Payload: p, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	}
	for _, topic := range []string{"pets.pet_saved", "pets.record_added", "pets.record_saved"} {
		if eventSources[topic] != "pets" {
			t.Fatalf("%s is not followed", topic)
		}
	}
	if !strings.Contains(streamTopics(), "pets.record_added") {
		t.Fatalf("stream topics: %s", streamTopics())
	}
	ctx := context.Background()
	a.s.handleEvent(ctx, ev("pets.weight_added", "alice@example.com"))  // not followed
	a.s.handleEvent(ctx, ev("pets.record_added", "nobody@example.com")) // not a person here
	a.s.handleEvent(ctx, ev("pets.record_added", "alice@example.com"))
	a.s.handleEvent(ctx, ev("pets.pet_saved", "alice@example.com")) // joins the same scan
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if l := a.list(a.alice, "/api/v1/renewals?source=pets"); len(l) == 3 {
			if n := len(a.list(a.bob, "/api/v1/renewals")); n != 0 {
				t.Fatalf("bob got alice's event: %d", n)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the pets did not arrive: %v", a.list(a.alice, "/api/v1/renewals"))
}

func TestPetsLinkDeclared(t *testing.T) {
	app, ok := appBySlug("pets")
	if !ok || len(app.scopes) != 2 || app.scopes[0] != scopePetsRead || app.scopes[1] != scopeEventsRead || app.reason == "" {
		t.Fatalf("pets app: %+v", app)
	}
	for _, u := range linkUses() {
		if u.App == "pets" {
			return
		}
	}
	t.Fatalf("pets is not in the manifest's Uses")
}
