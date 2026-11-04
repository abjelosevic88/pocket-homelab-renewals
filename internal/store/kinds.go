package store

import (
	"sort"
	"strconv"
	"strings"
)

// Kind is one sort of thing that runs out, with its sensible defaults.
type Kind struct {
	Value  string `json:"value"`
	Label  string `json:"label"`
	Group  string `json:"group"`
	Remind []int  `json:"remind"` // days before the date
	TermN  int    `json:"term_n"` // the usual term
	TermU  string `json:"term_u"` // its unit
	Lead   int    `json:"lead"`   // how long renewing usually takes, in days
	Travel bool   `json:"travel"` // needed to travel (checked against Pocket Travel)
}

// Kinds, in the order the add form shows them.
var Kinds = []Kind{
	{"passport", "Passport", "identity", []int{270, 180, 90, 30}, 10, "year", 42, true},
	{"id_card", "ID card", "identity", []int{90, 30, 7}, 10, "year", 21, true},
	{"driving_licence", "Driving licence", "identity", []int{90, 30, 7}, 10, "year", 21, false},
	{"visa", "Visa or residence permit", "identity", []int{120, 60, 30, 7}, 1, "year", 30, true},
	{"insurance", "Insurance", "insurance", []int{45, 14, 3}, 1, "year", 0, false},
	{"vehicle", "Vehicle registration or inspection", "vehicle", []int{30, 14, 3}, 1, "year", 0, false},
	{"warranty", "Warranty", "warranty", []int{30, 7}, 0, "year", 0, false},
	{"domain", "Domain name", "online", []int{60, 30, 7, 1}, 1, "year", 0, false},
	{"certificate", "TLS certificate", "online", []int{21, 7, 2}, 90, "day", 0, false},
	{"subscription", "Subscription", "subscription", []int{14, 3}, 1, "year", 0, false},
	{"membership", "Membership or season ticket", "subscription", []int{30, 7}, 1, "year", 0, false},
	{"contract", "Contract (phone, energy, lease)", "contract", []int{60, 30, 7}, 1, "year", 0, false},
	{"health", "Health (vaccination, prescription, check-up)", "health", []int{30, 7}, 1, "year", 0, false},
	{"licence", "Licence or permit", "other", []int{60, 30, 7}, 1, "year", 0, false},
	{"other", "Something else", "other", []int{30, 7}, 1, "year", 0, false},
}

// Groups, in the order the sidebar shows them.
var Groups = []struct {
	Value string `json:"value"`
	Label string `json:"label"`
}{
	{"identity", "Identity and travel"}, {"insurance", "Insurance"}, {"vehicle", "Vehicles"}, {"warranty", "Warranties"},
	{"online", "Domains and certificates"}, {"subscription", "Subscriptions"}, {"contract", "Contracts"}, {"health", "Health"}, {"other", "Other"},
}

// KindOf returns the kind (or "other").
func KindOf(v string) Kind {
	for _, k := range Kinds {
		if k.Value == v {
			return k
		}
	}
	return Kinds[len(Kinds)-1]
}

// ValidKind reports whether v is a kind.
func ValidKind(v string) bool {
	for _, k := range Kinds {
		if k.Value == v {
			return true
		}
	}
	return false
}

// ValidGroup reports whether v is a group.
func ValidGroup(v string) bool {
	for _, g := range Groups {
		if g.Value == v {
			return true
		}
	}
	return false
}

// ParseRemind turns "90, 30,7" into [90 30 7] (largest first, 0–3650, no repeats).
func ParseRemind(s string) []int {
	seen := map[int]bool{}
	var out []int
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 || n > 3650 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out
}

// JoinRemind is the stored form of reminder days.
func JoinRemind(days []int) string {
	parts := make([]string, len(days))
	for i, d := range days {
		parts[i] = strconv.Itoa(d)
	}
	return strings.Join(parts, ",")
}

// Reminders are the days before the date a person hears about it: their
// own list, else the kind's; a lead time adds a reminder two weeks before
// the last day to act.
func (r Renewal) Reminders() []int {
	days := ParseRemind(r.RemindDays)
	if len(days) == 0 {
		days = append([]int(nil), KindOf(r.Kind).Remind...)
	}
	if r.LeadDays > 0 {
		days = ParseRemind(JoinRemind(append(days, r.LeadDays+14)))
	}
	return days
}

// Statuses, most urgent first.
const (
	StatusExpired  = "expired"
	StatusSoon     = "soon"
	StatusOK       = "ok"
	StatusUndated  = "undated"
	StatusArchived = "archived"
)

// StatusRank orders statuses by urgency.
func StatusRank(s string) int {
	switch s {
	case StatusExpired:
		return 0
	case StatusSoon:
		return 1
	case StatusOK:
		return 2
	case StatusUndated:
		return 3
	}
	return 4
}

// derive fills the read-only fields for today.
func (r *Renewal) derive(today string) {
	r.Group = KindOf(r.Kind).Group
	r.ActBy, r.DaysLeft = "", 0
	if r.ExpiresOn != "" {
		r.DaysLeft = DaysBetween(today, r.ExpiresOn)
		if r.LeadDays > 0 {
			r.ActBy = AddDays(r.ExpiresOn, -r.LeadDays)
		}
	}
	rem := r.Reminders()
	window := 30
	if len(rem) > 0 && rem[0] > 0 {
		window = rem[0]
	}
	switch {
	case r.Archived:
		r.Status = StatusArchived
	case r.ExpiresOn == "":
		r.Status = StatusUndated
	case r.DaysLeft < 0:
		r.Status = StatusExpired
	case r.DaysLeft <= window:
		r.Status = StatusSoon
	default:
		r.Status = StatusOK
	}
}

// Stage is the reminder a renewal is at: the smallest reminder day not
// below the days left (0 on the day itself), -1 once it has run out, nil
// before the first reminder.
func (r Renewal) Stage() *int {
	if r.ExpiresOn == "" || r.Archived {
		return nil
	}
	if r.DaysLeft < 0 {
		n := -1
		return &n
	}
	if r.DaysLeft == 0 {
		z := 0
		return &z
	}
	var at *int
	for _, d := range r.Reminders() { // largest first
		if r.DaysLeft <= d {
			v := d
			at = &v
		}
	}
	return at
}
