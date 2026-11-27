package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-renewals/internal/store"
)

// The four routes behind `capabilities` (links.go): /api/v1/pocket/search,
// /day/{date}, /widget and /capture. Each runs as the request's person and
// reads only their renewals in this app's own database: what was found in
// Wallet, Rentals, Tasks and the others is kept here by the scans, so no
// other app is called from these routes.

// renewalURL opens a renewal in the app.
func renewalURL(id int64) string { return "/r/" + itoa(id) }

// kindWord is a kind in plain words, as search shows it: "domain", "ID card".
func kindWord(kind string) string {
	switch kind {
	case "id_card":
		return "ID card"
	case "other":
		return "renewal"
	}
	return strings.ReplaceAll(store.KindOf(kind).Value, "_", " ")
}

// searchKinds are the kinds search answers with, in the add form's order.
func searchKinds() []string {
	out := make([]string, 0, len(store.Kinds))
	for _, k := range store.Kinds {
		out = append(out, kindWord(k.Value))
	}
	return out
}

// moneyText is minor units in plain words: "320 EUR", "13.98 EUR".
func moneyText(minor int64, currency string) string {
	s := strconv.FormatInt(minor/100, 10)
	if c := minor % 100; c != 0 {
		s += fmt.Sprintf(".%02d", c)
	}
	return strings.TrimSpace(s + " " + currency)
}

// everyText says how often: "a year", "every 2 years", "" for once.
func everyText(n int, unit string) string {
	switch {
	case n <= 0 || !store.ValidUnit(unit):
		return ""
	case n == 1:
		return "a " + unit
	}
	return "every " + strconv.Itoa(n) + " " + unit + "s"
}

// costText is a renewal's cost and how often: "320 EUR a year", "" when not known.
func costText(r store.Renewal) string {
	if r.Cost == nil {
		return ""
	}
	return strings.TrimSpace(moneyText(*r.Cost, r.Currency) + " " + everyText(r.RenewN, r.RenewUnit))
}

// hasWord: a query with no letter or digit finds nothing.
func hasWord(q string) bool {
	return strings.IndexFunc(q, func(c rune) bool { return unicode.IsLetter(c) || unicode.IsDigit(c) }) >= 0
}

// ---------- search ----------

// kindMatches: the words start the kind's name or one of its words
// ("insurance", "dom", "id", "driving lic").
func kindMatches(k store.Kind, q string) bool {
	if len([]rune(q)) < 2 {
		return false
	}
	for _, name := range []string{strings.ToLower(k.Label), strings.ToLower(kindWord(k.Value))} {
		if strings.HasPrefix(name, q) {
			return true
		}
		for _, w := range strings.FieldsFunc(name, func(c rune) bool { return !unicode.IsLetter(c) && !unicode.IsDigit(c) }) {
			if strings.HasPrefix(w, q) {
				return true
			}
		}
	}
	return false
}

// pocketSearch finds renewals by name, holder, provider, number and notes
// (the list's own search) and by kind ("insurance"), archived ones too:
// names that hold the words first, archived ones last.
func (s *Server) pocketSearch(r *http.Request, q string, limit int) ([]core.SearchItem, error) {
	if !hasWord(q) {
		return nil, nil
	}
	ctx, uid := r.Context(), actorID(r.Context())
	list, err := s.store.ListRenewals(ctx, uid, store.RenewalFilter{Q: q, Archived: true})
	if err != nil {
		return nil, err
	}
	lq := strings.ToLower(q)
	seen := map[int64]bool{}
	for _, rn := range list {
		seen[rn.ID] = true
	}
	for _, k := range store.Kinds {
		if !kindMatches(k, lq) {
			continue
		}
		more, err := s.store.ListRenewals(ctx, uid, store.RenewalFilter{Kind: k.Value, Archived: true})
		if err != nil {
			return nil, err
		}
		for _, rn := range more {
			if !seen[rn.ID] {
				seen[rn.ID] = true
				list = append(list, rn)
			}
		}
	}
	rank := func(rn store.Renewal) int {
		n := 0
		if rn.Archived {
			n += 2
		}
		if !strings.Contains(strings.ToLower(rn.Title), lq) {
			n++
		}
		return n
	}
	sort.SliceStable(list, func(i, j int) bool { return rank(list[i]) < rank(list[j]) })
	today := store.Today()
	out := make([]core.SearchItem, 0, min(len(list), limit))
	for _, rn := range list {
		if len(out) == limit {
			break
		}
		snippet := searchWhen(rn, today)
		if c := costText(rn); c != "" {
			snippet += " · " + c
		}
		out = append(out, core.SearchItem{ID: itoa(rn.ID), Title: displayName(rn), Snippet: snippet, Kind: kindWord(rn.Kind), Date: rn.ExpiresOn, URL: renewalURL(rn.ID)})
	}
	return out, nil
}

// searchWhen is the date in a search line: "Expires on 2027-03-01",
// "Renews on …", "Expired on …", "Archived", "No date".
func searchWhen(r store.Renewal, today string) string {
	switch {
	case r.ExpiresOn == "":
		return "No date"
	case r.ExpiresOn < today:
		return "Expired on " + r.ExpiresOn
	case r.Archived:
		return "Archived, ran until " + r.ExpiresOn
	case r.AutoRenews:
		return "Renews on " + r.ExpiresOn
	}
	return "Expires on " + r.ExpiresOn
}

// ---------- day ----------

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// pocketDay is the /day/{date} route in the contract's shape: what runs out
// or renews that day (archived ones too, as /day has them), what ran out
// that day and was renewed later, and what was renewed that day.
func (s *Server) pocketDay(r *http.Request, date string, tz *time.Location) (*core.Day, error) {
	ctx, uid := r.Context(), actorID(r.Context())
	today := store.Today()
	if tz != nil {
		today = time.Now().In(tz).Format("2006-01-02")
	}
	ends, err := s.store.ListRenewals(ctx, uid, store.RenewalFilter{After: date, Before: date, Archived: true})
	if err != nil {
		return nil, err
	}
	hist, err := s.store.HistoryOn(ctx, uid, date)
	if err != nil {
		return nil, err
	}
	day := &core.Day{Date: date}
	var sentences []string
	due, done := 0, 0
	add := func(it core.DayItem, sentence string) {
		if it.Amount > 0 {
			sentence += ", " + moneyText(it.Amount, it.Currency)
		}
		day.Items = append(day.Items, it)
		sentences = append(sentences, sentence)
	}
	renewedToday := map[int64]bool{}
	for _, h := range hist {
		if h.RenewedOn == date {
			renewedToday[h.RenewalID] = true
		}
	}
	for _, rn := range ends {
		if renewedToday[rn.ID] {
			continue
		}
		verb, kind := "expires", "expires"
		if rn.AutoRenews {
			verb, kind = "renews", "renews"
		}
		switch {
		case date == today:
			verb += " today"
		case date < today && rn.AutoRenews:
			verb = "was due to renew"
		case date < today:
			verb = "expired"
		}
		it := core.DayItem{ID: "renewal:" + itoa(rn.ID), Title: displayName(rn), Sub: strings.ToUpper(verb[:1]) + verb[1:], Kind: kind, URL: renewalURL(rn.ID)}
		if rn.Cost != nil && *rn.Cost > 0 {
			it.Amount, it.Currency = *rn.Cost, rn.Currency
		}
		due++
		add(it, displayName(rn)+" "+verb)
	}
	for _, h := range hist {
		name := displayName(store.Renewal{Title: h.Title, Holder: h.Holder})
		it := core.DayItem{Title: name, URL: renewalURL(h.RenewalID)}
		if h.Cost > 0 {
			it.Amount, it.Currency = h.Cost, h.Currency
		}
		switch {
		case h.RenewedOn == date:
			it.ID, it.Kind, it.Done, it.Sub = "history:"+itoa(h.ID), "renewed", true, "Renewed"
			if h.NewExpires != "" {
				it.Sub += " until " + h.NewExpires
			}
			verb := "renewed"
			if h.Source == "auto" {
				it.Sub, verb = strings.Replace(it.Sub, "Renewed", "Renewed by itself", 1), "renewed by itself"
			}
			done++
			add(it, name+" "+verb)
		case h.OldExpires == date && h.RenewedOn > date:
			// it ran out that day and was renewed only later; renewed ahead of
			// the day, nothing ran out
			it.ID, it.Kind, it.Sub, it.Amount, it.Currency = "history:"+itoa(h.ID), "expired", "Expired, renewed on "+h.RenewedOn, 0, ""
			due++
			add(it, name+" expired")
		}
	}
	if len(day.Items) == 0 {
		return nil, nil
	}
	if len(day.Items) == 1 {
		day.Summary = sentences[0]
		return day, nil
	}
	var parts []string
	if due > 0 {
		when := "due"
		switch {
		case date == today:
			when = "due today"
		case date < today:
			when = map[bool]string{true: "was due", false: "were due"}[due == 1]
		}
		parts = append(parts, count(due, "renewal", "renewals")+" "+when)
	}
	if done > 0 {
		if len(parts) == 0 {
			parts = append(parts, count(done, "renewal", "renewals")+" done")
		} else {
			parts = append(parts, strconv.Itoa(done)+" done")
		}
	}
	day.Summary = strings.Join(parts, ", ")
	return day, nil
}

// ---------- widget ----------

// widgetDays is how far ahead the widget looks.
const widgetDays = 60

// daysText is "in 12 days", "today", "tomorrow", "yesterday", "3 days ago".
func daysText(d int) string {
	switch {
	case d < -1:
		return strconv.Itoa(-d) + " days ago"
	case d == -1:
		return "yesterday"
	case d == 0:
		return "today"
	case d == 1:
		return "tomorrow"
	}
	return "in " + strconv.Itoa(d) + " days"
}

// pocketWidget is what runs out soonest: what has run out first, then the
// next 60 days, with the year's cost the Overview shows. One read of the
// person's list, as the Overview does.
func (s *Server) pocketWidget(r *http.Request) (*core.WidgetData, error) {
	ctx, uid := r.Context(), actorID(r.Context())
	all, err := s.store.ListRenewals(ctx, uid, store.RenewalFilter{})
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, nil
	}
	var expired, soon []store.Renewal
	yearCost := map[string]int64{}
	for _, rn := range all {
		switch {
		case rn.ExpiresOn == "":
		case rn.DaysLeft < 0:
			expired = append(expired, rn)
		case rn.DaysLeft <= widgetDays:
			soon = append(soon, rn)
		}
		if rn.Cost != nil && rn.RenewN > 0 {
			yearCost[rn.Currency] += perYear(*rn.Cost, rn.RenewN, rn.RenewUnit)
		}
	}
	out := &core.WidgetData{URL: "/", Metrics: []core.Metric{
		{Label: "Expired", Value: strconv.Itoa(len(expired))},
		{Label: "Next " + strconv.Itoa(widgetDays) + " days", Value: strconv.Itoa(len(soon))},
	}}
	if len(yearCost) > 0 {
		curs := make([]string, 0, len(yearCost))
		for c := range yearCost {
			curs = append(curs, c)
		}
		sort.Slice(curs, func(i, j int) bool { return yearCost[curs[i]] > yearCost[curs[j]] })
		var vals []string
		for _, c := range curs {
			vals = append(vals, moneyText((yearCost[c]+50)/100*100, c)) // whole units, as the Overview's tile
		}
		out.Metrics = append(out.Metrics, core.Metric{Label: "A year", Value: strings.Join(vals, " + ")})
	}

	verb := func(rn store.Renewal) string {
		if rn.AutoRenews {
			return "renews"
		}
		return "expires"
	}
	switch {
	case len(expired) == 1:
		out.Line = displayName(expired[0]) + " expired " + daysText(expired[0].DaysLeft)
	case len(expired) > 1:
		out.Line = strconv.Itoa(len(expired)) + " things have expired"
	case len(soon) > 0:
		out.Line = displayName(soon[0]) + " " + verb(soon[0]) + " " + daysText(soon[0].DaysLeft)
	}
	if more := len(soon); len(expired) > 0 && more > 0 {
		out.Line += ", " + strconv.Itoa(more) + " more coming up"
	} else if len(expired) == 0 && more > 1 {
		out.Line += ", " + strconv.Itoa(more-1) + " more coming up"
	}

	for _, rn := range append(expired, soon...) {
		if len(out.Items) == 8 {
			break
		}
		detail := daysText(rn.DaysLeft)
		if rn.DaysLeft < 0 {
			detail = "expired " + detail
		}
		if rn.Cost != nil && *rn.Cost > 0 {
			detail += " · " + moneyText(*rn.Cost, rn.Currency)
		}
		out.Items = append(out.Items, core.WidgetItem{ID: itoa(rn.ID), Title: displayName(rn), Detail: detail, URL: renewalURL(rn.ID)})
	}
	return out, nil
}

// ---------- capture ----------

// captureLine splits a quick line into a renewal's name, notes and date: the
// title when one was given (the text is then the notes), else the text's
// first line; a date (YYYY-MM-DD) at the end of the name is the expiry when
// the date field does not say one.
func captureLine(in core.CaptureInput) (title, notes, date string) {
	if in.Title != "" {
		title, notes = in.Title, in.Text
	} else {
		title, notes, _ = strings.Cut(in.Text, "\n")
	}
	words := strings.Fields(title)
	if n := len(words); n > 0 && len(words[n-1]) == 10 && validDate(words[n-1]) {
		date, words = words[n-1], words[:n-1]
	}
	title, notes = strings.Join(words, " "), strings.TrimSpace(notes)
	if r := []rune(title); len(r) > 200 {
		title, notes = strings.TrimSpace(string(r[:199]))+"…", strings.TrimSpace(in.Title+"\n"+in.Text)
	}
	if in.Date != "" {
		date = in.Date
	} else if in.Due != "" && date == "" {
		date = in.Due
	}
	return title, notes, date
}

// pocketCapture keeps a quick line as a new renewal, as the add form does
// with a name and a date: the kind guessed from the words (else "Something
// else") with its usual term, the person's currency. The source stays "web":
// the person typed it, and the app that passed it on may not change it
// later. The same request_id again answers the same renewal and adds
// nothing: the receipt is written in the transaction that adds it.
func (s *Server) pocketCapture(r *http.Request, in core.CaptureInput) (*core.CaptureResult, error) {
	ctx, uid := r.Context(), actorID(r.Context())
	op, err := core.NewOperation(fmt.Sprintf("renewals:user:%d:%s", uid, core.CapturePath), in.RequestID, in)
	if err != nil {
		return nil, err
	}
	var res core.CaptureResult
	replay := func() (*core.CaptureResult, error) {
		found, err := op.Replay(ctx, s.store.DB, &res)
		switch {
		case errors.Is(err, core.ErrOperationConflict):
			return nil, fmt.Errorf("%w: this was already kept with other words", core.ErrCapture)
		case err != nil || !found:
			return nil, err
		}
		return &res, nil
	}
	if kept, err := replay(); kept != nil || err != nil {
		return kept, err
	}

	title, notes, date := captureLine(in)
	if title == "" {
		return nil, fmt.Errorf("%w: give it a name, e.g. \"Domain bjelke.org 2027-03-01\"", core.ErrCapture)
	}
	st, _ := s.store.GetSettings(ctx, uid)
	k := store.KindOf(firstNonEmpty(guessKind(title), "other"))
	rn := &store.Renewal{Title: title, Notes: notes, Kind: k.Value, ExpiresOn: date, RenewN: k.TermN, RenewUnit: k.TermU, Currency: st.Currency, Source: "web"}
	err = s.store.CreateRenewalWith(ctx, uid, rn, func(tx *sql.Tx, made *store.Renewal) error {
		res = core.CaptureResult{ID: itoa(made.ID), Title: made.Title, URL: renewalURL(made.ID)}
		return op.Save(ctx, tx, res)
	})
	if err != nil {
		// the same line sent twice at once: the other one kept it
		if kept, rerr := replay(); kept != nil || rerr != nil {
			return kept, rerr
		}
		return nil, err
	}
	s.publish(ctx, uid, "renewals.added", "renewal:"+itoa(rn.ID), renewalPayload(rn))
	s.syncCalendarSoon(uid)
	return &res, nil
}
