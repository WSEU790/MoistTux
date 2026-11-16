package forecast

import (
	"math"
	"testing"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/store"
)

const gib = 1 << 30

func series(hours int, start, perDay float64, total uint64, noise func(i int) float64) []store.FSPoint {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var out []store.FSPoint
	for i := 0; i < hours; i++ {
		used := start + perDay*float64(i)/24
		if noise != nil {
			used += noise(i)
		}
		out = append(out, store.FSPoint{Time: t0.Add(time.Duration(i) * time.Hour), Used: uint64(used), Avail: total - uint64(used), Total: total})
	}
	return out
}

func TestSteadyGrowthPredictsFullDate(t *testing.T) {
	// 100 GiB disk, 60 GiB used after 7 days of +2 GiB/day.
	pts := series(7*24, 46*gib, 2*gib, 100*gib, nil)
	f := Fit(pts)
	if f.DaysLeft == nil {
		t.Fatalf("no forecast: %+v", f)
	}
	free := 100*gib - float64(pts[len(pts)-1].Used)
	want := free / (2 * gib)
	if math.Abs(*f.DaysLeft-want) > 0.2 || f.R2 < 0.99 {
		t.Fatalf("days left %.2f (want %.2f), r2 %.3f", *f.DaysLeft, want, f.R2)
	}
}

func TestFlatAndNoisyAndShortHistory(t *testing.T) {
	if f := Fit(series(72, 50*gib, 0, 100*gib, nil)); f.DaysLeft != nil || f.Note != "not growing" {
		t.Errorf("flat disk: %+v", f)
	}
	// Big swings (logs rotated, caches cleared) without a trend.
	noisy := func(i int) float64 { return float64((i*7919)%13) * 2 * gib }
	if f := Fit(series(72, 40*gib, 0.05*gib, 100*gib, noisy)); f.DaysLeft != nil {
		t.Errorf("noisy disk produced a forecast: %+v", f)
	}
	if f := Fit(series(5, 40*gib, 10*gib, 100*gib, nil)); f.DaysLeft != nil || f.Note == "" {
		t.Errorf("short history produced a forecast: %+v", f)
	}
	if f := Fit(nil); f.Note != "no history yet" {
		t.Errorf("empty: %+v", f)
	}
}
