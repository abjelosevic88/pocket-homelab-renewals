package server

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/abjelosevic88/pocket-homelab-renewals/internal/store"
)

// seedDemo fills the owner's account with a family's renewals in every
// state (expired, coming up, later, found in other apps and waiting for a
// look, renewing by themselves) and years of renewal history (DEMO_DATA,
// for `make run`, the CI smoke test and the screenshots). Dates are relative
// to today, so the demo always looks current.
func (s *Server) seedDemo(ctx context.Context, uid int64) error {
	// with AUTH_MODE=none this runs inside the first request; finish even
	// when that client goes away
	ctx = context.WithoutCancel(ctx)
	if have, _ := s.store.ListRenewals(ctx, uid, store.RenewalFilter{Archived: true}); len(have) > 0 {
		return nil
	}
	rng := rand.New(rand.NewSource(42))
	today := store.Today()
	day := func(n int) string { return store.AddDays(today, n) }
	cost := func(v int64) *int64 { return &v }
	type demo struct {
		r       store.Renewal
		history int // past renewals to write
	}
	list := []demo{
		{store.Renewal{Title: "Passport", Kind: "passport", Holder: "Me", Provider: "Ministry of Civil Affairs", Number: "B1234567", IssuedOn: store.AddInterval(day(140), -10, "year"), ExpiresOn: day(140), Cost: cost(5500), RenewURL: "https://www.example.gov/passports", Notes: "Book the appointment online; bring the old passport and one photo."}, 1},
		{store.Renewal{Title: "Passport", Kind: "passport", Holder: "Ana", Provider: "Ministry of Civil Affairs", Number: "B7654321", IssuedOn: day(-2200), ExpiresOn: day(1450), Cost: cost(5500)}, 0},
		{store.Renewal{Title: "ID card", Kind: "id_card", Holder: "Me", Provider: "Municipality", Number: "1A2B3C", ExpiresOn: day(780), Cost: cost(1500)}, 1},
		{store.Renewal{Title: "ID card", Kind: "id_card", Holder: "Luka", Provider: "Municipality", ExpiresOn: day(-12), Cost: cost(1000), Notes: "Children's ID cards last five years."}, 0},
		{store.Renewal{Title: "Driving licence", Kind: "driving_licence", Holder: "Me", Number: "DL-889100", ExpiresOn: day(1830), Cost: cost(3000)}, 1},
		{store.Renewal{Title: "ESTA travel authorisation", Kind: "visa", Holder: "Ana", Provider: "U.S. Customs and Border Protection", ExpiresOn: day(205), RenewN: 2, RenewUnit: "year", Cost: cost(2000), RenewURL: "https://esta.cbp.dhs.gov"}, 1},
		{store.Renewal{Title: "Home insurance", Kind: "insurance", Provider: "Allianz", Number: "HI-2024-55120", ExpiresOn: day(25), AutoRenews: true, Cost: cost(18900), Notes: "Covers the flat and its contents; storm damage included."}, 4},
		{store.Renewal{Title: "Car insurance", Kind: "insurance", Holder: "the Golf", Provider: "Generali", Number: "AO-778812", ExpiresOn: day(62), Cost: cost(41200), LeadDays: 14}, 3},
		{store.Renewal{Title: "Travel insurance", Kind: "insurance", Holder: "Family", Provider: "Uniqa", ExpiresOn: day(181), AutoRenews: true, Cost: cost(8900)}, 2},
		{store.Renewal{Title: "Registration", Kind: "vehicle", Holder: "the Golf", Provider: "Vehicle registration office", Number: "A12-K-345", ExpiresOn: day(41), Cost: cost(16500), Source: "garage", SourceRef: "garage:reminder:3", FoundBy: "a reminder in Pocket Garage"}, 3},
		{store.Renewal{Title: "Technical inspection", Kind: "vehicle", Holder: "the Golf", ExpiresOn: day(41), Cost: cost(4500)}, 3},
		{store.Renewal{Title: "Austrian motorway vignette", Kind: "vehicle", Holder: "the Golf", Provider: "ASFINAG", ExpiresOn: day(8), Cost: cost(10340), RenewURL: "https://shop.asfinag.at"}, 2},
		{store.Renewal{Title: "Warranty: Laptop", Kind: "warranty", Holder: "Flat", Provider: "Lenovo", Number: "PF3K9X2", ExpiresOn: day(210), RenewN: 0, Source: "home", SourceRef: "home:thing:7", FoundBy: "the appliance's warranty in Pocket Home", NeedsCheck: true}, 0},
		{store.Renewal{Title: "Warranty: Washing machine", Kind: "warranty", Holder: "Flat", Provider: "Bosch", ExpiresOn: day(402), RenewN: 0, Source: "home", SourceRef: "home:thing:3", FoundBy: "the appliance's warranty in Pocket Home"}, 0},
		{store.Renewal{Title: "Phone warranty", Kind: "warranty", Holder: "Ana", Provider: "Samsung", ExpiresOn: day(-20), RenewN: 0}, 0},
		{store.Renewal{Title: "Domain family-photos.net", Kind: "domain", Provider: "Namecheap", ExpiresOn: day(45), AutoRenews: true, Cost: cost(1398), RenewURL: "https://www.namecheap.com"}, 3},
		{store.Renewal{Title: "Domain ana-design.com", Kind: "domain", Holder: "Ana", Provider: "Cloudflare", ExpiresOn: day(300), AutoRenews: true, Cost: cost(1046)}, 2},
		{store.Renewal{Title: "TLS certificate: cloud.family-photos.net", Kind: "certificate", Holder: "Home server", Provider: "Let's Encrypt", ExpiresOn: day(19), RenewN: 90, RenewUnit: "day", Source: "monitoring", SourceRef: "monitoring:cert:cloud.family-photos.net", FoundBy: "the uptime check “Nextcloud” in Pocket Monitoring", NeedsCheck: true}, 0},
		{store.Renewal{Title: "Microsoft 365 Family", Kind: "subscription", Provider: "Microsoft", ExpiresOn: day(97), AutoRenews: true, Cost: cost(9900), Source: "money", SourceRef: "money:subscription:12", FoundBy: "the next payment of a subscription in Pocket Money"}, 3},
		{store.Renewal{Title: "1Password Families", Kind: "subscription", Provider: "AgileBits", ExpiresOn: day(160), AutoRenews: true, Cost: cost(5988)}, 2},
		{store.Renewal{Title: "Gym membership", Kind: "membership", Holder: "Me", Provider: "FitZone", ExpiresOn: day(33), Cost: cost(36000), Notes: "Ask for the family discount when renewing."}, 2},
		{store.Renewal{Title: "Mobile contract", Kind: "contract", Holder: "Me", Provider: "A1", Number: "C-0099812", ExpiresOn: day(120), LeadDays: 30, RenewN: 2, RenewUnit: "year", Notes: "Cancel in writing a month before it ends, or it runs on for another year."}, 1},
		{store.Renewal{Title: "Flat lease", Kind: "contract", Provider: "Mr. Horvat (landlord)", ExpiresOn: day(380), LeadDays: 90, Cost: cost(0)}, 1},
		{store.Renewal{Title: "Tetanus booster", Kind: "health", Holder: "Me", ExpiresOn: day(720), RenewN: 10, RenewUnit: "year"}, 0},
		{store.Renewal{Title: "Allergy prescription", Kind: "health", Holder: "Luka", Provider: "Dr. Kovač", ExpiresOn: day(15), RenewN: 6, RenewUnit: "month", Source: "documents", SourceRef: "documents:41", FoundBy: "AI read the document's text", NeedsCheck: true}, 1},
		{store.Renewal{Title: "Fishing licence", Kind: "licence", Holder: "Me", Provider: "Sport fishing association", ExpiresOn: day(70), Cost: cost(6000)}, 2},
		{store.Renewal{Title: "Fire extinguisher", Kind: "other", Holder: "Hallway", Provider: "Gloria", ExpiresOn: day(225), RenewN: 2, RenewUnit: "year", Source: "inventory", SourceRef: "inventory:item:18", FoundBy: "the item's expiry date in Pocket Inventory", NeedsCheck: true}, 0},
		{store.Renewal{Title: "Health insurance card", Kind: "insurance", Holder: "Ana", Provider: "Health insurance fund", Source: "documents", SourceRef: "documents:57", FoundBy: "the “Valid until” field in Pocket Documents", ExpiresOn: day(290), NeedsCheck: true}, 0},
		{store.Renewal{Title: "Library card", Kind: "membership", Holder: "Luka", Provider: "City library", Notes: "No end date on the card; ask at the desk."}, 0},
		{store.Renewal{Title: "Old car insurance (Fiat)", Kind: "insurance", Holder: "the Fiat", Provider: "Generali", ExpiresOn: day(-400), Archived: true, Notes: "Sold the Fiat."}, 0},
	}
	for _, d := range list {
		r := d.r
		k := store.KindOf(r.Kind)
		if r.RenewN == 0 && r.RenewUnit == "" && r.Kind != "warranty" {
			r.RenewN, r.RenewUnit = k.TermN, k.TermU
		}
		if r.RenewUnit == "" {
			r.RenewUnit = "year"
		}
		if r.Source == "" {
			r.Source = "demo"
		}
		r.Currency = "EUR"
		check := r.NeedsCheck
		if err := s.store.CreateRenewal(ctx, uid, &r); err != nil {
			return fmt.Errorf("demo renewal %s: %w", r.Title, err)
		}
		if !check && r.NeedsCheck {
			_, _ = s.store.SetChecked(ctx, uid, []int64{r.ID})
		}
		// the renewals before this one: each term back from the current date
		if d.history == 0 || r.ExpiresOn == "" || r.RenewN == 0 {
			continue
		}
		end := r.ExpiresOn
		var hs []store.History
		for i := 0; i < d.history; i++ {
			prev := store.AddInterval(end, -r.RenewN, r.RenewUnit)
			h := store.History{RenewalID: r.ID, RenewedOn: store.AddDays(prev, -rng.Intn(20)), OldExpires: prev, NewExpires: end, Currency: "EUR", Source: "demo"}
			if r.AutoRenews {
				h.RenewedOn, h.Note, h.Source = prev, "Renewed by itself", "auto"
			}
			if r.Cost != nil && *r.Cost > 0 {
				// prices crept up a little each year
				h.Cost = *r.Cost * int64(100-4*i-rng.Intn(3)) / 100
			}
			if h.RenewedOn > today {
				break
			}
			hs = append(hs, h)
			end = prev
		}
		for i := len(hs) - 1; i >= 0; i-- {
			_ = s.store.InsertHistory(ctx, uid, &hs[i])
		}
	}
	// a few scans, as if the apps had been looked through
	for _, src := range []struct {
		name                 string
		looked, found, added int
		ago                  int
	}{{"documents", 312, 14, 2, 1}, {"home", 18, 6, 2, 1}, {"monitoring", 9, 3, 1, 2}, {"inventory", 41, 1, 1, 3}, {"money", 24, 4, 1, 3}, {"garage", 12, 2, 1, 6}} {
		sc, err := s.store.StartScan(ctx, uid, src.name, "weekly")
		if err != nil {
			continue
		}
		sc.Looked, sc.Found, sc.Added = src.looked, src.found, src.added
		_ = s.store.FinishScan(ctx, uid, sc)
		_, _ = s.store.DB.ExecContext(ctx, `UPDATE scans SET started_at = ?, finished_at = ? WHERE id = ?`, day(-src.ago)+"T04:00:02Z", day(-src.ago)+"T04:00:09Z", sc.ID)
	}
	return nil
}
