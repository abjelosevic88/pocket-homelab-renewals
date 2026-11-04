package store

import (
	"time"
)

// Units a renewal term is counted in.
var Units = []string{"day", "week", "month", "year"}

// ValidUnit reports whether u is a repeat unit.
func ValidUnit(u string) bool {
	for _, x := range Units {
		if x == u {
			return true
		}
	}
	return false
}

// AddInterval moves a date on by n units. Months and years keep the day,
// clamped to the end of a shorter month (31 Jan + 1 month = 28/29 Feb).
func AddInterval(date string, n int, unit string) string {
	t, err := ParseDate(date)
	if err != nil || n == 0 {
		return date
	}
	switch unit {
	case "day":
		t = t.AddDate(0, 0, n)
	case "week":
		t = t.AddDate(0, 0, 7*n)
	case "year":
		t = addMonths(t, 12*n)
	default:
		t = addMonths(t, n)
	}
	return t.Format("2006-01-02")
}

func addMonths(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	if d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, 0, 0, 0, 0, time.UTC)
}

// DaysBetween is b - a in whole days.
func DaysBetween(a, b string) int {
	ta, err1 := ParseDate(a)
	tb, err2 := ParseDate(b)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(tb.Sub(ta).Hours() / 24)
}
