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

// fakeTasks answers as one person's Pocket Tasks.
type fakeTasks struct {
	mu    sync.Mutex
	tasks []map[string]any
	asked []string
	ts    *httptest.Server
}

func newFakeTasks(t *testing.T) *fakeTasks {
	ft := &fakeTasks{}
	mux := http.NewServeMux()
	j := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { j(w, map[string]any{"status": "ok", "version": "0.1.0"}) })
	mux.HandleFunc("/api/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		ft.mu.Lock()
		defer ft.mu.Unlock()
		q := r.URL.Query()
		ft.asked = append(ft.asked, q.Get("status")+"|"+q.Get("due_from"))
		out := []map[string]any{}
		for _, t := range ft.tasks {
			st, _ := t["status"].(string)
			due, _ := t["due"].(string)
			if q.Get("status") == "open" && st != "open" && st != "in_progress" {
				continue
			}
			if f := q.Get("due_from"); f != "" && (due == "" || due[:10] < f) {
				continue
			}
			out = append(out, t)
		}
		j(w, map[string]any{"tasks": out, "total": len(out)})
	})
	ft.ts = httptest.NewServer(mux)
	t.Cleanup(ft.ts.Close)
	return ft
}

func (ft *fakeTasks) set(tasks []map[string]any) {
	ft.mu.Lock()
	ft.tasks = tasks
	ft.mu.Unlock()
}

func tasksFixture(today string) []map[string]any {
	day := func(n int) string { return store.AddDays(today, n) }
	return []map[string]any{
		{"id": 1, "title": "Renew passport", "status": "open", "due": day(40), "tags": []string{}, "list": map[string]any{"name": "Inbox"}},
		{"id": 2, "title": "Car registration", "status": "in_progress", "due": day(20) + "T09:00", "tags": []string{}},
		{"id": 3, "title": "Pay the plumber", "status": "open", "due": day(5), "tags": []string{"Renewal"}},       // tagged
		{"id": 4, "title": "Go to the gym", "status": "open", "due": day(2), "tags": []string{}},                  // a chore
		{"id": 5, "title": "Renew domain", "status": "open", "tags": []string{}},                                 // no date
		{"id": 6, "title": "Renew insurance", "status": "done", "due": day(10), "tags": []string{}},               // done
		{"id": 7, "title": "Renew ID card", "status": "open", "due": day(30), "url": "http://renewals.test/r/9"}, // Renewals' own
		{"id": 8, "title": "Obnoviti ličnu kartu", "status": "open", "due": day(60)},
	}
}

func tasksByRef(t *testing.T, a *app, auth string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, x := range a.list(auth, "/api/v1/renewals?source=tasks") {
		m := x.(map[string]any)
		out[m["source_ref"].(string)] = m
	}
	return out
}

func TestTasksScan(t *testing.T) {
	a := newApp(t, nil)
	a.s.cfg.BaseURL = "http://renewals.test"
	ctx := context.Background()
	uid := a.ids["Alice"]
	today := store.Today()
	ft := newFakeTasks(t)
	ft.set(tasksFixture(today))
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"tasks_mode": "direct", "tasks_url": ft.ts.URL, "tasks_token": "tok-tasks-123456"})
	src, ok := sourceByName("tasks")
	if !ok || src.App.slug != "tasks" {
		t.Fatalf("no tasks source")
	}
	sc, err := a.s.runScan(ctx, uid, src, "manual", 0)
	if err != nil || sc.Added != 4 {
		t.Fatalf("tasks scan: %+v %v (%v)", sc, err, tasksByRef(t, a, a.alice))
	}
	if len(ft.asked) == 0 || !strings.HasPrefix(ft.asked[0], "open|") {
		t.Fatalf("asked for tasks with %v", ft.asked)
	}
	got := tasksByRef(t, a, a.alice)
	p := got["tasks:task:1"]
	if p == nil || p["title"] != "Passport" || p["kind"] != "passport" || p["expires_on"] != store.AddDays(today, 40) ||
		p["source_url"] != ft.ts.URL+"/tasks/1" || p["needs_check"] != true || p["found_by"] != "a task due that day in Pocket Tasks (list Inbox)" {
		t.Fatalf("the passport task: %v", p)
	}
	if c := got["tasks:task:2"]; c == nil || c["kind"] != "vehicle" || c["expires_on"] != store.AddDays(today, 20) || c["title"] != "Car registration" {
		t.Fatalf("the registration task: %v", c)
	}
	if got["tasks:task:3"] == nil || got["tasks:task:8"] == nil || got["tasks:task:8"]["kind"] != "id_card" {
		t.Fatalf("tagged / Serbian tasks: %v", got)
	}
	for _, ref := range []string{"tasks:task:4", "tasks:task:5", "tasks:task:6", "tasks:task:7"} {
		if got[ref] != nil {
			t.Fatalf("%s should be left out: %v", ref, got[ref])
		}
	}

	// the due date moves in Tasks: it moves here
	tasks := tasksFixture(today)
	tasks[0]["due"] = store.AddDays(today, 70)
	ft.set(tasks)
	if sc, err := a.s.runScan(ctx, uid, src, "manual", 0); err != nil || sc.Added != 0 || sc.Updated != 1 {
		t.Fatalf("after moving the date: %+v %v", sc, err)
	}

	// a "renew it" task Renewals added (remembered by id) is not found back
	r := got["tasks:task:3"]
	id := int64(r["id"].(float64))
	tid := int64(11)
	if err := a.s.store.SetTask(ctx, uid, id, &tid); err != nil {
		t.Fatal(err)
	}
	tasks = append(tasks, map[string]any{"id": 11, "title": "Renew pay the plumber", "status": "open", "due": store.AddDays(today, 5)})
	ft.set(tasks)
	if sc, _ := a.s.runScan(ctx, uid, src, "manual", 0); sc.Added != 0 {
		t.Fatalf("found its own task: %+v", sc)
	}

	if n := len(a.list(a.bob, "/api/v1/renewals")); n != 0 {
		t.Fatalf("bob sees %d renewals", n)
	}
}

func TestTasksEventsRescan(t *testing.T) {
	liveDelay = 10 * time.Millisecond
	a := newApp(t, nil)
	ft := newFakeTasks(t)
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"tasks_mode": "direct", "tasks_url": ft.ts.URL, "tasks_token": "tok-tasks-123456"})
	ft.set(tasksFixture(store.Today()))
	for _, topic := range []string{"tasks.created", "tasks.updated"} {
		if eventSources[topic] != "tasks" || !strings.Contains(streamTopics(), topic) {
			t.Fatalf("%s is not followed: %s", topic, streamTopics())
		}
	}
	p, _ := json.Marshal(map[string]any{"user_email": "alice@example.com", "id": 1, "title": "Renew passport"})
	a.s.handleEvent(context.Background(), core.Event{ID: 1, Topic: "tasks.created", Payload: p, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if l := a.list(a.alice, "/api/v1/renewals?source=tasks"); len(l) == 4 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the tasks did not arrive: %v", a.list(a.alice, "/api/v1/renewals"))
}

func TestTasksLinkDeclared(t *testing.T) {
	app, ok := appBySlug("tasks")
	if !ok || strings.Join(app.scopes, ",") != "tasks:read,tasks:write,events:read" || app.reason == "" {
		t.Fatalf("tasks app: %+v", app)
	}
}
