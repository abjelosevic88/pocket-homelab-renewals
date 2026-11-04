package server

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-renewals/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/store"
)

// ---- settings ----

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

// publicSettings is what one person sees: their settings with the secrets
// masked and, per Pocket app, the mode in effect (<app>_mode), whether it
// works and how (<app>_via: core | direct | "") and why not (<app>_detail).
func (s *Server) publicSettings(ctx context.Context, u *store.User, st store.Settings) map[string]any {
	out := map[string]any{
		"core_configured": s.core.Configured(), "core_registered": s.coreStatus()["registered"],
		"title": st.Title, "date_format": st.DateFormat, "time_format": st.TimeFormat, "week_start": st.WeekStart,
		"currency": st.Currency, "soon_days": st.SoonDays,
		"notify_reminders": st.NotifyReminders, "notify_expired": st.NotifyExpired, "notify_found": st.NotifyFound, "notify_hour": st.NotifyHour,
		"notify_url": st.NotifyURL, "notify_format": st.NotifyFormat, "notify_configured": st.NotifyURL != "",
		"auto_documents": st.AutoDocuments, "scan_weekly": st.ScanWeekly, "scan_sources": st.ScanSources, "documents_ai": st.DocumentsAI,
		"email_days": st.EmailDays, "travel_months": st.TravelMonths,
		"money_book": st.MoneyBook, "money_account_id": st.MoneyAccountID, "money_category_id": st.MoneyCategoryID,
		"calendar_sync": st.CalendarSync, "calendar_id": st.CalendarID,
		"ai_provider": st.AIProvider, "ai_pocket_model": st.AIPocketModel, "openai_api_key": mask(st.OpenAIKey), "openai_model": st.OpenAIModel, "openai_base_url": st.OpenAIBaseURL,
		"setup_complete": st.SetupComplete, "base_url": s.cfg.BaseURL,
	}
	for _, a := range pocketApps {
		mode, via, detail := s.connStatus(ctx, u, st, a)
		out[a.slug+"_mode"], out[a.slug+"_via"], out[a.slug+"_detail"] = mode, via, detail
		out[a.slug+"_configured"] = via != ""
		_, url, token, public := connSettings(st, a)
		out[a.slug+"_url"], out[a.slug+"_token"], out[a.slug+"_public_url"] = url, mask(token), public
		if public == "" && via != "" {
			public = s.resolvePublic(ctx, a.slug)
		}
		out[a.slug+"_open_url"] = public
	}
	return out
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), userFrom(r.Context()), st))
}

// handlePutSettings saves the caller's personal settings. The box-wide title
// is only taken from an admin.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	isAdmin := me.Role == "admin"
	cur, err := s.store.GetSettings(r.Context(), me.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	raw := map[string]any{}
	var in store.Settings
	keys, err := decodeKeys(r, &raw)
	if err != nil || remarshal(raw, &in) != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	bad := func(msg string) { writeErr(w, http.StatusBadRequest, msg) }
	if keys["title"] && isAdmin {
		cur.Title = strings.TrimSpace(in.Title)
		if cur.Title == "" {
			cur.Title = store.DefaultTitle
		}
	}
	if keys["date_format"] {
		cur.DateFormat = in.DateFormat
	}
	if keys["time_format"] {
		cur.TimeFormat = in.TimeFormat
	}
	if keys["week_start"] && (in.WeekStart == 1 || in.WeekStart == 7 || in.WeekStart == 6) {
		cur.WeekStart = in.WeekStart
	}
	if keys["currency"] {
		if c := cleanCurrency(in.Currency); c != "" {
			cur.Currency = c
		} else {
			bad("currency is a three-letter code like EUR")
			return
		}
	}
	if keys["soon_days"] {
		if in.SoonDays < 7 || in.SoonDays > 730 {
			bad("“coming up” is 7 to 730 days")
			return
		}
		cur.SoonDays = in.SoonDays
	}
	if keys["email_days"] {
		if in.EmailDays < 7 || in.EmailDays > 3650 {
			bad("the email scan looks back 7 to 3650 days")
			return
		}
		cur.EmailDays = in.EmailDays
	}
	if keys["travel_months"] {
		if in.TravelMonths < 0 || in.TravelMonths > 24 {
			bad("0 to 24 months")
			return
		}
		cur.TravelMonths = in.TravelMonths
	}
	if keys["scan_sources"] {
		for k, v := range in.ScanSources {
			if _, ok := sourceByName(k); ok {
				cur.ScanSources[k] = v
			}
		}
	}
	if keys["notify_hour"] {
		if in.NotifyHour < 0 || in.NotifyHour > 23 {
			bad("the hour is 0 to 23")
			return
		}
		cur.NotifyHour = in.NotifyHour
	}
	secret := func(k string, dst *string, v string) {
		if keys[k] && !strings.Contains(v, "••") {
			*dst = strings.TrimSpace(v)
		}
	}
	url := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimRight(strings.TrimSpace(v), "/")
		}
	}
	text := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimSpace(v)
		}
	}
	boolIn := func(k string, dst *bool, v bool) {
		if keys[k] {
			*dst = v
		}
	}
	idIn := func(k string, dst *int64, v int64) {
		if keys[k] && v >= 0 {
			*dst = v
		}
	}
	boolIn("notify_reminders", &cur.NotifyReminders, in.NotifyReminders)
	boolIn("notify_expired", &cur.NotifyExpired, in.NotifyExpired)
	boolIn("notify_found", &cur.NotifyFound, in.NotifyFound)
	boolIn("auto_documents", &cur.AutoDocuments, in.AutoDocuments)
	boolIn("scan_weekly", &cur.ScanWeekly, in.ScanWeekly)
	boolIn("documents_ai", &cur.DocumentsAI, in.DocumentsAI)
	text("notify_url", &cur.NotifyURL, in.NotifyURL)
	if keys["notify_format"] && in.NotifyFormat != "" {
		cur.NotifyFormat = in.NotifyFormat
	}
	boolIn("money_book", &cur.MoneyBook, in.MoneyBook)
	idIn("money_account_id", &cur.MoneyAccountID, in.MoneyAccountID)
	idIn("money_category_id", &cur.MoneyCategoryID, in.MoneyCategoryID)
	calWas := cur.CalendarSync
	boolIn("calendar_sync", &cur.CalendarSync, in.CalendarSync)
	idIn("calendar_id", &cur.CalendarID, in.CalendarID)
	// each Pocket app: mode, URL, token, public URL
	inMap := map[string]string{}
	for k, v := range raw {
		if sv, ok := v.(string); ok {
			inMap[k] = sv
		}
	}
	for _, a := range pocketApps {
		c := cur.Conns[a.slug]
		if v := inMap[a.slug+"_mode"]; keys[a.slug+"_mode"] && (v == modeCore || v == modeDirect || v == modeOff) {
			c.Mode = v
		}
		url(a.slug+"_url", &c.URL, inMap[a.slug+"_url"])
		secret(a.slug+"_token", &c.Token, inMap[a.slug+"_token"])
		url(a.slug+"_public_url", &c.PublicURL, inMap[a.slug+"_public_url"])
		if c != (store.Conn{}) {
			cur.Conns[a.slug] = c
		}
	}
	if keys["ai_provider"] {
		if !validAIProvider(in.AIProvider) {
			bad("AI is automatic, Pocket AI or OpenAI")
			return
		}
		cur.AIProvider = in.AIProvider
	}
	if keys["ai_pocket_model"] {
		if !validPocketModel(in.AIPocketModel) {
			bad("unknown model")
			return
		}
		cur.AIPocketModel = in.AIPocketModel
	}
	secret("openai_api_key", &cur.OpenAIKey, in.OpenAIKey)
	text("openai_model", &cur.OpenAIModel, in.OpenAIModel)
	url("openai_base_url", &cur.OpenAIBaseURL, in.OpenAIBaseURL)
	if err := s.store.SaveUserSettings(r.Context(), me.ID, cur); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if isAdmin && keys["title"] {
		if err := s.store.SaveBoxSettings(r.Context(), cur); err != nil {
			s.fail(w, err, "save settings")
			return
		}
	}
	for k := range keys {
		if modeKey(k) {
			s.reportModesSoon()
			break
		}
	}
	if cur.CalendarSync != calWas || keys["calendar_id"] || keys["calendar_mode"] {
		s.syncCalendarSoon(me.ID)
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), me, cur))
}

// handleIntegrationTest checks a connection with the caller's own saved
// settings (or the URL/key typed into the form).
func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Which  string `json:"which"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		Format string `json:"format"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	uid := actorID(r.Context())
	st, _ := s.store.GetSettings(r.Context(), uid)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	keyOr := func(saved string) string {
		if strings.Contains(in.Key, "••") || in.Key == "" {
			return saved
		}
		return in.Key
	}
	urlOr := func(saved string) string {
		if strings.TrimSpace(in.URL) == "" {
			return saved
		}
		return strings.TrimRight(strings.TrimSpace(in.URL), "/")
	}
	var msg string
	var err error
	if a, ok := appBySlug(in.Which); ok {
		mode, savedURL, savedTok, _ := connSettings(st, a)
		typed := in.Key != "" && !strings.Contains(in.Key, "••")
		var conn *siblingConn
		if tok := keyOr(savedTok); tok != "" && (typed || s.connMode(mode, savedTok) == modeDirect) {
			base := urlOr(savedURL)
			if base == "" {
				base = s.resolveSibling(ctx, a.slug)
			}
			conn = &siblingConn{client: pocket.New(base, tok, 15*time.Second), via: modeDirect}
		} else {
			conn, err = s.clients(ctx, uid).conn(ctx, a, a.scopes...)
		}
		if err == nil {
			how := "direct connection"
			if conn.via == modeCore {
				how = "through Pocket Core"
			}
			var v string
			if v, err = conn.client.Health(ctx); err == nil {
				msg = fmt.Sprintf("%s %s, %s", a.name, v, how)
				switch a.slug {
				case "money":
					var accts []pocket.Account
					if accts, err = conn.client.Accounts(ctx); err == nil {
						msg += fmt.Sprintf(", %d accounts you can book on", len(accts))
					}
				case "calendar":
					var cals []pocket.Calendar
					if cals, err = conn.client.Calendars(ctx); err == nil {
						msg += fmt.Sprintf(", %d calendars you can write to", len(cals))
					}
				case "documents":
					var docs []pocket.Document
					if docs, err = conn.client.SearchDocuments(ctx, "", 1); err == nil && len(docs) == 0 {
						msg += ", no documents yet"
					}
				case "home":
					var things []pocket.Thing
					if things, err = conn.client.Things(ctx); err == nil {
						msg += fmt.Sprintf(", %d appliances", len(things))
					}
				case "garage":
					var vs []pocket.Vehicle
					if vs, err = conn.client.Vehicles(ctx); err == nil {
						msg += fmt.Sprintf(", %d vehicles", len(vs))
						if len(vs) > 0 {
							if _, rerr := conn.client.VehicleReminders(ctx, vs[0].ID); rerr != nil {
								msg += "; its reminders are not open to app links yet (" + rerr.Error() + ")"
							}
						}
					}
				case "inventory":
					_, err = conn.client.SearchItems(ctx, "", 1)
				case "monitoring":
					var srv []pocket.Server
					if srv, err = conn.client.Servers(ctx); err == nil {
						msg += fmt.Sprintf(", %d servers", len(srv))
					}
				case "travel":
					var trips []pocket.Trip
					if trips, err = conn.client.Trips(ctx); err == nil {
						msg += fmt.Sprintf(", %d trips", len(trips))
					}
				case "email":
					_, err = conn.client.SearchMessages(ctx, "newer_than:7d", 1)
				case "contacts":
					_, err = conn.client.SearchContacts(ctx, "a", 1)
				}
			}
		}
	} else if in.Which == "openai" {
		c := ai.New(urlOr(st.OpenAIBaseURL), keyOr(st.OpenAIKey), st.OpenAIModel, 20*time.Second)
		var ids []string
		if ids, err = c.Models(ctx); err == nil {
			msg = fmt.Sprintf("The key works (%d models)", len(ids))
		}
	} else if in.Which == "notify" {
		f := in.Format
		if f == "" {
			f = st.NotifyFormat
		}
		n := notify.New(urlOr(st.NotifyURL), f)
		err = n.Send(ctx, notify.Message{Title: "Pocket Renewals", Body: "Notifications work. Reminders before something runs out will arrive here, once a day.", Priority: "info", Tags: []string{"hourglass"}})
		msg = "Test message sent via " + n.Format
	} else {
		writeErr(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

// handleCheckNow runs the daily check for the caller now and says what it sent.
func (s *Server) handleCheckNow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	body, sent := s.dailyCheck(ctx, uid, st, time.Now())
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent, "body": body})
}

// ---- system / backup / export ----

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "data_dir": s.cfg.DataDir, "db_path": s.cfg.DBPath, "db_bytes": size,
		"uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": s.store.CountsFor(r.Context(), actorID(r.Context())),
	})
}

// handleBackup downloads the whole database, an admin-only box operation.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "backups")
	name := fmt.Sprintf("pocket-renewals-%s.db", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, dst)
}

// exportDoc is the JSON export of one person's data.
type exportDoc struct {
	App      string          `json:"app"`
	Version  string          `json:"version"`
	Exported string          `json:"exported_at"`
	Renewals []store.Renewal `json:"renewals"`
	History  []store.History `json:"history"`
}

func (s *Server) export(ctx context.Context, uid int64) exportDoc {
	doc := exportDoc{App: "pocket-renewals", Version: Version, Exported: store.Now()}
	doc.Renewals, _ = s.store.ListRenewals(ctx, uid, store.RenewalFilter{Archived: true})
	doc.History, _ = s.store.ListHistory(ctx, uid, 0, 0)
	return doc
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-renewals-export.json"`)
	writeJSON(w, http.StatusOK, s.export(r.Context(), actorID(r.Context())))
}

// handleExportCSV writes the renewals as CSV rows (a spreadsheet of what runs out).
func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := s.store.ListRenewals(ctx, actorID(ctx), store.RenewalFilter{Archived: true})
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-renewals.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"title", "kind", "holder", "provider", "number", "issued_on", "expires_on", "status", "days_left", "renews_every", "auto_renews", "cost", "currency", "renew_url", "notes", "source"})
	for _, rn := range list {
		cost, term := "", ""
		if rn.Cost != nil {
			cost = strconv.FormatFloat(float64(*rn.Cost)/100, 'f', 2, 64)
		}
		if rn.RenewN > 0 {
			term = fmt.Sprintf("%d %s", rn.RenewN, rn.RenewUnit)
		}
		_ = cw.Write([]string{rn.Title, rn.Kind, rn.Holder, rn.Provider, rn.Number, rn.IssuedOn, rn.ExpiresOn, rn.Status, strconv.Itoa(rn.DaysLeft), term,
			strconv.FormatBool(rn.AutoRenews), cost, rn.Currency, rn.RenewURL, rn.Notes, rn.Source})
	}
	cw.Flush()
}

// handleImportJSON merges an export into the caller's own data. Rows keep
// their identity (the export's ids, remembered per person), so importing
// the same file twice adds nothing. Calendar, task and Money links are not
// carried over: they belong to the account the file came from.
func (s *Server) handleImportJSON(w http.ResponseWriter, r *http.Request) {
	var doc exportDoc
	if err := decode(r, &doc); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if doc.App != "" && doc.App != "pocket-renewals" {
		writeErr(w, http.StatusBadRequest, "this is not a Pocket Renewals export")
		return
	}
	ctx := r.Context()
	uid := actorID(ctx)
	counts := map[string]int{"renewals": 0, "history": 0}
	idOf := func(kind string, old int64) (int64, bool) {
		v, _, ok := s.store.CacheGet(ctx, fmt.Sprintf("import:%d:%s:%d", uid, kind, old))
		if !ok {
			return 0, false
		}
		n, _ := strconv.ParseInt(v, 10, 64)
		if n <= 0 {
			return 0, false
		}
		if kind == "renewal" {
			if _, err := s.store.GetRenewal(ctx, uid, n); err != nil {
				return 0, false
			}
		}
		return n, true
	}
	remember := func(kind string, old, id int64) {
		_ = s.store.CachePut(ctx, fmt.Sprintf("import:%d:%s:%d", uid, kind, old), strconv.FormatInt(id, 10))
	}
	for _, rn := range doc.Renewals {
		if _, ok := idOf("renewal", rn.ID); ok {
			continue
		}
		if rn.SourceRef != "" {
			if have, err := s.store.RenewalBySource(ctx, uid, rn.SourceRef); err == nil {
				remember("renewal", rn.ID, have.ID)
				continue
			}
		}
		old := rn.ID
		rn.CalEventID, rn.TaskID, rn.Source = nil, nil, firstNonEmpty(rn.Source, "import")
		if err := s.store.CreateRenewal(ctx, uid, &rn); err == nil {
			remember("renewal", old, rn.ID)
			counts["renewals"]++
		}
	}
	for _, h := range doc.History {
		if _, ok := idOf("history", h.ID); ok {
			continue
		}
		rid, ok := idOf("renewal", h.RenewalID)
		if !ok {
			continue
		}
		old := h.ID
		h.RenewalID, h.MoneyTxID = rid, nil
		if err := s.store.InsertHistory(ctx, uid, &h); err == nil {
			remember("history", old, h.ID)
			counts["history"]++
		}
	}
	writeJSON(w, http.StatusOK, counts)
}
