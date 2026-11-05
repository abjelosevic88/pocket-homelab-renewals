package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-renewals/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/store"
)

// ---- finding renewals in the other Pocket apps ----
//
// A scan looks through one app for dates that run out and adds each as a
// renewal marked "check this" (needs_check), keyed by what it came from
// (source_ref), so a second scan adds nothing twice and only moves a date
// on when the app has a later one. What the person deleted is remembered
// (dismissed) and never comes back. Pocket Documents is also followed live:
// a new or changed document is read as soon as it is filed (events.go).

// scanSource is one app a scan can look through.
type scanSource struct {
	Name   string    `json:"name"`
	Label  string    `json:"label"`
	What   string    `json:"what"`
	App    pocketApp `json:"-"`
	Scopes []string  `json:"-"`
	AI     string    `json:"ai"` // "" | optional | required
	run    func(s *Server, ctx context.Context, sc *scanRun) error
}

var scanSources = []scanSource{
	{"documents", "Pocket Documents", "Passports, ID cards, policies, warranty cards and contracts: the “Valid until” and other date fields, or the text read by AI.", appDocuments, []string{scopeDocumentsRead}, "optional", (*Server).scanDocuments},
	{"home", "Pocket Home", "When the warranties of your appliances end.", appHome, []string{scopeHomeRead}, "", (*Server).scanHome},
	{"garage", "Pocket Garage", "Registration, inspection, insurance and vignette reminders, and vehicle papers with an end date.", appGarage, []string{scopeVehiclesRead}, "", (*Server).scanGarage},
	{"inventory", "Pocket Inventory", "Items with an expiry date that are not food: the first-aid kit, the fire extinguisher, medicine.", appInventory, []string{scopeItemsRead}, "", (*Server).scanInventory},
	{"monitoring", "Pocket Monitoring", "The TLS certificates of the websites you monitor.", appMonitoring, []string{scopeMonitorRead}, "", (*Server).scanMonitoring},
	{"money", "Pocket Money", "Yearly and other long subscriptions, and subscriptions with an end date.", appMoney, []string{scopeTxRead}, "", (*Server).scanMoney},
	{"wealth", "Pocket Wealth", "Term deposits that mature, fixed mortgage rates that end, money lent that is due back.", appWealth, []string{scopeWealthRead}, "", (*Server).scanWealth},
	{"pets", "Pocket Pets", "Your pets’ insurance and their vaccines’ “valid until”, the latest of each.", appPets, []string{scopePetsRead}, "", (*Server).scanPets},
	{"wallet", "Pocket Wallet", "Your passport, ID card, driving licence and insurance cards, memberships and gift cards with an end date.", appWallet, []string{scopeWalletRead}, "", (*Server).scanWallet},
	{"email", "Pocket Email", "Renewal notices in your email (“your domain expires”, “your policy renews”), read by AI.", appEmail, []string{scopeMailRead}, "required", (*Server).scanEmail},
}

func sourceByName(name string) (scanSource, bool) {
	for _, src := range scanSources {
		if src.Name == name {
			return src, true
		}
	}
	return scanSource{}, false
}

// scanRun is one scan in progress.
type scanRun struct {
	uid     int64
	st      store.Settings
	conn    *siblingConn
	ai      *ai.Client
	aiLeft  int // AI reads left in this scan
	rec     *store.Scan
	added   []*store.Renewal
	oldest  string // dates before this are not worth adding
	onlyDoc int64  // documents: just this one (a live event)
}

// add stores one found date.
func (s *Server) add(ctx context.Context, sc *scanRun, f store.Found) {
	if f.ExpiresOn == "" || f.ExpiresOn < sc.oldest || strings.TrimSpace(f.Title) == "" {
		return
	}
	if !store.ValidKind(f.Kind) {
		f.Kind = "other"
	}
	if f.Currency == "" {
		f.Currency = sc.st.Currency
	}
	sc.rec.Found++
	res, r, err := s.store.UpsertFound(ctx, sc.uid, f)
	if err != nil {
		s.log.Warn("scan: saving a found renewal failed", "ref", f.Ref, "err", err)
		return
	}
	switch res {
	case store.UpsertAdded:
		sc.rec.Added++
		sc.added = append(sc.added, r)
	case store.UpsertUpdated:
		sc.rec.Updated++
	}
}

func (s *Server) scanKey(uid int64, src string) string { return fmt.Sprintf("%d:%s", uid, src) }

func (s *Server) scanning(uid int64, src string) bool {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	return s.scans[s.scanKey(uid, src)]
}

// runScan scans one source for one person; false when one is already running.
func (s *Server) runScan(ctx context.Context, uid int64, src scanSource, trigger string, onlyDoc int64) (*store.Scan, error) {
	key := s.scanKey(uid, src.Name)
	if onlyDoc == 0 {
		s.scanMu.Lock()
		if s.scans[key] {
			s.scanMu.Unlock()
			return nil, errBusy
		}
		s.scans[key] = true
		s.scanMu.Unlock()
		defer func() { s.scanMu.Lock(); delete(s.scans, key); s.scanMu.Unlock() }()
	}
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil {
		return nil, err
	}
	rec, err := s.store.StartScan(ctx, uid, src.Name, trigger)
	if err != nil {
		return nil, err
	}
	sc := &scanRun{uid: uid, st: st, rec: rec, aiLeft: 40, oldest: store.AddDays(store.Today(), -180), onlyDoc: onlyDoc}
	if trigger == "event" {
		sc.aiLeft = 2
	}
	finish := func(err error) (*store.Scan, error) {
		if err != nil {
			rec.Error = err.Error()
		}
		if trigger == "event" && rec.Added == 0 && rec.Updated == 0 && rec.Error == "" {
			_ = s.store.DeleteScan(context.WithoutCancel(ctx), uid, rec.ID)
		} else {
			_ = s.store.FinishScan(context.WithoutCancel(ctx), uid, rec)
		}
		s.announceFound(context.WithoutCancel(ctx), uid, src, sc.added)
		return rec, err
	}
	conn, err := s.clients(ctx, uid).conn(ctx, src.App, src.Scopes...)
	if err != nil {
		return finish(err)
	}
	sc.conn = conn
	if src.AI != "" {
		if cl, _, why := s.aiFor(ctx, uid, st); cl != nil {
			sc.ai = cl
		} else if src.AI == "required" {
			return finish(errors.New("needs AI to read it: " + firstNonEmpty(why, "set it up in Settings → AI")))
		}
	}
	if src.Name == "documents" && !st.DocumentsAI {
		sc.ai = nil
	}
	return finish(src.run(s, ctx, sc))
}

var errBusy = errors.New("a scan of this app is already running")

// announceFound publishes renewals.found and syncs the calendar for what was added.
func (s *Server) announceFound(ctx context.Context, uid int64, src scanSource, added []*store.Renewal) {
	if len(added) == 0 {
		return
	}
	for _, r := range added {
		p := renewalPayload(r)
		p["found_in"] = src.Name
		s.publish(ctx, uid, "renewals.found", "renewal:"+itoa(r.ID), p)
	}
	s.syncCalendarSoon(uid)
}

// ---- Pocket Documents ----

func (s *Server) scanDocuments(ctx context.Context, sc *scanRun) error {
	fields, err := sc.conn.client.CustomFields(ctx)
	if err != nil {
		return fmt.Errorf("Pocket Documents: %w", err)
	}
	dateFields := map[int64]string{}
	for _, f := range fields {
		if f.DataType == "date" && isExpiryField(f.Name) {
			dateFields[f.ID] = f.Name
		}
	}
	base := strings.TrimRight(sc.conn.public, "/")
	if sc.onlyDoc > 0 {
		d, err := sc.conn.client.GetDocument(ctx, sc.onlyDoc)
		if err != nil {
			return fmt.Errorf("Pocket Documents: %w", err)
		}
		sc.rec.Looked++
		s.readDocument(ctx, sc, d, dateFields, base)
		return nil
	}
	const page = 100
	for off := 0; off < 3000; off += page {
		docs, err := sc.conn.client.ListDocuments(ctx, off, page)
		if err != nil {
			return fmt.Errorf("Pocket Documents: %w", err)
		}
		for i := range docs {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			sc.rec.Looked++
			s.readDocument(ctx, sc, &docs[i], dateFields, base)
		}
		if len(docs) < page {
			break
		}
	}
	return nil
}

// readDocument finds a document's end date: a date field first, else (when
// the document looks like something that runs out) its text read by AI.
func (s *Server) readDocument(ctx context.Context, sc *scanRun, d *pocket.Document, dateFields map[int64]string, base string) {
	if d.Status != "" && d.Status != "done" {
		return
	}
	kind := guessKind(d.DocumentType, d.Title, d.OriginalName)
	f := store.Found{Ref: "documents:" + itoa(d.ID), Source: "documents", Title: d.Title, Kind: firstNonEmpty(kind, "other"), Provider: d.Correspondent,
		DocumentID: &d.ID, DocTitle: d.Title}
	if base != "" {
		f.SourceURL = base + "/documents/" + itoa(d.ID)
	}
	for _, v := range d.CustomFields {
		name, ok := dateFields[v.Field]
		if !ok || !validDate(v.Value) || v.Value == "" {
			continue
		}
		if f.ExpiresOn == "" || v.Value < f.ExpiresOn {
			f.ExpiresOn, f.FoundBy = v.Value, "the “"+name+"” field in Pocket Documents"
		}
	}
	if f.ExpiresOn != "" {
		if kind == "" {
			f.Kind = "other"
		}
		s.add(ctx, sc, f)
		return
	}
	if sc.ai == nil || kind == "" || sc.aiLeft <= 0 {
		return
	}
	if s.store.IsDismissed(ctx, sc.uid, f.Ref) {
		return
	}
	if have, err := s.store.RenewalBySource(ctx, sc.uid, f.Ref); err == nil && have != nil {
		return // already read; a date field later still moves it
	}
	cacheKey := fmt.Sprintf("airead:%d:doc:%d:%s", sc.uid, d.ID, d.ModifiedAt)
	var got aiFound
	if v, _, ok := s.store.CacheGet(ctx, cacheKey); ok {
		_ = json.Unmarshal([]byte(v), &got)
	} else {
		sc.aiLeft--
		text, err := sc.conn.client.DocumentText(ctx, d.ID)
		if err != nil || strings.TrimSpace(text) == "" {
			return
		}
		got, err = s.aiRead(ctx, sc.ai, fmt.Sprintf("A document in someone's archive.\nTitle: %s\nType: %s\nFrom: %s\nDated: %s\n\nIts text:\n%s",
			d.Title, d.DocumentType, d.Correspondent, d.Created, clip(text, 7000)))
		if err != nil {
			s.log.Debug("scan: AI could not read a document", "doc", d.ID, "err", err)
			return
		}
		b, _ := json.Marshal(got)
		_ = s.store.CachePut(ctx, cacheKey, string(b))
	}
	if !got.RunsOut || !validDate(got.ExpiresOn) || got.ExpiresOn == "" {
		return
	}
	got.into(&f)
	f.FoundBy, f.Unsure = "AI read the document's text", true
	s.add(ctx, sc, f)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n[…]"
}

// aiFound is what the AI read from a document or an email.
type aiFound struct {
	RunsOut    bool   `json:"runs_out"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Holder     string `json:"holder"`
	Provider   string `json:"provider"`
	Number     string `json:"number"`
	IssuedOn   string `json:"issued_on"`
	ExpiresOn  string `json:"expires_on"`
	AutoRenews bool   `json:"auto_renews"`
}

func (a aiFound) into(f *store.Found) {
	if store.ValidKind(a.Kind) {
		f.Kind = a.Kind
	}
	if t := strings.TrimSpace(a.Title); t != "" && len(t) <= 120 {
		f.Title = t
	}
	f.Holder, f.Number, f.ExpiresOn, f.AutoRenews = clip1(a.Holder, 80), clip1(a.Number, 80), a.ExpiresOn, a.AutoRenews
	if p := clip1(a.Provider, 120); p != "" {
		f.Provider = p
	}
	if validDate(a.IssuedOn) {
		f.IssuedOn = a.IssuedOn
	}
}

func clip1(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return ""
	}
	return s
}

const aiReadVoice = `You read documents and emails for a person who keeps track of what runs out: passports, ID cards, driving licences, visas, insurance policies, vehicle registration and inspection, warranties, domain names, certificates, subscriptions, memberships, contracts, prescriptions, licences. You extract facts that are written in the text and never guess a date that is not there. Dates are YYYY-MM-DD.`

func (s *Server) aiRead(ctx context.Context, cl *ai.Client, what string) (aiFound, error) {
	kinds := make([]string, 0, len(store.Kinds))
	for _, k := range store.Kinds {
		kinds = append(kinds, k.Value)
	}
	prompt := what + `

Does this show something of the person's that runs out or has to be renewed on a date written here (an expiry, "valid until", the end of a policy, contract, warranty or subscription)? An invoice's payment due date does not count, nor does a past event.
Answer with JSON only: {"runs_out": true|false, "kind": "` + strings.Join(kinds, "|") + `", "title": "a short plain name, e.g. Passport, Car insurance, Laptop warranty, Domain example.com", "holder": "whose it is (a first name, or the car), or empty", "provider": "who issued it or sells it, or empty", "number": "document or policy number, or empty", "issued_on": "YYYY-MM-DD or empty", "expires_on": "YYYY-MM-DD", "auto_renews": true|false}`
	var out aiFound
	err := cl.JSON(ctx, aiReadVoice, prompt, &out)
	return out, err
}

// ---- Pocket Home ----

func (s *Server) scanHome(ctx context.Context, sc *scanRun) error {
	things, err := sc.conn.client.Things(ctx)
	if err != nil {
		return fmt.Errorf("Pocket Home: %w", err)
	}
	base := strings.TrimRight(sc.conn.public, "/")
	for _, t := range things {
		sc.rec.Looked++
		if t.Archived || t.WarrantyUntil == "" {
			continue
		}
		f := store.Found{Ref: "home:thing:" + itoa(t.ID), Source: "home", Title: "Warranty: " + t.Name, Kind: "warranty", Holder: t.HomeName,
			Provider: firstNonEmpty(t.Vendor, t.Brand), Number: t.Serial, IssuedOn: t.PurchasedOn, ExpiresOn: t.WarrantyUntil, Currency: t.Currency,
			FoundBy: "the appliance's warranty in Pocket Home", RenewUnit: "year"}
		if base != "" {
			f.SourceURL = base + "/things/" + itoa(t.ID)
		}
		s.add(ctx, sc, f)
	}
	return nil
}

// ---- Pocket Garage ----

func (s *Server) scanGarage(ctx context.Context, sc *scanRun) error {
	vs, err := sc.conn.client.Vehicles(ctx)
	if err != nil {
		return fmt.Errorf("Pocket Garage: %w", err)
	}
	base := strings.TrimRight(sc.conn.public, "/")
	var firstErr error
	for _, v := range vs {
		if v.Status != "" && v.Status != "active" {
			continue
		}
		name := firstNonEmpty(v.Name, strings.TrimSpace(v.Make+" "+v.Model), "Vehicle")
		rems, err := sc.conn.client.VehicleReminders(ctx, v.ID)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("Pocket Garage reminders: %w", err)
		}
		for _, r := range rems {
			sc.rec.Looked++
			if r.DueDate == "" || r.Status == "done" || !isVehicleRenewal(r.Title, r.Kind, r.Category) {
				continue
			}
			kind := "vehicle"
			if strings.Contains(strings.ToLower(r.Title+r.Kind+r.Category), "insur") || strings.Contains(strings.ToLower(r.Title), "osiguran") {
				kind = "insurance"
			}
			f := store.Found{Ref: "garage:reminder:" + itoa(r.ID), Source: "garage", Title: r.Title, Kind: kind, Holder: name, Number: v.Plate,
				ExpiresOn: r.DueDate, FoundBy: "a reminder in Pocket Garage"}
			if r.IntervalDays > 0 {
				f.RenewN, f.RenewUnit = r.IntervalDays, "day"
				if r.IntervalDays%365 == 0 {
					f.RenewN, f.RenewUnit = r.IntervalDays/365, "year"
				}
			}
			if base != "" {
				f.SourceURL = base + "/vehicles/" + itoa(v.ID) + "/reminders"
			}
			s.add(ctx, sc, f)
		}
		docs, err := sc.conn.client.VehicleDocuments(ctx, v.ID)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("Pocket Garage papers: %w", err)
		}
		for _, d := range docs {
			sc.rec.Looked++
			if d.ExpiresOn == "" {
				continue
			}
			kind := "vehicle"
			if d.Kind == "insurance" {
				kind = "insurance"
			} else if d.Kind == "manual" || d.Kind == "invoice" || d.Kind == "receipt" {
				kind = "warranty"
			}
			f := store.Found{Ref: "garage:document:" + itoa(d.ID), Source: "garage", Title: d.Name, Kind: kind, Holder: name, Number: v.Plate,
				ExpiresOn: d.ExpiresOn, FoundBy: "a paper kept with the vehicle in Pocket Garage"}
			if base != "" {
				f.SourceURL = base + "/vehicles/" + itoa(v.ID) + "/documents"
			}
			s.add(ctx, sc, f)
		}
	}
	if sc.rec.Looked == 0 && firstErr != nil {
		return firstErr
	}
	return nil
}

// ---- Pocket Inventory ----

func (s *Server) scanInventory(ctx context.Context, sc *scanRun) error {
	items, err := sc.conn.client.ExpiringItems(ctx, 3650)
	if err != nil {
		return fmt.Errorf("Pocket Inventory: %w", err)
	}
	base := strings.TrimRight(sc.conn.public, "/")
	for _, it := range items {
		sc.rec.Looked++
		if it.Archived || it.ExpiryDate == "" || isFood(it.Category, it.Location, it.Name) {
			continue
		}
		kind := "other"
		if strings.Contains(strings.ToLower(it.Category+" "+it.Name), "medic") || strings.Contains(strings.ToLower(it.Category+" "+it.Name), "first aid") || strings.Contains(strings.ToLower(it.Category), "lijek") {
			kind = "health"
		}
		f := store.Found{Ref: "inventory:item:" + itoa(it.ID), Source: "inventory", Title: it.Name, Kind: kind, Holder: it.Location,
			Provider: it.Brand, Number: it.Serial, ExpiresOn: it.ExpiryDate, FoundBy: "the item's expiry date in Pocket Inventory", RenewUnit: "year"}
		if base != "" {
			f.SourceURL = base + "/items/" + itoa(it.ID)
		}
		s.add(ctx, sc, f)
	}
	return nil
}

// ---- Pocket Monitoring ----

func (s *Server) scanMonitoring(ctx context.Context, sc *scanRun) error {
	servers, err := sc.conn.client.Servers(ctx)
	if err != nil {
		return fmt.Errorf("Pocket Monitoring: %w", err)
	}
	base := strings.TrimRight(sc.conn.public, "/")
	seen := map[string]bool{}
	var firstErr error
	for _, srv := range servers {
		checks, err := sc.conn.client.ServerChecks(ctx, srv.ID)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("Pocket Monitoring checks: %w", err)
			}
			continue
		}
		for _, c := range checks {
			sc.rec.Looked++
			if c.CertExpires <= 0 || c.IgnoreTLS || !strings.HasPrefix(strings.ToLower(c.Target), "https://") {
				continue
			}
			host := hostOf(c.Target)
			if host == "" || seen[host] {
				continue
			}
			seen[host] = true
			f := store.Found{Ref: "monitoring:cert:" + host, Source: "monitoring", Title: "TLS certificate: " + host, Kind: "certificate", Holder: srv.Name,
				ExpiresOn: time.Unix(c.CertExpires, 0).Format("2006-01-02"), AutoRenews: false, RenewN: 90, RenewUnit: "day",
				FoundBy: "the uptime check “" + firstNonEmpty(c.Name, host) + "” in Pocket Monitoring"}
			if base != "" {
				f.SourceURL = base + "/server/" + itoa(srv.ID) + "?tab=uptime"
			}
			s.add(ctx, sc, f)
		}
	}
	if sc.rec.Looked == 0 && firstErr != nil {
		return firstErr
	}
	return nil
}

func hostOf(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(u)), "https://"), "http://")
	h, _, _ := strings.Cut(u, "/")
	h, _, _ = strings.Cut(h, ":")
	return h
}

// ---- Pocket Wealth ----

func (s *Server) scanWealth(ctx context.Context, sc *scanRun) error {
	items, err := sc.conn.client.Maturing(ctx)
	if err != nil {
		return fmt.Errorf("Pocket Wealth: %w", err)
	}
	for _, it := range items {
		sc.rec.Looked++
		if it.MaturesOn == "" {
			continue
		}
		title := "Matures: " + it.Name
		switch {
		case it.Debt:
			title = "Fixed rate ends: " + it.Name
		case it.Kind == "receivable":
			title = "Due back: " + it.Name
		}
		f := store.Found{Ref: "wealth:asset:" + itoa(it.ID), Source: "wealth", Title: title, Kind: "contract", Provider: it.Institution, Number: it.Number,
			ExpiresOn: it.MaturesOn, SourceURL: it.URL, FoundBy: "the date it matures or ends in Pocket Wealth (" + it.KindLabel + ")"}
		s.add(ctx, sc, f)
	}
	return nil
}

// ---- Pocket Pets ----

// scanPets reads each living pet's insurance and, from the health log, the
// latest vaccine of each name (an older one of the same name was superseded
// by it); its "valid until" is when the booster is due. Pets that passed
// away or were archived are left out, with their vaccines.
func (s *Server) scanPets(ctx context.Context, sc *scanRun) error {
	pets, err := sc.conn.client.Pets(ctx)
	if err != nil {
		return fmt.Errorf("Pocket Pets: %w", err)
	}
	base := strings.TrimRight(sc.conn.public, "/")
	live := map[int64]string{}
	for _, p := range pets {
		sc.rec.Looked++
		if p.PassedOn != "" || p.Archived {
			continue
		}
		name := firstNonEmpty(strings.TrimSpace(p.Name), "Pet")
		live[p.ID] = name
		if p.InsuredUntil == "" {
			continue
		}
		f := store.Found{Ref: "pets:pet:" + itoa(p.ID) + ":insurance", Source: "pets", Title: name + ": Pet insurance", Kind: "insurance", Holder: name,
			Provider: p.Insurer, Number: p.PolicyNo, ExpiresOn: p.InsuredUntil, Currency: p.Currency, FoundBy: "the pet's insurance in Pocket Pets"}
		if base != "" {
			f.SourceURL = base + "/pets/" + itoa(p.ID)
		}
		s.add(ctx, sc, f)
	}
	if len(live) == 0 {
		return nil
	}
	recs, err := sc.conn.client.Vaccines(ctx)
	if err != nil {
		return fmt.Errorf("Pocket Pets health log: %w", err)
	}
	latest := map[string]pocket.PetRecord{}
	var order []string
	for _, r := range recs {
		sc.rec.Looked++
		name := vaccineName(r.Title)
		if _, ok := live[r.PetID]; !ok || name == "" || (r.Kind != "" && r.Kind != "vaccine") {
			continue
		}
		key := itoa(r.PetID) + ":" + name
		have, ok := latest[key]
		if !ok {
			order = append(order, key)
		} else if have.Date > r.Date || (have.Date == r.Date && have.ID > r.ID) {
			continue
		}
		latest[key] = r
	}
	for _, key := range order {
		r := latest[key]
		if r.ValidUntil == "" {
			continue // the latest one has no end date: nothing runs out
		}
		pet := live[r.PetID]
		title := strings.TrimSpace(r.Title)
		if t := strings.ToLower(title); !strings.Contains(t, "vaccin") && !strings.Contains(t, "vakcin") && !strings.Contains(t, "cjep") {
			title += " vaccine"
		}
		f := store.Found{Ref: "pets:vaccine:" + key, Source: "pets", Title: pet + ": " + title, Kind: "health", Holder: pet, Provider: r.ProName,
			IssuedOn: r.Date, ExpiresOn: r.ValidUntil, FoundBy: "the latest “" + strings.TrimSpace(r.Title) + "” vaccine in Pocket Pets' health log"}
		if days := store.DaysBetween(r.Date, r.ValidUntil); days >= 330 {
			f.RenewN, f.RenewUnit = (days+60)/365, "year"
		}
		if r.Cost > 0 {
			cost := r.Cost
			f.Cost, f.Currency = &cost, r.Currency
		}
		if base != "" {
			f.SourceURL = base + "/log/" + itoa(r.ID)
		}
		s.add(ctx, sc, f)
	}
	return nil
}

// vaccineName is a vaccine's title made comparable: "Rabies " and "rabies" are one.
func vaccineName(title string) string {
	return strings.Join(strings.Fields(strings.ToLower(title)), "-")
}

// ---- Pocket Wallet ----

// walletKinds are the wallet's kinds that run out, as Renewals' kinds, with
// how to say what each one is. Loyalty cards, tickets and boarding passes
// are left out: they do not get renewed.
var walletKinds = map[string]struct{ kind, what string }{
	"passport":   {"passport", "a passport"},
	"id":         {"id_card", "an ID card"},
	"licence":    {"driving_licence", "a driving licence"},
	"insurance":  {"insurance", "an insurance or health card"},
	"membership": {"membership", "a membership card"},
	"gift":       {"other", "a gift card"},
}

// scanWallet reads the end date of the IDs, passports, driving licences and
// insurance cards in Pocket Wallet, and of its memberships and gift cards.
// A gift card with nothing left on it is left out; one with money left says
// how much in "found by".
func (s *Server) scanWallet(ctx context.Context, sc *scanRun) error {
	cards, err := sc.conn.client.Cards(ctx)
	if err != nil {
		return fmt.Errorf("Pocket Wallet: %w", err)
	}
	base := strings.TrimRight(sc.conn.public, "/")
	for _, c := range cards {
		sc.rec.Looked++
		k, ok := walletKinds[c.Kind]
		if !ok || c.Archived || c.ExpiresOn == "" || (c.Kind == "gift" && c.Balance != nil && *c.Balance <= 0) {
			continue
		}
		title := firstNonEmpty(strings.TrimSpace(c.Name), strings.TrimSpace(c.Issuer))
		switch {
		case c.Kind == "gift" && title != "":
			title = "Gift card: " + title
		case c.Kind == "gift":
			title = "Gift card"
		case title == "":
			title = store.KindOf(k.kind).Label
		}
		f := store.Found{Ref: "wallet:card:" + itoa(c.ID), Source: "wallet", Title: title, Kind: k.kind, Holder: c.Holder, Provider: c.Issuer, Number: c.Number, ExpiresOn: c.ExpiresOn, Currency: c.Currency, FoundBy: k.what + " in Pocket Wallet"}
		if validDate(c.ValidFrom) {
			f.IssuedOn = c.ValidFrom
		}
		if c.Kind == "gift" {
			f.RenewUnit = "year" // nothing to renew: spend it before the date
			if c.Balance != nil {
				f.FoundBy += fmt.Sprintf(", with %d.%02d %s left on it when it was found", *c.Balance/100, *c.Balance%100, firstNonEmpty(c.Currency, sc.st.Currency))
			}
		}
		if base != "" {
			f.SourceURL = base + "/cards/" + itoa(c.ID)
		}
		s.add(ctx, sc, f)
	}
	return nil
}

// ---- Pocket Money ----

func (s *Server) scanMoney(ctx context.Context, sc *scanRun) error {
	subs, err := sc.conn.client.Subscriptions(ctx)
	if err != nil {
		return fmt.Errorf("Pocket Money subscriptions: %w", err)
	}
	base := strings.TrimRight(sc.conn.public, "/")
	for _, sub := range subs {
		sc.rec.Looked++
		if !sub.Active || sub.Status == "ended" || sub.Status == "paused" {
			continue
		}
		n, unit := termOf(sub.Freq, sub.Every)
		long := unit == "year" || (unit == "month" && n >= 3)
		date := sub.NextDate
		by := "the next payment of a subscription in Pocket Money"
		if sub.EndDate != "" {
			date, by, long = sub.EndDate, "the end date of a subscription in Pocket Money", true
		}
		if !long || date == "" {
			continue
		}
		kind := firstNonEmpty(guessKind(sub.Name, sub.Payee), "subscription")
		if kind == "other" || kind == "licence" || kind == "certificate" {
			kind = "subscription"
		}
		cost := sub.Amount
		f := store.Found{Ref: "money:subscription:" + itoa(sub.ID), Source: "money", Title: sub.Name, Kind: kind, Provider: sub.Payee, ExpiresOn: date,
			AutoRenews: sub.EndDate == "", RenewN: n, RenewUnit: unit, Cost: &cost, Currency: sub.Currency, FoundBy: by}
		if base != "" {
			f.SourceURL = base + "/subscriptions/" + itoa(sub.ID)
		}
		if u, ok := cleanURL(sub.URL); ok && u != "" {
			f.SourceURL = firstNonEmpty(f.SourceURL, u)
		}
		s.add(ctx, sc, f)
	}
	return nil
}

// termOf reads Money's frequency ("yearly", "monthly", "weekly", "quarterly", "daily").
func termOf(freq string, every int) (int, string) {
	if every <= 0 {
		every = 1
	}
	f := strings.ToLower(freq)
	switch {
	case strings.Contains(f, "year") || strings.Contains(f, "annual"):
		return every, "year"
	case strings.Contains(f, "quarter"):
		return 3 * every, "month"
	case strings.Contains(f, "month"):
		return every, "month"
	case strings.Contains(f, "week"):
		return every, "week"
	case strings.Contains(f, "day") || strings.Contains(f, "daily"):
		return every, "day"
	}
	return every, "month"
}

// ---- Pocket Email ----

// emailQueries find renewal notices by their subject.
var emailQueries = []string{"subject:expires", "subject:expiring", "subject:expiration", "subject:expired", "subject:renewal", "subject:renew",
	"subject:\"valid until\"", "subject:ističe", "subject:istječe", "subject:obnova", "subject:produženje", "subject:läuft", "subject:verlängerung"}

func (s *Server) scanEmail(ctx context.Context, sc *scanRun) error {
	days := sc.st.EmailDays
	if days <= 0 || days > 3650 {
		days = 365
	}
	seen := map[int64]bool{}
	var msgs []pocket.Message
	var firstErr error
	for _, q := range emailQueries {
		list, err := sc.conn.client.SearchMessages(ctx, q+fmt.Sprintf(" newer_than:%dd", days), 40)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("Pocket Email: %w", err)
			}
			continue
		}
		for _, m := range list {
			if !seen[m.ID] && !isNewsletter(m.Subject) {
				seen[m.ID] = true
				msgs = append(msgs, m)
			}
		}
	}
	if len(msgs) == 0 && firstErr != nil {
		return firstErr
	}
	base := strings.TrimRight(sc.conn.public, "/")
	for _, m := range msgs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		sc.rec.Looked++
		cacheKey := fmt.Sprintf("airead:%d:mail:%d", sc.uid, m.ID)
		var got aiFound
		if v, _, ok := s.store.CacheGet(ctx, cacheKey); ok {
			_ = json.Unmarshal([]byte(v), &got)
		} else {
			if sc.aiLeft <= 0 {
				continue
			}
			sc.aiLeft--
			text, err := sc.conn.client.MessageText(ctx, m.ID)
			if err != nil {
				continue
			}
			got, err = s.aiRead(ctx, sc.ai, fmt.Sprintf("An email.\nFrom: %s <%s>\nDate: %s\nSubject: %s\n\n%s", m.From.Name, m.From.Addr, m.Date, m.Subject, clip(text, 6000)))
			if err != nil {
				s.log.Debug("scan: AI could not read an email", "msg", m.ID, "err", err)
				continue
			}
			b, _ := json.Marshal(got)
			_ = s.store.CachePut(ctx, cacheKey, string(b))
		}
		if !got.RunsOut || !validDate(got.ExpiresOn) || got.ExpiresOn == "" {
			continue
		}
		f := store.Found{Source: "email", Title: m.Subject, Kind: "other", Provider: m.From.Name, FoundBy: "AI read the email “" + clip(m.Subject, 80) + "”", Unsure: true}
		got.into(&f)
		// the same thing in several emails (a reminder, then a last reminder) is one renewal
		f.Ref = "email:" + f.Kind + ":" + firstNonEmpty(slugify(f.Number), slugify(f.Provider+" "+f.Title))
		if base != "" {
			f.SourceURL = base + "/search/" + itoa(m.ID)
		}
		s.add(ctx, sc, f)
	}
	return nil
}

func isNewsletter(subject string) bool {
	t := strings.ToLower(subject)
	for _, w := range []string{"offer expires", "sale", "% off", "discount", "deal", "webinar", "newsletter", "popust", "akcija"} {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

// ---- the API ----

// sourceState is a source as Find in my apps shows it.
type sourceState struct {
	scanSource
	AppSlug   string      `json:"app"`
	Mode      string      `json:"mode"`
	Connected bool        `json:"connected"`
	Detail    string      `json:"detail"`
	Included  bool        `json:"included"`
	Running   bool        `json:"running"`
	Last      *store.Scan `json:"last"`
	Count     int         `json:"count"`
	AIReady   bool        `json:"ai_ready"`
	OpenURL   string      `json:"open_url"`
}

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	u := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	last, _ := s.store.LastScans(ctx, uid)
	counts := map[string]int{}
	if all, err := s.store.ListRenewals(ctx, uid, store.RenewalFilter{Archived: true}); err == nil {
		for _, rn := range all {
			counts[rn.Source]++
		}
	}
	aiCl, _, _ := s.aiFor(ctx, uid, st)
	out := []sourceState{}
	for _, src := range scanSources {
		mode, via, detail := s.connStatus(ctx, u, st, src.App)
		ss := sourceState{scanSource: src, AppSlug: src.App.slug, Mode: mode, Connected: via != "", Detail: detail, Included: st.Source(src.Name),
			Running: s.scanning(uid, src.Name), Count: counts[src.Name], AIReady: aiCl != nil}
		if l, ok := last[src.Name]; ok {
			ss.Last = &l
		}
		if via != "" {
			ss.OpenURL = s.siblingLink(ctx, uid, src.App, "")
		}
		out = append(out, ss)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": out, "live": st.Live, "scan_nightly": st.ScanNightly,
		"stream": s.streamStatus(), "documents_ai": st.DocumentsAI})
}

// handleScan starts a scan of one source ({"source": "home"}) or every
// included one ({"source": "all"}) in the background; the page polls /sources.
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Source string `json:"source"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	st, _ := s.store.GetSettings(ctx, uid)
	var todo []scanSource
	if in.Source == "all" || in.Source == "" {
		u := userFrom(ctx)
		for _, src := range scanSources {
			if _, via, _ := s.connStatus(ctx, u, st, src.App); via != "" && st.Source(src.Name) {
				todo = append(todo, src)
			}
		}
		if len(todo) == 0 {
			writeErr(w, http.StatusConflict, "none of the apps to look in is connected yet: switch them on in Pocket Core → Links, or set them up in Settings → Connected apps")
			return
		}
	} else {
		src, ok := sourceByName(in.Source)
		if !ok {
			writeErr(w, http.StatusBadRequest, "unknown source")
			return
		}
		if s.scanning(uid, src.Name) {
			writeErr(w, http.StatusConflict, errBusy.Error())
			return
		}
		todo = []scanSource{src}
	}
	names := []string{}
	for _, src := range todo {
		names = append(names, src.Name)
		s.scanMu.Lock()
		busy := s.scans[s.scanKey(uid, src.Name)]
		s.scanMu.Unlock()
		if busy {
			continue
		}
		go func(src scanSource) {
			bctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			if _, err := s.runScan(bctx, uid, src, "manual", 0); err != nil && !errors.Is(err, errBusy) {
				s.log.Info("scan finished with an error", "user", uid, "source", src.Name, "err", err)
			}
		}(src)
	}
	// let the goroutines mark themselves running before the page polls
	time.Sleep(50 * time.Millisecond)
	writeJSON(w, http.StatusAccepted, map[string]any{"started": names})
}

func (s *Server) handleListScans(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := s.store.ListScans(ctx, actorID(ctx), 50)
	if err != nil {
		s.fail(w, err, "scans")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// nightlyScans looks through every included, connected app once a night per
// person, at 04:00, so whatever the live events missed is caught by morning.
func (s *Server) nightlyScans(ctx context.Context) {
	now := time.Now()
	if now.Hour() != 4 || now.Minute() > 10 {
		return
	}
	ids, err := s.store.SettingsUsers(ctx)
	if err != nil {
		return
	}
	for _, uid := range ids {
		key := fmt.Sprintf("nightly:%d:%s", uid, store.Today())
		if _, _, done := s.store.CacheGet(ctx, key); done {
			continue
		}
		st, err := s.store.GetSettings(ctx, uid)
		if err != nil || !st.ScanNightly {
			continue
		}
		_ = s.store.CachePut(ctx, key, "1")
		u, err := s.store.GetUser(ctx, uid)
		if err != nil {
			continue
		}
		go func(uid int64, u *store.User, st store.Settings) {
			for _, src := range scanSources {
				if _, via, _ := s.connStatus(ctx, u, st, src.App); via == "" || !st.Source(src.Name) {
					continue
				}
				bctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
				_, _ = s.runScan(bctx, uid, src, "nightly", 0)
				cancel()
			}
		}(uid, u, st)
	}
}
