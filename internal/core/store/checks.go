package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SiteCheck is a website/HTTP check run from NEXUS.
type SiteCheck struct {
	ID            string    `json:"id"`
	ServerID      string    `json:"server_id"`
	Name          string    `json:"name"`
	URL           string    `json:"url"`
	ExpectStatus  string    `json:"expect_status"` // e.g. "200-399" or "200,301"
	ExpectText    string    `json:"expect_text"`
	IntervalSec   int       `json:"interval_sec"`
	TimeoutSec    int       `json:"timeout_sec"`
	FailThreshold int       `json:"fail_threshold"`
	SlowMs        int       `json:"slow_ms"`
	CertWarnDays  int       `json:"cert_warn_days"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
}

// SiteResult is one check run.
type SiteResult struct {
	CheckID     string     `json:"check_id"`
	Time        time.Time  `json:"ts"`
	OK          bool       `json:"ok"`
	Status      int        `json:"status"`
	LatencyMs   int64      `json:"latency_ms"`
	Error       string     `json:"error,omitempty"`
	CertExpires *time.Time `json:"cert_expires,omitempty"`
}

const checkCols = `id, server_id, name, url, expect_status, expect_text, interval_sec, timeout_sec, fail_threshold, slow_ms, cert_warn_days, enabled, created_at`

func scanCheck(sc interface{ Scan(...any) error }) (*SiteCheck, error) {
	var c SiteCheck
	var en int
	var cr int64
	if err := sc.Scan(&c.ID, &c.ServerID, &c.Name, &c.URL, &c.ExpectStatus, &c.ExpectText, &c.IntervalSec, &c.TimeoutSec, &c.FailThreshold, &c.SlowMs, &c.CertWarnDays, &en, &cr); err != nil {
		return nil, err
	}
	c.Enabled, c.CreatedAt = en == 1, ms(cr)
	return &c, nil
}

func (s *Store) PutSiteCheck(ctx context.Context, c *SiteCheck) error {
	if c.ID == "" {
		c.ID = NewID("chk")
		c.CreatedAt = ms(now())
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO site_checks(`+checkCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET server_id=excluded.server_id, name=excluded.name, url=excluded.url, expect_status=excluded.expect_status,
		expect_text=excluded.expect_text, interval_sec=excluded.interval_sec, timeout_sec=excluded.timeout_sec, fail_threshold=excluded.fail_threshold,
		slow_ms=excluded.slow_ms, cert_warn_days=excluded.cert_warn_days, enabled=excluded.enabled`,
		c.ID, c.ServerID, c.Name, c.URL, c.ExpectStatus, c.ExpectText, c.IntervalSec, c.TimeoutSec, c.FailThreshold, c.SlowMs, c.CertWarnDays, b2i(c.Enabled), c.CreatedAt.UnixMilli())
	return err
}

func (s *Store) ListSiteChecks(ctx context.Context) ([]*SiteCheck, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+checkCols+` FROM site_checks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SiteCheck{}
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetSiteCheck(ctx context.Context, id string) (*SiteCheck, error) {
	c, err := scanCheck(s.db.QueryRowContext(ctx, `SELECT `+checkCols+` FROM site_checks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *Store) DeleteSiteCheck(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM site_checks WHERE id = ?`, id)
	return err
}

func (s *Store) AddSiteResult(ctx context.Context, r *SiteResult) error {
	var ce any
	if r.CertExpires != nil {
		ce = r.CertExpires.UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO site_results(check_id, ts, ok, status, latency_ms, error, cert_expires) VALUES (?,?,?,?,?,?,?)`,
		r.CheckID, r.Time.UnixMilli(), b2i(r.OK), r.Status, r.LatencyMs, r.Error, ce)
	return err
}

// SiteResults returns a check's results since t, oldest first.
func (s *Store) SiteResults(ctx context.Context, checkID string, since time.Time, limit int) ([]SiteResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT check_id, ts, ok, status, latency_ms, error, cert_expires FROM site_results WHERE check_id = ? AND ts >= ? ORDER BY ts DESC LIMIT ?`, checkID, since.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SiteResult
	for rows.Next() {
		var r SiteResult
		var ts int64
		var ok int
		var ce sql.NullInt64
		if err := rows.Scan(&r.CheckID, &ts, &ok, &r.Status, &r.LatencyMs, &r.Error, &ce); err != nil {
			return nil, err
		}
		r.Time, r.OK, r.CertExpires = ms(ts), ok == 1, msPtr(ce)
		out = append(out, r)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

func (s *Store) PruneSiteResults(ctx context.Context, before time.Time) {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM site_results WHERE ts < ?`, before.UnixMilli())
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// FSPoint is one hourly filesystem usage sample.
type FSPoint struct {
	Time  time.Time `json:"ts"`
	Used  uint64    `json:"used"`
	Avail uint64    `json:"avail"` // space available to users, as df reports it
	Total uint64    `json:"total"`
}

func (s *Store) AddFSUsage(ctx context.Context, serverID, mount string, t time.Time, used, avail, total uint64) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO fs_usage(server_id, mount, ts, used, avail, total) VALUES (?,?,?,?,?,?)`, serverID, mount, t.UnixMilli(), int64(used), int64(avail), int64(total))
	return err
}

// FSUsage returns per-mount usage history since t, oldest first.
func (s *Store) FSUsage(ctx context.Context, serverID string, since time.Time) (map[string][]FSPoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mount, ts, used, avail, total FROM fs_usage WHERE server_id = ? AND ts >= ? ORDER BY mount, ts`, serverID, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]FSPoint{}
	for rows.Next() {
		var m string
		var ts, used, avail, total int64
		if err := rows.Scan(&m, &ts, &used, &avail, &total); err != nil {
			return nil, err
		}
		if used < 0 || avail < 0 {
			continue
		}
		out[m] = append(out[m], FSPoint{Time: ms(ts), Used: uint64(used), Avail: uint64(avail), Total: uint64(total)})
	}
	return out, rows.Err()
}

func (s *Store) PruneFSUsage(ctx context.Context, before time.Time) {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM fs_usage WHERE ts < ?`, before.UnixMilli())
}
