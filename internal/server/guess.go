package server

import (
	"regexp"
	"strings"
)

// Guessing what kind of renewal something is from its words (English,
// Bosnian/Croatian/Serbian, German): document types, titles, reminder names.

var kindWords = []struct {
	kind  string
	words []string
}{
	{"passport", []string{"passport", "pasoš", "pasos", "putovnica", "reisepass"}},
	{"id_card", []string{"id card", "identity card", "national id", "lična karta", "licna karta", "lična", "osobna iskaznica", "personalausweis", "identity document"}},
	{"driving_licence", []string{"driving licen", "driver's licen", "drivers licen", "driver licen", "vozačka", "vozacka", "führerschein", "fuhrerschein"}},
	{"visa", []string{"visa", "viza", "residence permit", "boravišn", "boravisn", "dozvola boravka", "aufenthalt", "work permit", "radna dozvola"}},
	{"certificate", []string{"tls certificate", "ssl certificate", "tls cert", "ssl cert"}},
	{"domain", []string{"domain", "domena", "domäne"}},
	{"vehicle", []string{"registration", "registracij", "inspection", "tehnički pregled", "tehnicki pregled", "mot test", "tüv", "tuv", "vignette", "vinjeta", "road tax", "zulassung", "kfz"}},
	{"insurance", []string{"insurance", "insurer", "policy", "polica", "osiguran", "versicherung", "kasko"}},
	{"warranty", []string{"warranty", "guarantee", "garancij", "jamstv", "garantie", "gewährleistung"}},
	{"membership", []string{"membership", "članarin", "clanarin", "season ticket", "mitgliedschaft", "gym"}},
	{"subscription", []string{"subscription", "pretplat", "abonnement", "abo ", "renews automatically", "auto-renew"}},
	{"contract", []string{"contract", "ugovor", "vertrag", "lease", "najam", "zakup", "tenancy", "rental agreement", "mietvertrag"}},
	{"health", []string{"vaccin", "vakcin", "cjepiv", "impf", "prescription", "recept", "rezept", "health card", "zdravstven"}},
	{"licence", []string{"licence", "license", "permit", "dozvola", "lizenz", "genehmigung"}},
}

// guessKind is the first kind whose words appear in the text, "" when none does.
func guessKind(texts ...string) string {
	t := " " + strings.ToLower(strings.Join(texts, " ")) + " "
	for _, kw := range kindWords {
		for _, w := range kw.words {
			if strings.Contains(t, w) {
				return kw.kind
			}
		}
	}
	return ""
}

// expiryField matches the name of a date field that says when something runs out.
var expiryField = regexp.MustCompile(`(?i)(valid|expir|until|renew|end date|ends|gültig|gultig|ablauf|važi|vazi|vrijedi|istek|ističe|istice|traje do|rok važenja|rok vazenja)`)

// notExpiry are date fields that look alike but are not an end date.
var notExpiry = regexp.MustCompile(`(?i)(issue|izdat|ausgestellt|payment|paid|plaćanj|placanj|zahlung|invoice|račun|racun|rechnung|due date|dospijeć|dospjec|delivery|isporuk)`)

func isExpiryField(name string) bool {
	return expiryField.MatchString(name) && !notExpiry.MatchString(name)
}

// foodWords: inventory places and categories whose expiry dates are food, not renewals.
var foodWords = []string{"food", "pantry", "fridge", "freezer", "grocer", "kitchen", "snack", "drink", "beverage", "spice", "hrana", "namirnic", "frižider", "frizider", "zamrzivač", "zamrzivac", "ostava", "špajz", "spajz", "kuhinj", "piće", "lebensmittel", "kühlschrank", "vorrat"}

func isFood(texts ...string) bool {
	t := strings.ToLower(strings.Join(texts, " "))
	for _, w := range foodWords {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

// vehicleRenewalWords pick the garage reminders that are renewals, not services.
var vehicleRenewalWords = []string{"registration", "registracij", "insurance", "osiguran", "inspection", "pregled", "mot", "tüv", "tuv", "vignette", "vinjeta", "tax", "porez", "licen", "permit", "dozvol", "zulassung", "versicherung", "hu/au", "eco", "green card", "zelena karta"}

func isVehicleRenewal(texts ...string) bool {
	t := strings.ToLower(strings.Join(texts, " "))
	for _, w := range vehicleRenewalWords {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

// slugify makes a stable key from free text.
func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
