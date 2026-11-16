// Package forecast predicts when filesystems will fill up. It records each
// filesystem's usage hourly (90 days kept) and fits a least-squares line to
// the last 14 days. A forecast is only acted on when the fit is good (R² ≥
// 0.6) and growth is real, so a one-off spike or a single clean-up does not
// produce alarms.
package forecast

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/monitor"
	"github.com/WSEU790/MoistTux/internal/core/store"
	"github.com/WSEU790/MoistTux/internal/protocol"
)

const Rule = "disk_forecast"

// Forecast is the projection for one filesystem.
type Forecast struct {
	ServerID     string     `json:"server_id"`
	Mount        string     `json:"mount"`
	UsedPct      float64    `json:"used_pct"`
	Total        uint64     `json:"total"`
	Used         uint64     `json:"used"`
	GrowthPerDay float64    `json:"growth_bytes_per_day"`
	DaysLeft     *float64   `json:"days_left,omitempty"` // nil: not growing, or not enough history
	FullAt       *time.Time `json:"full_at,omitempty"`
	R2           float64    `json:"fit_r2"`
	Points       int        `json:"points"`
	SpanHours    float64    `json:"history_hours"`
	Note         string     `json:"note,omitempty"`
}

type Engine struct {
	st  *store.Store
	mon *monitor.Engine

	mu   sync.Mutex
	last map[string]time.Time // server → last hourly record
}

func New(st *store.Store, mon *monitor.Engine) *Engine {
	return &Engine{st: st, mon: mon, last: map[string]time.Time{}}
}

// OnSamples records usage at most hourly per server and re-evaluates alerts.
func (e *Engine) OnSamples(srv *store.Server, samples []protocol.MetricsSample) {
	if len(samples) == 0 {
		return
	}
	s := samples[len(samples)-1]
	e.mu.Lock()
	due := s.Time.Sub(e.last[srv.ID]) >= time.Hour
	if due {
		e.last[srv.ID] = s.Time
	}
	e.mu.Unlock()
	if !due {
		return
	}
	ctx := context.Background()
	hour := s.Time.Truncate(time.Hour)
	for _, f := range s.FS {
		if f.Total == 0 || f.ReadOnly {
			continue
		}
		_ = e.st.AddFSUsage(ctx, srv.ID, f.Mount, hour, f.Used, f.Avail, f.Total)
	}
	e.st.PruneFSUsage(ctx, time.Now().Add(-90*24*time.Hour))
	e.Evaluate(ctx, srv.ID)
}

// Server returns forecasts for every filesystem of a server.
func (e *Engine) Server(ctx context.Context, serverID string) ([]Forecast, error) {
	hist, err := e.st.FSUsage(ctx, serverID, time.Now().Add(-14*24*time.Hour))
	if err != nil {
		return nil, err
	}
	var out []Forecast
	for mount, pts := range hist {
		f := Fit(pts)
		f.ServerID, f.Mount = serverID, mount
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return dl(out[i]) < dl(out[j]) })
	return out, nil
}

func dl(f Forecast) float64 {
	if f.DaysLeft == nil {
		return math.Inf(1)
	}
	return *f.DaysLeft
}

// Evaluate raises or clears forecast alerts for a server.
func (e *Engine) Evaluate(ctx context.Context, serverID string) {
	fs, err := e.Server(ctx, serverID)
	if err != nil {
		return
	}
	for _, f := range fs {
		ev := map[string]any{"mount": f.Mount, "used_pct": f.UsedPct, "growth_bytes_per_day": f.GrowthPerDay, "days_left": f.DaysLeft, "fit_r2": f.R2}
		switch {
		case f.DaysLeft != nil && *f.DaysLeft <= 3:
			e.mon.Raise(ctx, serverID, Rule, f.Mount, "critical", fmt.Sprintf("%s will be full in about %.1f days", f.Mount, *f.DaysLeft),
				fmt.Sprintf("%s grows by %s per day (%.0f%% used now); at this rate it is full around %s.", f.Mount, human(f.GrowthPerDay), f.UsedPct, f.FullAt.Local().Format("Mon 2 Jan 15:04")), ev)
		case f.DaysLeft != nil && *f.DaysLeft <= 14:
			e.mon.Raise(ctx, serverID, Rule, f.Mount, "warning", fmt.Sprintf("%s will be full in about %.0f days", f.Mount, *f.DaysLeft),
				fmt.Sprintf("%s grows by %s per day (%.0f%% used now); at this rate it is full around %s.", f.Mount, human(f.GrowthPerDay), f.UsedPct, f.FullAt.Local().Format("Mon 2 Jan")), ev)
		default:
			e.mon.Clear(ctx, serverID, Rule, f.Mount)
		}
	}
}

// Fit computes the trend of a filesystem's usage history.
func Fit(pts []store.FSPoint) Forecast {
	f := Forecast{Points: len(pts)}
	if len(pts) == 0 {
		f.Note = "no history yet"
		return f
	}
	last := pts[len(pts)-1]
	f.Used, f.Total = last.Used, last.Total
	// Like df: used / (used + available); reserved blocks are not usable.
	usable := float64(last.Used) + float64(last.Avail)
	if usable > 0 {
		f.UsedPct = math.Round(float64(last.Used)/usable*1000) / 10
	}
	f.SpanHours = last.Time.Sub(pts[0].Time).Hours()
	if len(pts) < 6 || f.SpanHours < 24 {
		f.Note = "collecting history (a forecast needs at least a day of hourly samples)"
		return f
	}
	// Least squares on (days since first point, used bytes).
	t0 := pts[0].Time
	var sx, sy, sxx, sxy float64
	n := float64(len(pts))
	for _, p := range pts {
		x := p.Time.Sub(t0).Hours() / 24
		y := float64(p.Used)
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return f
	}
	slope := (n*sxy - sx*sy) / den
	icpt := (sy - slope*sx) / n
	var ssRes, ssTot float64
	mean := sy / n
	for _, p := range pts {
		x := p.Time.Sub(t0).Hours() / 24
		y := float64(p.Used)
		pred := icpt + slope*x
		ssRes += (y - pred) * (y - pred)
		ssTot += (y - mean) * (y - mean)
	}
	if ssTot > 0 {
		f.R2 = math.Round((1-ssRes/ssTot)*1000) / 1000
	} else {
		f.R2 = 1
	}
	f.GrowthPerDay = math.Round(slope)
	// Only meaningful growth with a good fit becomes a forecast.
	if slope <= usable*0.0005 || f.R2 < 0.6 {
		if slope > 0 && f.R2 < 0.6 {
			f.Note = "usage changes too irregularly for a reliable forecast"
		} else {
			f.Note = "not growing"
		}
		return f
	}
	days := float64(last.Avail) / slope
	if days > 3650 {
		f.Note = "more than ten years at the current rate"
		return f
	}
	days = math.Round(days*10) / 10
	full := last.Time.Add(time.Duration(days * 24 * float64(time.Hour)))
	f.DaysLeft, f.FullAt = &days, &full
	return f
}

func human(b float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", b, units[i])
}
