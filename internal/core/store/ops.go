package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ---------------------------------------------------------------- alerts & incidents

type Alert struct {
	ID          string          `json:"id"`
	Fingerprint string          `json:"fingerprint"`
	ServerID    string          `json:"server_id"`
	Rule        string          `json:"rule"`
	Severity    string          `json:"severity"`
	Subject     string          `json:"subject"`
	Title       string          `json:"title"`
	Detail      string          `json:"detail"`
	Evidence    json.RawMessage `json:"evidence,omitempty"`
	Status      string          `json:"status"` // firing | resolved
	FirstSeen   time.Time       `json:"first_seen"`
	LastSeen    time.Time       `json:"last_seen"`
	ResolvedAt  *time.Time      `json:"resolved_at,omitempty"`
	AckedAt     *time.Time      `json:"acknowledged_at,omitempty"`
	Count       int             `json:"count"`
	IncidentID  string          `json:"incident_id"`
	NotifiedAt  *time.Time      `json:"notified_at,omitempty"`
}

const alertCols = `id, fingerprint, server_id, rule, severity, subject, title, detail, evidence, status, first_seen, last_seen, resolved_at, acknowledged_at, count, incident_id, notified_at`

func scanAlert(sc interface{ Scan(...any) error }) (*Alert, error) {
	var a Alert
	var ev string
	var fs, ls int64
	var res, ack, not sql.NullInt64
	if err := sc.Scan(&a.ID, &a.Fingerprint, &a.ServerID, &a.Rule, &a.Severity, &a.Subject, &a.Title, &a.Detail, &ev, &a.Status, &fs, &ls, &res, &ack, &a.Count, &a.IncidentID, &not); err != nil {
		return nil, err
	}
	if ev != "" {
		a.Evidence = json.RawMessage(ev)
	}
	a.FirstSeen, a.LastSeen = ms(fs), ms(ls)
	a.ResolvedAt, a.AckedAt, a.NotifiedAt = msPtr(res), msPtr(ack), msPtr(not)
	return &a, nil
}

func (s *Store) FiringAlert(ctx context.Context, fingerprint string) (*Alert, error) {
	a, err := scanAlert(s.db.QueryRowContext(ctx, `SELECT `+alertCols+` FROM alerts WHERE fingerprint = ? AND status = 'firing' ORDER BY first_seen DESC LIMIT 1`, fingerprint))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

func (s *Store) InsertAlert(ctx context.Context, a *Alert) error {
	if a.ID == "" {
		a.ID = NewID("alr")
	}
	n := now()
	a.FirstSeen, a.LastSeen, a.Status, a.Count = ms(n), ms(n), "firing", 1
	_, err := s.db.ExecContext(ctx, `INSERT INTO alerts(`+alertCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,NULL,NULL,?,?,NULL)`,
		a.ID, a.Fingerprint, a.ServerID, a.Rule, a.Severity, a.Subject, a.Title, a.Detail, string(a.Evidence), a.Status, n, n, a.Count, a.IncidentID)
	return err
}

func (s *Store) TouchAlert(ctx context.Context, id, severity, detail string, evidence json.RawMessage) error {
	_, err := s.db.ExecContext(ctx, `UPDATE alerts SET last_seen = ?, count = count + 1, severity = ?, detail = ?, evidence = ? WHERE id = ?`, now(), severity, detail, string(evidence), id)
	return err
}

func (s *Store) ResolveAlert(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE alerts SET status = 'resolved', resolved_at = ? WHERE id = ? AND status = 'firing'`, now(), id)
	return err
}

func (s *Store) AckAlert(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE alerts SET acknowledged_at = ? WHERE id = ?`, now(), id)
	return err
}

func (s *Store) MarkAlertNotified(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE alerts SET notified_at = ? WHERE id = ?`, now(), id)
	return err
}

func (s *Store) ListAlerts(ctx context.Context, status, serverID string, limit int) ([]*Alert, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	q := `SELECT ` + alertCols + ` FROM alerts WHERE 1=1`
	var args []any
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	if serverID != "" {
		q += ` AND server_id = ?`
		args = append(args, serverID)
	}
	q += ` ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, last_seen DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Alert{}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) FiringForServer(ctx context.Context, serverID string) ([]*Alert, error) {
	return s.ListAlerts(ctx, "firing", serverID, 1000)
}

type Incident struct {
	ID         string     `json:"id"`
	ServerID   string     `json:"server_id"`
	Title      string     `json:"title"`
	Severity   string     `json:"severity"`
	Status     string     `json:"status"`
	OpenedAt   time.Time  `json:"opened_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	AIStatus   string     `json:"ai_status"`
	AIReport   string     `json:"ai_report"`
	AIConvID   string     `json:"ai_conversation_id"`
	Alerts     []*Alert   `json:"alerts,omitempty"`
}

const incCols = `id, server_id, title, severity, status, opened_at, updated_at, resolved_at, ai_status, ai_report, ai_conversation_id`

func scanIncident(sc interface{ Scan(...any) error }) (*Incident, error) {
	var i Incident
	var o, u int64
	var r sql.NullInt64
	if err := sc.Scan(&i.ID, &i.ServerID, &i.Title, &i.Severity, &i.Status, &o, &u, &r, &i.AIStatus, &i.AIReport, &i.AIConvID); err != nil {
		return nil, err
	}
	i.OpenedAt, i.UpdatedAt, i.ResolvedAt = ms(o), ms(u), msPtr(r)
	return &i, nil
}

// OpenIncidentFor returns the server's open incident updated within window.
func (s *Store) OpenIncidentFor(ctx context.Context, serverID string, window time.Duration) (*Incident, error) {
	i, err := scanIncident(s.db.QueryRowContext(ctx, `SELECT `+incCols+` FROM incidents WHERE server_id = ? AND status = 'open' AND updated_at >= ? ORDER BY updated_at DESC LIMIT 1`,
		serverID, time.Now().Add(-window).UnixMilli()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return i, err
}

func (s *Store) CreateIncident(ctx context.Context, i *Incident) error {
	if i.ID == "" {
		i.ID = NewID("inc")
	}
	n := now()
	i.OpenedAt, i.UpdatedAt, i.Status = ms(n), ms(n), "open"
	_, err := s.db.ExecContext(ctx, `INSERT INTO incidents(`+incCols+`) VALUES (?,?,?,?,?,?,?,NULL,?,?,?)`, i.ID, i.ServerID, i.Title, i.Severity, i.Status, n, n, i.AIStatus, i.AIReport, i.AIConvID)
	return err
}

func (s *Store) UpdateIncident(ctx context.Context, i *Incident) error {
	var res any
	if i.ResolvedAt != nil {
		res = i.ResolvedAt.UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE incidents SET title = ?, severity = ?, status = ?, updated_at = ?, resolved_at = ?, ai_status = ?, ai_report = ?, ai_conversation_id = ? WHERE id = ?`,
		i.Title, i.Severity, i.Status, now(), res, i.AIStatus, i.AIReport, i.AIConvID, i.ID)
	return err
}

func (s *Store) GetIncident(ctx context.Context, id string) (*Incident, error) {
	i, err := scanIncident(s.db.QueryRowContext(ctx, `SELECT `+incCols+` FROM incidents WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+alertCols+` FROM alerts WHERE incident_id = ? ORDER BY first_seen`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		i.Alerts = append(i.Alerts, a)
	}
	return i, rows.Err()
}

func (s *Store) ListIncidents(ctx context.Context, status string, limit int) ([]*Incident, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT ` + incCols + ` FROM incidents`
	var args []any
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Incident{}
	for rows.Next() {
		i, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) CountFiringInIncident(ctx context.Context, incidentID string) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE incident_id = ? AND status = 'firing'`, incidentID).Scan(&n)
	return n
}

// ---------------------------------------------------------------- notifications

type Notification struct {
	ID         int64     `json:"id"`
	Time       time.Time `json:"time"`
	Severity   string    `json:"severity"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	ServerID   string    `json:"server_id"`
	Ref        string    `json:"ref"`
	Read       bool      `json:"read"`
	Deliveries string    `json:"deliveries,omitempty"`
}

func (s *Store) AddNotification(ctx context.Context, n *Notification) error {
	n.Time = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `INSERT INTO notifications(ts, severity, kind, title, body, server_id, ref, deliveries) VALUES (?,?,?,?,?,?,?,?)`,
		n.Time.UnixMilli(), n.Severity, n.Kind, n.Title, n.Body, n.ServerID, n.Ref, n.Deliveries)
	if err != nil {
		return err
	}
	n.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) SetNotificationDeliveries(ctx context.Context, id int64, d string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE notifications SET deliveries = ? WHERE id = ?`, d, id)
	return err
}

func (s *Store) ListNotifications(ctx context.Context, limit int) ([]Notification, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, ts, severity, kind, title, body, server_id, ref, read, deliveries FROM notifications ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		var ts int64
		var rd int
		if err := rows.Scan(&n.ID, &ts, &n.Severity, &n.Kind, &n.Title, &n.Body, &n.ServerID, &n.Ref, &rd, &n.Deliveries); err != nil {
			return nil, 0, err
		}
		n.Time, n.Read = ms(ts), rd == 1
		out = append(out, n)
	}
	var unread int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE read = 0`).Scan(&unread)
	return out, unread, rows.Err()
}

func (s *Store) MarkNotificationsRead(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE notifications SET read = 1 WHERE read = 0`)
	return err
}

// ---------------------------------------------------------------- policies

func (s *Store) GetPolicy(ctx context.Context, scope string, out any) (bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT json FROM policies WHERE scope = ?`, scope).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(v), out)
}

func (s *Store) PutPolicy(ctx context.Context, scope string, v any, by string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO policies(scope, json, updated_at, updated_by) VALUES (?,?,?,?)
		ON CONFLICT(scope) DO UPDATE SET json = excluded.json, updated_at = excluded.updated_at, updated_by = excluded.updated_by`, scope, toJSON(v), now(), by)
	return err
}

func (s *Store) DeletePolicy(ctx context.Context, scope string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM policies WHERE scope = ?`, scope)
	return err
}

func (s *Store) ListPolicyScopes(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT scope FROM policies ORDER BY scope`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sc string
		if err := rows.Scan(&sc); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- memory

type Memory struct {
	ID         string    `json:"id"`
	ServerID   string    `json:"server_id"`
	Kind       string    `json:"kind"`
	Content    string    `json:"content"`
	Source     string    `json:"source"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	ObservedAt time.Time `json:"observed_at"`
}

func (s *Store) PutMemory(ctx context.Context, m *Memory) error {
	n := now()
	if m.ID == "" {
		m.ID = NewID("mem")
		m.CreatedAt = ms(n)
	}
	if m.ObservedAt.IsZero() {
		m.ObservedAt = ms(n)
	}
	m.UpdatedAt = ms(n)
	_, err := s.db.ExecContext(ctx, `INSERT INTO memory(id, server_id, kind, content, source, created_at, updated_at, observed_at) VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET server_id = excluded.server_id, kind = excluded.kind, content = excluded.content, source = excluded.source,
		updated_at = excluded.updated_at, observed_at = excluded.observed_at`,
		m.ID, m.ServerID, m.Kind, m.Content, m.Source, m.CreatedAt.UnixMilli(), n, m.ObservedAt.UnixMilli())
	return err
}

func (s *Store) DeleteMemory(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM memory WHERE id = ?`, id)
	return err
}

// ListMemory returns global memory plus, if serverID is set, that server's.
func (s *Store) ListMemory(ctx context.Context, serverID string, all bool) ([]Memory, error) {
	q := `SELECT id, server_id, kind, content, source, created_at, updated_at, observed_at FROM memory`
	var args []any
	if !all {
		q += ` WHERE server_id = '' OR server_id = ?`
		args = append(args, serverID)
	}
	q += ` ORDER BY server_id, kind, updated_at DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		var m Memory
		var c, u, o int64
		if err := rows.Scan(&m.ID, &m.ServerID, &m.Kind, &m.Content, &m.Source, &c, &u, &o); err != nil {
			return nil, err
		}
		m.CreatedAt, m.UpdatedAt, m.ObservedAt = ms(c), ms(u), ms(o)
		out = append(out, m)
	}
	return out, rows.Err()
}
