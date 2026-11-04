package store

import "strings"

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Renewal is something that runs out: a passport, a policy, a warranty, a
// domain. Status and days left are derived on read.
type Renewal struct {
	ID            int64  `json:"id"`
	UserID        int64  `json:"-"`
	Title         string `json:"title"`
	Kind          string `json:"kind"`
	Holder        string `json:"holder"`
	Provider      string `json:"provider"`
	Number        string `json:"number"`
	IssuedOn      string `json:"issued_on"`
	ExpiresOn     string `json:"expires_on"`
	RenewN        int    `json:"renew_n"`
	RenewUnit     string `json:"renew_unit"`
	AutoRenews    bool   `json:"auto_renews"`
	RemindDays    string `json:"remind_days"`
	LeadDays      int    `json:"lead_days"`
	Cost          *int64 `json:"cost"`
	Currency      string `json:"currency"`
	RenewURL      string `json:"renew_url"`
	Notes         string `json:"notes"`
	DocumentID    *int64 `json:"document_id"`
	DocumentTitle string `json:"document_title"`
	ContactID     *int64 `json:"contact_id"`
	ContactName   string `json:"contact_name"`
	Archived      bool   `json:"archived"`
	NeedsCheck    bool   `json:"needs_check"`
	Source        string `json:"source"`
	SourceRef     string `json:"source_ref"`
	SourceURL     string `json:"source_url"`
	FoundBy       string `json:"found_by"`
	NotifiedDays  *int   `json:"-"`
	NotifiedOn    string `json:"-"`
	CalEventID    *int64 `json:"cal_event_id"`
	CalSynced     string `json:"-"`
	TaskID        *int64 `json:"task_id"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`

	Group    string `json:"group"`     // identity | insurance | vehicle | warranty | online | subscription | contract | health | other
	Status   string `json:"status"`    // expired | soon | ok | undated | archived
	DaysLeft int    `json:"days_left"` // to expires_on; 0 when undated
	ActBy    string `json:"act_by"`    // expires_on minus lead_days, when there is a lead time
	Renewals int    `json:"renewals"`  // times renewed
}

// History is one renewal of a Renewal.
type History struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"-"`
	RenewalID  int64  `json:"renewal_id"`
	RenewedOn  string `json:"renewed_on"`
	OldExpires string `json:"old_expires"`
	NewExpires string `json:"new_expires"`
	Cost       int64  `json:"cost"`
	Currency   string `json:"currency"`
	Note       string `json:"note"`
	MoneyTxID  *int64 `json:"money_tx_id"`
	Source     string `json:"source"`
	CreatedAt  string `json:"created_at"`

	Title string `json:"title,omitempty"`
}

// Scan is one look through another app for dates that run out.
type Scan struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"-"`
	Source     string `json:"source"`
	Trigger    string `json:"trigger"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Looked     int    `json:"looked"`
	Found      int    `json:"found"`
	Added      int    `json:"added"`
	Updated    int    `json:"updated"`
	Error      string `json:"error"`
}

// Conn is how a person reaches one sibling Pocket app: "core" (an app link
// through Pocket Core), "direct" (URL + token) or "off"; "" = not chosen yet.
type Conn struct {
	Mode      string `json:"mode"`
	URL       string `json:"url"`
	Token     string `json:"token"`
	PublicURL string `json:"public_url"`
}

// Settings are the box-wide values plus one person's preferences and their
// own connections.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	DateFormat string `json:"date_format"`
	TimeFormat string `json:"time_format"`
	WeekStart  int    `json:"week_start"` // 1 = Monday, 7 = Sunday
	Currency   string `json:"currency"`   // for new renewals
	SoonDays   int    `json:"soon_days"`  // the Overview's "coming up" window

	// Notifications: one message a day at NotifyHour.
	NotifyReminders bool   `json:"notify_reminders"` // a reminder day of a renewal
	NotifyExpired   bool   `json:"notify_expired"`   // on the day it runs out
	NotifyFound     bool   `json:"notify_found"`     // something new was found in another app
	NotifyHour      int    `json:"notify_hour"`
	NotifyURL       string `json:"notify_url"`
	NotifyFormat    string `json:"notify_format"` // apprise | ntfy | json

	// Finding renewals in the other apps.
	Live        bool            `json:"live"`         // new and changed items in the other apps are read at once (their events)
	ScanNightly bool            `json:"scan_nightly"` // look through every included app every night
	ScanSources map[string]bool `json:"scan_sources"` // per source: included in "Scan everything" and the weekly scan
	DocumentsAI bool            `json:"documents_ai"` // read a document's text with AI when no date field says it
	EmailDays   int             `json:"email_days"`   // how far back the email scan looks

	// Pocket Money: a renewal's cost becomes a transaction.
	MoneyBook       bool  `json:"money_book"`
	MoneyAccountID  int64 `json:"money_account_id"`
	MoneyCategoryID int64 `json:"money_category_id"`

	// Pocket Calendar: dates as all-day events.
	CalendarSync bool  `json:"calendar_sync"`
	CalendarID   int64 `json:"calendar_id"` // 0 = the person's default calendar

	// Pocket Travel: warn when a passport or ID runs out before or soon after a trip.
	TravelMonths int `json:"travel_months"` // how long after coming home it must still be valid

	// Connections to the other Pocket apps, by slug.
	Conns map[string]Conn `json:"conns"`

	// AI for reading documents and email: Pocket AI through Core, or an
	// OpenAI-compatible API with the person's own key.
	AIProvider    string `json:"ai_provider"` // auto | pocket | openai
	AIPocketModel string `json:"ai_pocket_model"`
	OpenAIKey     string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"`
}

// Source reports whether a scan source is included (default: yes).
func (st Settings) Source(name string) bool {
	if v, ok := st.ScanSources[name]; ok {
		return v
	}
	return true
}

// SplitTags turns "a, b ,c" into clean lowercase tags.
func SplitTags(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Split(s, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// JoinTags is the stored form of tags.
func JoinTags(s string) string { return strings.Join(SplitTags(s), ", ") }
