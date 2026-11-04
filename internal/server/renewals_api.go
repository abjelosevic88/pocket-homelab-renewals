package server

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-renewals/internal/store"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func cleanURL(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", true
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return v, true
}

func cleanCurrency(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	if len(v) != 3 {
		return ""
	}
	for _, c := range v {
		if c < 'A' || c > 'Z' {
			return ""
		}
	}
	return v
}

// linkMayChange: a link token may only change what its own app added.
func linkMayChange(ctx context.Context, source string) bool {
	app := linkApp(ctx)
	return app == "" || app == source
}

type renewalInput struct {
	Title         *string `json:"title"`
	Kind          *string `json:"kind"`
	Holder        *string `json:"holder"`
	Provider      *string `json:"provider"`
	Number        *string `json:"number"`
	IssuedOn      *string `json:"issued_on"`
	ExpiresOn     *string `json:"expires_on"`
	RenewN        *int    `json:"renew_n"`
	RenewUnit     *string `json:"renew_unit"`
	AutoRenews    *bool   `json:"auto_renews"`
	RemindDays    *string `json:"remind_days"`
	LeadDays      *int    `json:"lead_days"`
	Cost          *int64  `json:"cost"`
	Currency      *string `json:"currency"`
	RenewURL      *string `json:"renew_url"`
	Notes         *string `json:"notes"`
	DocumentID    *int64  `json:"document_id"`
	DocumentTitle *string `json:"document_title"`
	ContactID     *int64  `json:"contact_id"`
	ContactName   *string `json:"contact_name"`
	Archived      *bool   `json:"archived"`
	NeedsCheck    *bool   `json:"needs_check"`
	SourceURL     *string `json:"source_url"`
	ExternalID    *string `json:"external_id"` // a linked app's own id, kept as the source item
}

// apply copies what was sent; "" = fine, else what is wrong.
func (in renewalInput) apply(r *store.Renewal, keys map[string]bool) string {
	str := func(k string, dst *string, v *string, max int) {
		if keys[k] && v != nil {
			t := strings.TrimSpace(*v)
			if len(t) > max {
				t = t[:max]
			}
			*dst = t
		}
	}
	str("title", &r.Title, in.Title, 200)
	str("holder", &r.Holder, in.Holder, 120)
	str("provider", &r.Provider, in.Provider, 160)
	str("number", &r.Number, in.Number, 120)
	str("notes", &r.Notes, in.Notes, 8000)
	str("document_title", &r.DocumentTitle, in.DocumentTitle, 200)
	str("contact_name", &r.ContactName, in.ContactName, 160)
	str("remind_days", &r.RemindDays, in.RemindDays, 120)
	if keys["kind"] && in.Kind != nil {
		if !store.ValidKind(*in.Kind) {
			return "unknown kind"
		}
		r.Kind = *in.Kind
	}
	for _, d := range []struct {
		k   string
		dst *string
		v   *string
	}{{"issued_on", &r.IssuedOn, in.IssuedOn}, {"expires_on", &r.ExpiresOn, in.ExpiresOn}} {
		if keys[d.k] && d.v != nil {
			if !validDate(*d.v) {
				return "dates are YYYY-MM-DD"
			}
			*d.dst = *d.v
		}
	}
	if keys["renew_n"] && in.RenewN != nil {
		if *in.RenewN < 0 || *in.RenewN > 1000 {
			return "the term is 0 to 1000"
		}
		r.RenewN = *in.RenewN
	}
	if keys["renew_unit"] && in.RenewUnit != nil {
		if !store.ValidUnit(*in.RenewUnit) {
			return "the term is in days, weeks, months or years"
		}
		r.RenewUnit = *in.RenewUnit
	}
	if keys["lead_days"] && in.LeadDays != nil {
		if *in.LeadDays < 0 || *in.LeadDays > 730 {
			return "the lead time is 0 to 730 days"
		}
		r.LeadDays = *in.LeadDays
	}
	if keys["cost"] {
		if in.Cost != nil && *in.Cost < 0 {
			return "the cost cannot be negative"
		}
		r.Cost = in.Cost
	}
	if keys["currency"] && in.Currency != nil {
		c := cleanCurrency(*in.Currency)
		if c == "" {
			return "currency is a three-letter code like EUR"
		}
		r.Currency = c
	}
	for _, u := range []struct {
		k   string
		dst *string
		v   *string
	}{{"renew_url", &r.RenewURL, in.RenewURL}, {"source_url", &r.SourceURL, in.SourceURL}} {
		if keys[u.k] && u.v != nil {
			v, ok := cleanURL(*u.v)
			if !ok {
				return "links start with http:// or https://"
			}
			*u.dst = v
		}
	}
	if keys["document_id"] {
		r.DocumentID = in.DocumentID
		if in.DocumentID == nil {
			r.DocumentTitle = ""
		}
	}
	if keys["contact_id"] {
		r.ContactID = in.ContactID
		if in.ContactID == nil {
			r.ContactName = ""
		}
	}
	if keys["auto_renews"] && in.AutoRenews != nil {
		r.AutoRenews = *in.AutoRenews
	}
	if keys["archived"] && in.Archived != nil {
		r.Archived = *in.Archived
	}
	if keys["needs_check"] && in.NeedsCheck != nil {
		r.NeedsCheck = *in.NeedsCheck
	}
	return ""
}

func renewalPayload(r *store.Renewal) map[string]any {
	return map[string]any{"renewal_id": r.ID, "title": r.Title, "kind": r.Kind, "holder": r.Holder, "provider": r.Provider,
		"expires_on": r.ExpiresOn, "days_left": r.DaysLeft, "status": r.Status, "source": r.Source}
}

// withLinks fills where to open a renewal's document and source item.
type renewalOut struct {
	store.Renewal
	DocumentURL string `json:"document_url,omitempty"`
	OpenURL     string `json:"open_url,omitempty"` // here
}

func (s *Server) dress(ctx context.Context, uid int64, list []store.Renewal) []renewalOut {
	out := make([]renewalOut, len(list))
	docBase := ""
	for i, r := range list {
		out[i] = renewalOut{Renewal: r, OpenURL: s.cfg.BaseURL + "/r/" + itoa(r.ID)}
		if r.DocumentID != nil {
			if docBase == "" {
				docBase = s.siblingLink(ctx, uid, appDocuments, "")
			}
			if docBase != "" {
				out[i].DocumentURL = docBase + "/documents/" + itoa(*r.DocumentID)
			}
		}
	}
	return out
}

func (s *Server) handleListRenewals(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	f := store.RenewalFilter{Q: q.Get("q"), Kind: q.Get("kind"), Group: q.Get("group"), Holder: q.Get("holder"), Status: q.Get("status"),
		Source: q.Get("source"), Before: q.Get("before"), After: q.Get("after"), Archived: queryBool(r, "archived")}
	list, err := s.store.ListRenewals(ctx, actorID(ctx), f)
	if err != nil {
		s.fail(w, err, "renewals")
		return
	}
	if q.Get("sort") == "urgency" {
		store.SortByUrgency(list)
	}
	writeJSON(w, http.StatusOK, s.dress(ctx, actorID(ctx), list))
}

func (s *Server) handleGetRenewal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	rn, err := s.store.GetRenewal(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "renewal")
		return
	}
	hist, _ := s.store.ListHistory(ctx, uid, rn.ID, 0)
	out := s.dress(ctx, uid, []store.Renewal{*rn})[0]
	next := suggestNext(rn, store.Today())
	writeJSON(w, http.StatusOK, map[string]any{"renewal": out, "history": hist, "reminders": rn.Reminders(), "next_expires": next,
		"travel": s.travelWarningsFor(ctx, uid, rn)})
}

func (s *Server) handleCreateRenewal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in renewalInput
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	st, _ := s.store.GetSettings(ctx, uid)
	rn := &store.Renewal{Kind: "other", Currency: st.Currency, Source: "web"}
	if app := linkApp(ctx); app != "" {
		rn.Source = app
	} else if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		rn.Source = "api"
	}
	if msg := in.apply(rn, keys); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if rn.Title == "" {
		writeErr(w, http.StatusBadRequest, "give it a name")
		return
	}
	if !keys["renew_n"] && !keys["renew_unit"] {
		k := store.KindOf(rn.Kind)
		rn.RenewN, rn.RenewUnit = k.TermN, k.TermU
	}
	if in.ExternalID != nil && strings.TrimSpace(*in.ExternalID) != "" {
		rn.SourceRef = rn.Source + ":" + strings.TrimSpace(*in.ExternalID)
		if have, err := s.store.RenewalBySource(ctx, uid, rn.SourceRef); err == nil {
			writeJSON(w, http.StatusOK, s.dress(ctx, uid, []store.Renewal{*have})[0])
			return
		}
	}
	if err := s.store.CreateRenewal(ctx, uid, rn); err != nil {
		s.fail(w, err, "create renewal")
		return
	}
	s.publish(ctx, uid, "renewals.added", "renewal:"+itoa(rn.ID), renewalPayload(rn))
	s.syncCalendarSoon(uid)
	writeJSON(w, http.StatusCreated, s.dress(ctx, uid, []store.Renewal{*rn})[0])
}

func (s *Server) handleUpdateRenewal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	rn, err := s.store.GetRenewal(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "renewal")
		return
	}
	if !linkMayChange(ctx, rn.Source) {
		writeErr(w, http.StatusForbidden, "this Pocket Core link may only change what its own app added")
		return
	}
	var in renewalInput
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := in.apply(rn, keys); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if rn.Title == "" {
		writeErr(w, http.StatusBadRequest, "give it a name")
		return
	}
	if !keys["needs_check"] && linkApp(ctx) == "" {
		rn.NeedsCheck = false // edited by the person: looked at
	}
	if err := s.store.UpdateRenewal(ctx, uid, rn); err != nil {
		s.fail(w, err, "save renewal")
		return
	}
	s.syncCalendarSoon(uid)
	writeJSON(w, http.StatusOK, s.dress(ctx, uid, []store.Renewal{*rn})[0])
}

func (s *Server) handleDeleteRenewal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	rn, err := s.store.GetRenewal(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "renewal")
		return
	}
	if !linkMayChange(ctx, rn.Source) {
		writeErr(w, http.StatusForbidden, "this Pocket Core link may only change what its own app added")
		return
	}
	if _, err := s.store.DeleteRenewal(ctx, uid, rn.ID); err != nil {
		s.fail(w, err, "delete renewal")
		return
	}
	if rn.CalEventID != nil {
		s.dropCalendarEvents(uid, []store.Renewal{*rn})
	}
	w.WriteHeader(http.StatusNoContent)
}

// suggestNext is the new date a renewal usually gets: one term on from the
// old date, or from today when it ran out long ago (a new passport counts
// from the day it is issued).
func suggestNext(r *store.Renewal, today string) string {
	if r.RenewN <= 0 {
		return ""
	}
	from := r.ExpiresOn
	if from == "" || store.DaysBetween(from, today) > 30 || r.Group == "identity" {
		from = today
	}
	return store.AddInterval(from, r.RenewN, r.RenewUnit)
}

// handleRenew records a renewal: the new date, what it cost, a note, and
// (when asked) the cost as a transaction in Pocket Money.
func (s *Server) handleRenew(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	rn, err := s.store.GetRenewal(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "renewal")
		return
	}
	if !linkMayChange(ctx, rn.Source) {
		writeErr(w, http.StatusForbidden, "this Pocket Core link may only change what its own app added")
		return
	}
	var in struct {
		RenewedOn string `json:"renewed_on"`
		ExpiresOn string `json:"expires_on"`
		Cost      *int64 `json:"cost"`
		Currency  string `json:"currency"`
		Note      string `json:"note"`
		Money     *bool  `json:"money"`
		Number    string `json:"number"`
		IssuedOn  string `json:"issued_on"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.RenewedOn == "" {
		in.RenewedOn = store.Today()
	}
	if in.ExpiresOn == "" {
		in.ExpiresOn = suggestNext(rn, in.RenewedOn)
	}
	if !validDate(in.RenewedOn) || !validDate(in.ExpiresOn) || in.ExpiresOn == "" {
		writeErr(w, http.StatusBadRequest, "give the new end date (YYYY-MM-DD)")
		return
	}
	if !validDate(in.IssuedOn) {
		writeErr(w, http.StatusBadRequest, "dates are YYYY-MM-DD")
		return
	}
	h := &store.History{RenewedOn: in.RenewedOn, NewExpires: in.ExpiresOn, Note: strings.TrimSpace(in.Note), Currency: rn.Currency}
	if c := cleanCurrency(in.Currency); c != "" {
		h.Currency = c
	}
	if in.Cost != nil && *in.Cost > 0 {
		h.Cost = *in.Cost
	} else if in.Cost == nil && rn.Cost != nil {
		h.Cost = *rn.Cost
	}
	if app := linkApp(ctx); app != "" {
		h.Source = app
	}
	if in.Number != "" || in.IssuedOn != "" {
		if in.Number != "" {
			rn.Number = strings.TrimSpace(in.Number)
		}
		if in.IssuedOn != "" {
			rn.IssuedOn = in.IssuedOn
		}
		_ = s.store.UpdateRenewal(ctx, uid, rn)
	}
	if err := s.store.Renew(ctx, uid, rn, h); err != nil {
		s.fail(w, err, "renew")
		return
	}
	out := map[string]any{"renewal": s.dress(ctx, uid, []store.Renewal{*rn})[0], "history": h}
	if h.Cost > 0 && s.wantsMoney(ctx, uid, in.Money) {
		m := s.bookRenewal(ctx, uid, rn, h)
		out["money"] = m
	}
	p := renewalPayload(rn)
	p["old_expires"], p["cost"], p["currency"] = h.OldExpires, h.Cost, h.Currency
	s.publish(ctx, uid, "renewals.renewed", "renewal:"+itoa(rn.ID), p)
	s.syncCalendarSoon(uid)
	writeJSON(w, http.StatusOK, out)
}

// handleHistoryMoney books an earlier renewal's cost in Pocket Money.
func (s *Server) handleHistoryMoney(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	h, err := s.store.GetHistory(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "history")
		return
	}
	rn, err := s.store.GetRenewal(ctx, uid, h.RenewalID)
	if err != nil {
		s.fail(w, err, "renewal")
		return
	}
	writeJSON(w, http.StatusOK, s.bookRenewal(ctx, uid, rn, h))
}

func (s *Server) handleDeleteHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.DeleteHistory(ctx, actorID(ctx), idParam(r, "id")); err != nil {
		s.fail(w, err, "history")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit := queryInt(r, "limit")
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	list, err := s.store.ListHistory(ctx, actorID(ctx), int64(queryInt(r, "renewal_id")), limit)
	if err != nil {
		s.fail(w, err, "history")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleChecked marks found renewals as looked at ({"ids": [...]} or {"all": true}).
func (s *Server) handleChecked(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		IDs []int64 `json:"ids"`
		All bool    `json:"all"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ids := in.IDs
	if in.All {
		ids = nil
	} else if ids == nil {
		ids = []int64{}
	}
	n, err := s.store.SetChecked(ctx, actorID(ctx), ids)
	if err != nil {
		s.fail(w, err, "checked")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"checked": n})
}

// handleTask adds "Renew …" to the person's Pocket Tasks, due by the last day to act.
func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	rn, err := s.store.GetRenewal(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "renewal")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := s.clients(cctx, uid).conn(cctx, appTasks, scopeTasksWrite)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	due := firstNonEmpty(rn.ActBy, rn.ExpiresOn)
	if due != "" && due < store.Today() {
		due = store.Today()
	}
	title := "Renew " + lowerFirst(rn.Title)
	if rn.Holder != "" && !strings.Contains(strings.ToLower(rn.Title), strings.ToLower(rn.Holder)) {
		title += " (" + rn.Holder + ")"
	}
	notes := "Runs out on " + firstNonEmpty(rn.ExpiresOn, "an unknown date") + "."
	if rn.RenewURL != "" {
		notes += "\nRenew at " + rn.RenewURL
	}
	t, err := conn.client.CreateTask(cctx, title, notes, due, s.cfg.BaseURL+"/r/"+itoa(rn.ID))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Pocket Tasks: "+err.Error())
		return
	}
	_ = s.store.SetTask(ctx, uid, rn.ID, &t.ID)
	writeJSON(w, http.StatusOK, map[string]any{"task_id": t.ID, "url": strings.TrimRight(conn.public, "/") + "/tasks/" + itoa(t.ID)})
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	w, rest, _ := strings.Cut(s, " ")
	if strings.ToUpper(w) == w && len(w) > 1 { // an abbreviation: TLS, ID
		return s
	}
	out := strings.ToLower(s[:1]) + s[1:]
	if rest == "" {
		return out
	}
	return out
}

func (s *Server) handleHolders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h, err := s.store.Holders(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "holders")
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleKinds(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"kinds": store.Kinds, "groups": store.Groups})
}

func (s *Server) handleDismissed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := s.store.ListDismissed(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "dismissed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleUndismiss(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Ref string `json:"source_ref"`
		All bool   `json:"all"`
	}
	if err := decode(r, &in); err != nil || (in.Ref == "" && !in.All) {
		writeErr(w, http.StatusBadRequest, "which one?")
		return
	}
	if err := s.store.Undismiss(ctx, actorID(ctx), in.Ref); err != nil {
		s.fail(w, err, "dismissed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
