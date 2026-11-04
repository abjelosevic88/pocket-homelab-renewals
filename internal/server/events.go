package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// ---- new and changed items in the other apps, as they happen ----
//
// Renewals follows Pocket Core's event bus over SSE with its app token. Core
// sends only what Renewals may read: the events of people who have the
// Renewals → <app> events:read link switched on. A processed or changed
// document is read at once (scanDocuments with just that one), so a passport
// scanned into Pocket Documents shows up here within seconds; an appliance,
// a vehicle reminder or paper, an item, a subscription or a certificate that
// was saved starts a quick look through that app for that person (a few
// seconds later, once for a burst of saves). The last event id is kept, so
// after a restart the stream resumes where it was; events older than a day
// are skipped (the nightly scan catches those).

const cursorKey = "events:cursor"

// eventSources maps the topics followed to the source a change belongs to.
var eventSources = map[string]string{
	"documents.processed": "documents", "documents.updated": "documents",
	"home.thing_saved":      "home",
	"garage.reminder_saved": "garage", "garage.document_saved": "garage",
	"inventory.item_created": "inventory", "inventory.item_updated": "inventory",
	"money.subscription_saved": "money", "money.subscription_paid": "money",
	"monitoring.cert_changed": "monitoring",
}

func streamTopics() string {
	topics := make([]string, 0, len(eventSources))
	for t := range eventSources {
		topics = append(topics, t)
	}
	sort.Strings(topics)
	return strings.Join(topics, ",")
}

func (s *Server) setStream(ok bool, err string) {
	s.streamMu.Lock()
	s.streamOK, s.streamErr = ok, err
	s.streamMu.Unlock()
}

func (s *Server) streamStatus() map[string]any {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	out := map[string]any{"configured": s.core.Configured(), "connected": s.streamOK, "error": s.streamErr}
	if !s.lastEvent.IsZero() {
		out["last_event_at"] = s.lastEvent.UTC().Format(time.RFC3339)
	}
	return out
}

// listen follows the bus until ctx ends, reconnecting with backoff.
func (s *Server) listen(ctx context.Context) {
	wait := 2 * time.Second
	for ctx.Err() == nil {
		if s.core.Token() == "" { // not registered yet
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		err := s.streamOnce(ctx, s.cursor(ctx))
		if ctx.Err() != nil {
			return
		}
		msg := "the stream ended"
		if err != nil {
			msg = err.Error()
		}
		s.setStream(false, msg)
		s.log.Debug("pocket core: event stream closed, reconnecting", "err", msg, "in", wait.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err == nil {
			wait = 2 * time.Second
		} else if wait < time.Minute {
			wait *= 2
		}
	}
}

// cursor is the last event id seen; on the very first start it is the
// newest event on the bus, so old history is left to the scans.
func (s *Server) cursor(ctx context.Context) int64 {
	if v, _, ok := s.store.CacheGet(ctx, cursorKey); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	var last int64
	if evs, err := s.core.Events(ctx, "", 0, 1); err == nil && len(evs) > 0 {
		last = evs[0].ID
	}
	_ = s.store.CachePut(ctx, cursorKey, strconv.FormatInt(last, 10))
	return last
}

func (s *Server) streamOnce(ctx context.Context, after int64) error {
	q := url.Values{"topics": {streamTopics()}}
	if after > 0 {
		q.Set("after", strconv.FormatInt(after, 10))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.CoreURL+"/api/v1/events/stream?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.core.Token())
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", s.cfg.UserAgent)
	if after > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(after, 10))
	}
	res, err := (&http.Client{}).Do(req) // long-lived: no timeout
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return fmt.Errorf("Pocket Core answered %d %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	s.setStream(true, "")
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if data.Len() > 0 {
				var e core.Event
				if json.Unmarshal([]byte(data.String()), &e) == nil && e.ID > 0 {
					s.handleEvent(ctx, e)
					_ = s.store.CachePut(ctx, cursorKey, strconv.FormatInt(e.ID, 10))
				}
				data.Reset()
			}
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return sc.Err()
}

// eventOwner is the person an event belongs to.
func eventOwner(p map[string]any) string {
	for _, k := range []string{"user_email", "owner_email", "user"} {
		if v, ok := p[k].(string); ok && strings.Contains(v, "@") {
			return strings.ToLower(strings.TrimSpace(v))
		}
	}
	return ""
}

// handleEvent reads what an event is about, for its owner: the one document,
// or (for the other apps) a quick scan of that app.
func (s *Server) handleEvent(ctx context.Context, e core.Event) {
	name, ok := eventSources[e.Topic]
	if !ok {
		return
	}
	if at, err := time.Parse(time.RFC3339Nano, e.CreatedAt); err == nil && time.Since(at) > 24*time.Hour {
		return
	}
	var p map[string]any
	if json.Unmarshal(e.Payload, &p) != nil {
		return
	}
	email := eventOwner(p)
	if email == "" {
		return
	}
	u, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		return // not a person of this box (yet)
	}
	st, err := s.store.GetSettings(ctx, u.ID)
	if err != nil || !st.Live || !st.Source(name) {
		return
	}
	s.streamMu.Lock()
	s.lastEvent = time.Now()
	s.streamMu.Unlock()
	src, _ := sourceByName(name)
	if name == "documents" {
		id, _ := p["id"].(float64)
		if id <= 0 {
			return
		}
		go func() {
			bctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			// the AI in Documents may still be filling in fields: give it a moment
			if e.Topic == "documents.processed" {
				select {
				case <-bctx.Done():
					return
				case <-time.After(3 * time.Second):
				}
			}
			if _, err := s.runScan(bctx, u.ID, src, "event", int64(id)); err != nil {
				s.log.Debug("documents event: reading the document failed", "doc", int64(id), "err", err)
			}
		}()
		return
	}
	s.rescanSoon(u.ID, src, liveDelay)
}

// liveDelay gathers a burst of saves into one scan.
var liveDelay = 5 * time.Second

// rescanSoon looks through one app for one person a moment from now; saves
// arriving meanwhile join that one scan. A scan already running is waited
// for, so the change it may have missed is read right after.
func (s *Server) rescanSoon(uid int64, src scanSource, wait time.Duration) {
	key := s.scanKey(uid, src.Name)
	s.scanMu.Lock()
	if s.pending[key] {
		s.scanMu.Unlock()
		return
	}
	s.pending[key] = true
	s.scanMu.Unlock()
	time.AfterFunc(wait, func() {
		s.scanMu.Lock()
		delete(s.pending, key)
		s.scanMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if _, err := s.runScan(ctx, uid, src, "event", 0); err != nil {
			if errors.Is(err, errBusy) {
				s.rescanSoon(uid, src, 30*time.Second)
				return
			}
			s.log.Debug("event: looking through an app failed", "source", src.Name, "err", err)
		}
	})
}
