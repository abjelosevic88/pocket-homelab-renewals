package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/store"
)

// ---- the other Pocket apps ----
//
// Each person chooses how Renewals reaches each app: through a Pocket Core app
// link (Renewals asks Core for a five-minute token acting as that person,
// nothing to paste), directly with their own URL + API token, or not at
// all. Core tokens are asked for per request with only the scopes the work
// needs and never kept, so a link switched off in Core applies at once.

const (
	scopeAccountsRead  = "accounts:read"
	scopeTxRead        = "transactions:read"
	scopeTxWrite       = "transactions:write"
	scopeCalendarRead  = "calendar:read"
	scopeCalendarWrite = "calendar:write"
	scopeTasksWrite    = "tasks:write"
	scopeContactsRead  = "contacts:read"
	scopeDocumentsRead = "documents:read"
	scopeEventsRead    = "events:read"
	scopeHomeRead      = "home:read"
	scopeVehiclesRead  = "vehicles:read"
	scopeItemsRead     = "items:read"
	scopeMonitorRead   = "monitoring:read"
	scopeTripsRead     = "trips:read"
	scopeMailRead      = "mail:read"
	scopeWealthRead    = "wealth:read"
	scopePetsRead      = "pets:read"
)

// pocketApp is one sibling Pocket app Renewals acts in.
type pocketApp struct {
	slug, name string
	scopes     []string
	reason     string
}

var (
	appDocuments  = pocketApp{"documents", "Pocket Documents", []string{scopeDocumentsRead, scopeEventsRead}, "Find expiry dates in your documents (passports, policies, warranty cards, contracts) and add each new document's date as soon as it is filed"}
	appHome       = pocketApp{"home", "Pocket Home", []string{scopeHomeRead, scopeEventsRead}, "Track when the warranties of your appliances end, as soon as you save one"}
	appGarage     = pocketApp{"garage", "Pocket Garage", []string{scopeVehiclesRead, scopeEventsRead}, "Track your vehicles' registration, inspection, insurance and other dated reminders, as soon as you save one"}
	appInventory  = pocketApp{"inventory", "Pocket Inventory", []string{scopeItemsRead, scopeEventsRead}, "Track the expiry dates of items on your shelves (first-aid kit, fire extinguisher, medicine), as soon as you save one"}
	appMonitoring = pocketApp{"monitoring", "Pocket Monitoring", []string{scopeMonitorRead, scopeEventsRead}, "Track when the TLS certificates of the sites you monitor run out, and when they are renewed"}
	appMoney      = pocketApp{"money", "Pocket Money", []string{scopeAccountsRead, scopeTxRead, scopeTxWrite, scopeEventsRead}, "Find yearly subscriptions as soon as you add or pay one, and book what a renewal cost"}
	appWealth     = pocketApp{"wealth", "Pocket Wealth", []string{scopeWealthRead, scopeEventsRead}, "Remind you before a term deposit matures, a fixed mortgage rate ends or money you lent is due back, as soon as you set the date"}
	appPets       = pocketApp{"pets", "Pocket Pets", []string{scopePetsRead, scopeEventsRead}, "Remind you before your pets' insurance runs out or a vaccine needs a booster, as soon as you save it"}
	appEmail      = pocketApp{"email", "Pocket Email", []string{scopeMailRead}, "Find renewal notices in your email (\"your domain expires\", \"your policy renews\")"}
	appTravel     = pocketApp{"travel", "Pocket Travel", []string{scopeTripsRead}, "Warn you when a passport or ID runs out before or soon after a trip"}
	appCalendar   = pocketApp{"calendar", "Pocket Calendar", []string{scopeCalendarRead, scopeCalendarWrite}, "Put expiry dates on your calendar as all-day events, and move them when you renew"}
	appTasks      = pocketApp{"tasks", "Pocket Tasks", []string{scopeTasksWrite}, "Add \"renew it\" to your task list, due by the last day to act"}
	appContacts   = pocketApp{"contacts", "Pocket Contacts", []string{scopeContactsRead}, "Keep who to call (the insurance agent, the registrar) with a renewal"}
	pocketApps    = []pocketApp{appDocuments, appHome, appGarage, appInventory, appMonitoring, appMoney, appWealth, appPets, appEmail, appTravel, appCalendar, appTasks, appContacts}
)

func appBySlug(slug string) (pocketApp, bool) {
	for _, a := range pocketApps {
		if a.slug == slug {
			return a, true
		}
	}
	return pocketApp{}, false
}

// linkUses is the manifest's Uses.
func linkUses() []core.Use {
	out := make([]core.Use, 0, len(pocketApps))
	for _, a := range pocketApps {
		out = append(out, core.Use{App: a.slug, Scopes: a.scopes, Reason: a.reason})
	}
	return out
}

// Connection modes, chosen per person and per app in Settings.
const (
	modeCore   = "core"
	modeDirect = "direct"
	modeOff    = "off"
)

// connMode is the mode in effect: the chosen one, else direct when a token
// was entered, else Pocket Core when Renewals is connected to one, else direct.
func (s *Server) connMode(mode, token string) string {
	switch mode {
	case modeCore, modeDirect, modeOff:
		return mode
	}
	if token != "" || !s.core.Configured() {
		return modeDirect
	}
	return modeCore
}

// notConnected means the person has no working connection to the app.
type notConnected struct{ msg string }

func (e *notConnected) Error() string { return e.msg }

func isNotConnected(err error) bool {
	var nc *notConnected
	return errors.As(err, &nc)
}

// siblingConn is how Renewals reaches one person's copy of a sibling app.
type siblingConn struct {
	client *pocket.Client
	via    string
	public string
}

func linkReason(app, reason string) string {
	switch reason {
	case "off_person":
		return "you switched the Renewals → " + app + " link off in Pocket Core (Settings → Apps acting for me)"
	case "off_admin":
		return "the Renewals → " + app + " link is switched off in Pocket Core (Links)"
	case "target_missing":
		return app + " is not registered with Pocket Core"
	case "not_offered":
		return "this " + app + " version does not accept app links yet"
	case "not_declared":
		return "Pocket Core has not seen this Renewals version yet; it re-registers within a minute of starting"
	case "not_in_audience":
		return "you do not have access to " + app + " in Pocket Core"
	case "unknown_user":
		return "your email is not a person in Pocket Core"
	}
	return ""
}

// connSettings returns a person's saved mode, URL, token and public URL for an app.
func connSettings(st store.Settings, a pocketApp) (mode, url, token, public string) {
	c := st.Conns[a.slug]
	return c.Mode, c.URL, c.Token, c.PublicURL
}

// siblingFor connects to app a for person u with exactly these scopes.
func (s *Server) siblingFor(ctx context.Context, u *store.User, st store.Settings, a pocketApp, scopes ...string) (*siblingConn, error) {
	if u == nil {
		return nil, &notConnected{"nobody to act for"}
	}
	mode, url, token, public := connSettings(st, a)
	switch s.connMode(mode, token) {
	case modeOff:
		return nil, &notConnected{a.name + " is switched off in Settings → Connected apps"}
	case modeDirect:
		if token == "" {
			return nil, &notConnected{a.name + " is not set up (Settings → Connected apps): add a URL and token, or switch to Pocket Core"}
		}
		base := url
		if base == "" {
			base = s.resolveSibling(ctx, a.slug)
		}
		if base == "" {
			return nil, &notConnected{"set the " + a.name + " URL in Settings → Connected apps"}
		}
		return &siblingConn{client: pocket.New(base, token, 15*time.Second), via: modeDirect, public: firstNonEmpty(public, base)}, nil
	}
	if !s.core.Configured() {
		return nil, &notConnected{a.name + " through Pocket Core: there is no Pocket Core on this box; use a direct connection"}
	}
	tok, err := s.core.ActAs(ctx, u.Email, a.slug, scopes...)
	if err != nil {
		var le *core.LinkError
		if errors.As(err, &le) {
			if t := linkReason(a.name, le.Reason); t != "" {
				return nil, &notConnected{a.name + " is not set up: " + t}
			}
			return nil, &notConnected{a.name + " is not set up: " + le.Error()}
		}
		return nil, fmt.Errorf("%s through Pocket Core: %w", a.name, err)
	}
	if tok.URL == "" || tok.AccessToken == "" {
		return nil, fmt.Errorf("%s through Pocket Core: Core gave no address or token", a.name)
	}
	return &siblingConn{client: pocket.New(tok.URL, tok.AccessToken, 15*time.Second), via: modeCore, public: firstNonEmpty(public, firstNonEmpty(tok.PublicURL, tok.URL))}, nil
}

// connStatus says, without issuing anything, how a person reaches an app.
func (s *Server) connStatus(ctx context.Context, u *store.User, st store.Settings, a pocketApp) (eff, via, detail string) {
	mode, _, token, _ := connSettings(st, a)
	eff = s.connMode(mode, token)
	switch eff {
	case modeOff:
		return eff, "", "switched off"
	case modeDirect:
		if token == "" {
			return eff, "", "no token yet"
		}
		return eff, modeDirect, ""
	}
	if u == nil || !s.core.Configured() {
		return eff, "", "there is no Pocket Core on this box"
	}
	list, err := s.core.LinkStatus(ctx, u.Email, a.slug)
	if err != nil {
		return eff, "", "Pocket Core did not answer: " + err.Error()
	}
	state := map[string]core.LinkState{}
	for _, l := range list {
		state[l.Scope] = l
	}
	var on, off []string
	reason := ""
	for _, sc := range a.scopes {
		l, ok := state[sc]
		if ok && l.On {
			on = append(on, sc)
			continue
		}
		off = append(off, sc)
		if reason == "" {
			reason = "not_declared"
			if ok {
				reason = l.State
			}
		}
	}
	text := linkReason(a.name, reason)
	if text == "" {
		text = reason
	}
	switch {
	case len(off) == 0:
		return eff, modeCore, ""
	case len(on) == 0:
		return eff, "", text
	}
	sort.Strings(off)
	return eff, modeCore, "partly: " + strings.Join(off, ", ") + " off (" + text + ")"
}

// clients carries one person's settings and their connections for one request.
type clients struct {
	s      *Server
	u      *store.User
	st     store.Settings
	notify *notify.Client
	conns  map[string]connResult
}

type connResult struct {
	c   *siblingConn
	err error
}

// clients builds the connections from ONE user's own settings.
func (s *Server) clients(ctx context.Context, uid int64) *clients {
	st, _ := s.store.GetSettings(ctx, uid)
	u := userFrom(ctx)
	if u == nil || u.ID != uid {
		u, _ = s.store.GetUser(ctx, uid)
	}
	return &clients{s: s, u: u, st: st, notify: notify.New(st.NotifyURL, st.NotifyFormat), conns: map[string]connResult{}}
}

func (c *clients) conn(ctx context.Context, a pocketApp, scopes ...string) (*siblingConn, error) {
	sc := append([]string(nil), scopes...)
	sort.Strings(sc)
	key := a.slug + " " + strings.Join(sc, " ")
	if r, ok := c.conns[key]; ok {
		return r.c, r.err
	}
	sib, err := c.s.siblingFor(ctx, c.u, c.st, a, scopes...)
	c.conns[key] = connResult{sib, err}
	return sib, err
}

// optional turns "not connected" into (nil, nil).
func optional(sib *siblingConn, err error) (*siblingConn, error) {
	if isNotConnected(err) {
		return nil, nil
	}
	return sib, err
}
