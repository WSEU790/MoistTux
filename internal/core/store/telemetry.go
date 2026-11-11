package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

// Point is one row of a telemetry time series as served to the UI and AI.
type Point struct {
	T       int64    `json:"t"`
	CPU     *float64 `json:"cpu"`
	CPUMax  *float64 `json:"cpu_max,omitempty"`
	Mem     *float64 `json:"mem"`
	Swap    *float64 `json:"swap"`
	Load1   *float64 `json:"load1"`
	Load5   *float64 `json:"load5"`
	Load15  *float64 `json:"load15"`
	DiskMax *float64 `json:"disk_max"`
	RxBps   *float64 `json:"rx_bps"`
	TxBps   *float64 `json:"tx_bps"`
	ReadBps *float64 `json:"read_bps"`
	WriteBps *float64 `json:"write_bps"`
	IOWait  *float64 `json:"iowait"`
	Procs   *int64   `json:"procs"`
	TCP     *int64   `json:"tcp"`
	TempMax *float64 `json:"temp_max"`
}

const (
	RawRetention    = 48 * time.Hour
	RollupRetention = 30 * 24 * time.Hour
	RollupBucket    = 5 * time.Minute
)

// Summarise reduces a full sample to the scalar series stored long term.
func Summarise(smp protocol.MetricsSample) (diskMax, rx, tx, rd, wr float64, temp *float64) {
	for _, f := range smp.FS {
		if f.UsedPct > diskMax {
			diskMax = f.UsedPct
		}
	}
	for _, n := range smp.Net {
		if n.Iface == "lo" {
			continue
		}
		rx += n.RxBps
		tx += n.TxBps
	}
	for _, d := range smp.DiskIO {
		rd += d.ReadBps
		wr += d.WriteBps
	}
	for _, t := range smp.Temps {
		v := t.Celsius
		if temp == nil || v > *temp {
			temp = &v
		}
	}
	return
}

func (s *Store) InsertSamples(ctx context.Context, serverID string, samples []protocol.MetricsSample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO telemetry(server_id, ts, cpu, mem, swap, load1, load5, load15, disk_max, rx_bps, tx_bps, read_bps, write_bps, iowait, procs, tcp, temp_max)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, smp := range samples {
		dm, rx, txb, rd, wr, temp := Summarise(smp)
		var t any
		if temp != nil {
			t = *temp
		}
		if _, err := stmt.ExecContext(ctx, serverID, smp.Time.UnixMilli(), smp.CPU.UsagePct, smp.Mem.UsedPct, smp.Mem.SwapPct,
			smp.Load[0], smp.Load[1], smp.Load[2], dm, rx, txb, rd, wr, smp.CPU.IOWaitPct, smp.Procs.Total, smp.TCPConns, t); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func f64(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func i64(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// QueryTelemetry returns points between from and to. Ranges longer than six
// hours (or older than raw retention) are served from 5-minute rollups.
func (s *Store) QueryTelemetry(ctx context.Context, serverID string, from, to time.Time, maxPoints int) ([]Point, string, error) {
	if maxPoints <= 0 {
		maxPoints = 600
	}
	useRollup := to.Sub(from) > 6*time.Hour || from.Before(time.Now().Add(-RawRetention))
	var q string
	res := "raw"
	if useRollup {
		res = "5m"
		q = `SELECT ts, cpu, cpu_max, mem, swap, load1, load5, load15, disk_max, rx_bps, tx_bps, read_bps, write_bps, iowait, procs, tcp, temp_max
			FROM telemetry_5m WHERE server_id = ? AND ts >= ? AND ts <= ? ORDER BY ts`
	} else {
		q = `SELECT ts, cpu, NULL, mem, swap, load1, load5, load15, disk_max, rx_bps, tx_bps, read_bps, write_bps, iowait, procs, tcp, temp_max
			FROM telemetry WHERE server_id = ? AND ts >= ? AND ts <= ? ORDER BY ts`
	}
	rows, err := s.db.QueryContext(ctx, q, serverID, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, res, err
	}
	defer rows.Close()
	var pts []Point
	for rows.Next() {
		var p Point
		var cpu, cmax, mem, swap, l1, l5, l15, dm, rx, tx, rd, wr, iow, temp sql.NullFloat64
		var procs, tcp sql.NullInt64
		if err := rows.Scan(&p.T, &cpu, &cmax, &mem, &swap, &l1, &l5, &l15, &dm, &rx, &tx, &rd, &wr, &iow, &procs, &tcp, &temp); err != nil {
			return nil, res, err
		}
		p.CPU, p.CPUMax, p.Mem, p.Swap = f64(cpu), f64(cmax), f64(mem), f64(swap)
		p.Load1, p.Load5, p.Load15, p.DiskMax = f64(l1), f64(l5), f64(l15), f64(dm)
		p.RxBps, p.TxBps, p.ReadBps, p.WriteBps, p.IOWait = f64(rx), f64(tx), f64(rd), f64(wr), f64(iow)
		p.Procs, p.TCP, p.TempMax = i64(procs), i64(tcp), f64(temp)
		pts = append(pts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, res, err
	}
	return downsample(pts, maxPoints), res, nil
}

// downsample keeps at most n points by averaging consecutive buckets,
// preserving the maximum CPU in each bucket.
func downsample(pts []Point, n int) []Point {
	if len(pts) <= n || n <= 0 {
		return pts
	}
	step := (len(pts) + n - 1) / n
	out := make([]Point, 0, n)
	for i := 0; i < len(pts); i += step {
		end := i + step
		if end > len(pts) {
			end = len(pts)
		}
		out = append(out, avgPoints(pts[i:end]))
	}
	return out
}

func avgPoints(ps []Point) Point {
	out := Point{T: ps[len(ps)-1].T}
	avg := func(get func(Point) *float64) *float64 {
		var sum float64
		n := 0
		for _, p := range ps {
			if v := get(p); v != nil {
				sum += *v
				n++
			}
		}
		if n == 0 {
			return nil
		}
		r := sum / float64(n)
		return &r
	}
	maxf := func(get func(Point) *float64) *float64 {
		var m *float64
		for _, p := range ps {
			if v := get(p); v != nil && (m == nil || *v > *m) {
				x := *v
				m = &x
			}
		}
		return m
	}
	out.CPU = avg(func(p Point) *float64 { return p.CPU })
	out.CPUMax = maxf(func(p Point) *float64 {
		if p.CPUMax != nil {
			return p.CPUMax
		}
		return p.CPU
	})
	out.Mem = avg(func(p Point) *float64 { return p.Mem })
	out.Swap = avg(func(p Point) *float64 { return p.Swap })
	out.Load1 = avg(func(p Point) *float64 { return p.Load1 })
	out.Load5 = avg(func(p Point) *float64 { return p.Load5 })
	out.Load15 = avg(func(p Point) *float64 { return p.Load15 })
	out.DiskMax = maxf(func(p Point) *float64 { return p.DiskMax })
	out.RxBps = avg(func(p Point) *float64 { return p.RxBps })
	out.TxBps = avg(func(p Point) *float64 { return p.TxBps })
	out.ReadBps = avg(func(p Point) *float64 { return p.ReadBps })
	out.WriteBps = avg(func(p Point) *float64 { return p.WriteBps })
	out.IOWait = avg(func(p Point) *float64 { return p.IOWait })
	out.TempMax = maxf(func(p Point) *float64 { return p.TempMax })
	last := ps[len(ps)-1]
	out.Procs, out.TCP = last.Procs, last.TCP
	return out
}

// Rollup aggregates complete 5-minute buckets and enforces retention.
func (s *Store) Rollup(ctx context.Context) error {
	var until int64
	_, _ = s.GetSetting(ctx, "telemetry.rollup_until", &until)
	bucket := RollupBucket.Milliseconds()
	end := (time.Now().UnixMilli()/bucket)*bucket - bucket // last complete bucket boundary
	if until == 0 {
		var minTS sql.NullInt64
		_ = s.db.QueryRowContext(ctx, `SELECT MIN(ts) FROM telemetry`).Scan(&minTS)
		if !minTS.Valid {
			return s.retention(ctx)
		}
		until = (minTS.Int64 / bucket) * bucket
	}
	if end > until {
		_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO telemetry_5m(server_id, ts, cpu, cpu_max, mem, mem_max, swap, load1, load5, load15, disk_max, rx_bps, tx_bps, read_bps, write_bps, iowait, procs, tcp, temp_max, n)
			SELECT server_id, (ts / ?) * ?, AVG(cpu), MAX(cpu), AVG(mem), MAX(mem), AVG(swap), AVG(load1), AVG(load5), AVG(load15), MAX(disk_max),
				AVG(rx_bps), AVG(tx_bps), AVG(read_bps), AVG(write_bps), AVG(iowait), MAX(procs), MAX(tcp), MAX(temp_max), COUNT(*)
			FROM telemetry WHERE ts >= ? AND ts < ? GROUP BY server_id, ts / ?`, bucket, bucket, until, end, bucket)
		if err != nil {
			return err
		}
		if err := s.PutSetting(ctx, "telemetry.rollup_until", end); err != nil {
			return err
		}
	}
	return s.retention(ctx)
}

func (s *Store) retention(ctx context.Context) error {
	n := time.Now()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM telemetry WHERE ts < ?`, n.Add(-RawRetention).UnixMilli()); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM telemetry_5m WHERE ts < ?`, n.Add(-RollupRetention).UnixMilli()); err != nil {
		return err
	}
	// Bounded history for operational tables (audit is kept, it is the record of truth).
	_, _ = s.db.ExecContext(ctx, `DELETE FROM notifications WHERE ts < ?`, n.Add(-90*24*time.Hour).UnixMilli())
	_, _ = s.db.ExecContext(ctx, `DELETE FROM alerts WHERE status = 'resolved' AND resolved_at < ?`, n.Add(-90*24*time.Hour).UnixMilli())
	return nil
}

func (s *Store) TelemetryStats(ctx context.Context) (raw, rollup int64) {
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM telemetry`).Scan(&raw)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM telemetry_5m`).Scan(&rollup)
	return
}
