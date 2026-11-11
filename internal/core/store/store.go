// Package store is the NEXUS local persistence layer (SQLite, WAL mode).
// Telemetry has its own retention, separate from configuration, AI history
// and the audit trail.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db      *sql.DB
	auditMu sync.Mutex
}

var ErrNotFound = errors.New("not found")

// Open opens (creating if needed) the database and applies migrations.
func Open(path string) (*Store, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

func NewID(prefix string) string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func now() int64 { return time.Now().UnixMilli() }

func ms(t int64) time.Time {
	if t == 0 {
		return time.Time{}
	}
	return time.UnixMilli(t).UTC()
}

func msPtr(t sql.NullInt64) *time.Time {
	if !t.Valid || t.Int64 == 0 {
		return nil
	}
	v := time.UnixMilli(t.Int64).UTC()
	return &v
}

func toJSON(v any) string {
	if v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// ---------------------------------------------------------------- migrations

var migrations = []string{
	// v1 — foundation
	`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL);

	CREATE TABLE servers (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		address TEXT NOT NULL,
		port INTEGER NOT NULL,
		agent_id TEXT NOT NULL DEFAULT '',
		agent_fingerprint TEXT NOT NULL,
		pending_fingerprint TEXT NOT NULL DEFAULT '',
		environment TEXT NOT NULL DEFAULT 'production',
		group_name TEXT NOT NULL DEFAULT '',
		tags TEXT NOT NULL DEFAULT '[]',
		notes TEXT NOT NULL DEFAULT '',
		monitoring_enabled INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		enrolled_at INTEGER,
		info_json TEXT NOT NULL DEFAULT '',
		info_at INTEGER,
		health_json TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'unknown',
		status_reason TEXT NOT NULL DEFAULT '',
		last_seen INTEGER,
		last_seq INTEGER NOT NULL DEFAULT 0,
		boot_id TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE telemetry (
		server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		ts INTEGER NOT NULL,
		cpu REAL, mem REAL, swap REAL, load1 REAL, load5 REAL, load15 REAL,
		disk_max REAL, rx_bps REAL, tx_bps REAL, read_bps REAL, write_bps REAL,
		iowait REAL, procs INTEGER, tcp INTEGER, temp_max REAL,
		PRIMARY KEY (server_id, ts)
	) WITHOUT ROWID;

	CREATE TABLE telemetry_5m (
		server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		ts INTEGER NOT NULL,
		cpu REAL, cpu_max REAL, mem REAL, mem_max REAL, swap REAL, load1 REAL, load5 REAL, load15 REAL,
		disk_max REAL, rx_bps REAL, tx_bps REAL, read_bps REAL, write_bps REAL,
		iowait REAL, procs INTEGER, tcp INTEGER, temp_max REAL, n INTEGER NOT NULL,
		PRIMARY KEY (server_id, ts)
	) WITHOUT ROWID;

	CREATE TABLE inventory (
		server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		kind TEXT NOT NULL,
		json TEXT NOT NULL,
		error TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (server_id, kind)
	);

	CREATE TABLE incidents (
		id TEXT PRIMARY KEY,
		server_id TEXT NOT NULL,
		title TEXT NOT NULL,
		severity TEXT NOT NULL,
		status TEXT NOT NULL,
		opened_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		resolved_at INTEGER,
		ai_status TEXT NOT NULL DEFAULT '',
		ai_report TEXT NOT NULL DEFAULT '',
		ai_conversation_id TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX incidents_status ON incidents(status, updated_at);

	CREATE TABLE alerts (
		id TEXT PRIMARY KEY,
		fingerprint TEXT NOT NULL,
		server_id TEXT NOT NULL,
		rule TEXT NOT NULL,
		severity TEXT NOT NULL,
		subject TEXT NOT NULL,
		title TEXT NOT NULL,
		detail TEXT NOT NULL,
		evidence TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		first_seen INTEGER NOT NULL,
		last_seen INTEGER NOT NULL,
		resolved_at INTEGER,
		acknowledged_at INTEGER,
		count INTEGER NOT NULL DEFAULT 1,
		incident_id TEXT NOT NULL DEFAULT '',
		notified_at INTEGER
	);
	CREATE INDEX alerts_fp ON alerts(fingerprint, status);
	CREATE INDEX alerts_status ON alerts(status, last_seen);

	CREATE TABLE conversations (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		server_id TEXT NOT NULL DEFAULT '',
		mode TEXT NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		origin TEXT NOT NULL DEFAULT 'operator',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		archived INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		tool_calls TEXT NOT NULL DEFAULT '',
		tool_name TEXT NOT NULL DEFAULT '',
		execution_id TEXT NOT NULL DEFAULT '',
		meta TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);
	CREATE INDEX messages_conv ON messages(conversation_id, id);

	CREATE TABLE executions (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL DEFAULT '',
		incident_id TEXT NOT NULL DEFAULT '',
		server_id TEXT NOT NULL,
		tool TEXT NOT NULL,
		args TEXT NOT NULL,
		risk TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		mode TEXT NOT NULL,
		decision TEXT NOT NULL,
		decision_reason TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		result TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		started_at INTEGER,
		finished_at INTEGER
	);
	CREATE INDEX executions_created ON executions(created_at);
	CREATE INDEX executions_status ON executions(status);

	CREATE TABLE approvals (
		id TEXT PRIMARY KEY,
		execution_id TEXT NOT NULL REFERENCES executions(id) ON DELETE CASCADE,
		server_id TEXT NOT NULL,
		tool TEXT NOT NULL,
		risk TEXT NOT NULL,
		summary TEXT NOT NULL,
		impact TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		requested_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		decided_at INTEGER,
		decided_by TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX approvals_status ON approvals(status, requested_at);

	CREATE TABLE audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts INTEGER NOT NULL,
		actor TEXT NOT NULL,
		action TEXT NOT NULL,
		target TEXT NOT NULL,
		detail TEXT NOT NULL,
		prev_hash TEXT NOT NULL,
		hash TEXT NOT NULL
	);

	CREATE TABLE memory (
		id TEXT PRIMARY KEY,
		server_id TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL,
		content TEXT NOT NULL,
		source TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		observed_at INTEGER NOT NULL
	);
	CREATE INDEX memory_server ON memory(server_id);

	CREATE TABLE policies (
		scope TEXT PRIMARY KEY,
		json TEXT NOT NULL,
		updated_at INTEGER NOT NULL,
		updated_by TEXT NOT NULL
	);

	CREATE TABLE notifications (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts INTEGER NOT NULL,
		severity TEXT NOT NULL,
		kind TEXT NOT NULL,
		title TEXT NOT NULL,
		body TEXT NOT NULL,
		server_id TEXT NOT NULL DEFAULT '',
		ref TEXT NOT NULL DEFAULT '',
		read INTEGER NOT NULL DEFAULT 0,
		deliveries TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX notifications_ts ON notifications(ts);`,

	// v2 — optional saved SSH logins (sealed with the platform protector)
	`CREATE TABLE ssh_credentials (
		server_id TEXT PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
		host TEXT NOT NULL,
		port INTEGER NOT NULL,
		user TEXT NOT NULL,
		auth_kind TEXT NOT NULL,
		host_key TEXT NOT NULL,
		sealed TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		last_used_at INTEGER
	)`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_version(version) VALUES (0)`); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("database schema v%d is newer than this NEXUS build (v%d); refusing to downgrade", v, len(migrations))
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, stmt := range splitSQL(migrations[i]) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func splitSQL(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ";") {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *Store) SchemaVersion() int {
	var v int
	_ = s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&v)
	return v
}

// ---------------------------------------------------------------- settings

func (s *Store) GetSetting(ctx context.Context, key string, out any) (bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(v), out)
}

func (s *Store) PutSetting(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings(key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, string(b), now())
	return err
}

// ---------------------------------------------------------------- audit

type AuditEntry struct {
	ID       int64           `json:"id"`
	Time     time.Time       `json:"time"`
	Actor    string          `json:"actor"`
	Action   string          `json:"action"`
	Target   string          `json:"target"`
	Detail   json.RawMessage `json:"detail"`
	Hash     string          `json:"hash"`
	PrevHash string          `json:"prev_hash"`
}

// Audit appends a hash-chained record. Each hash covers the previous hash, so
// edits or deletions in the middle of the log are detectable by VerifyAudit.
func (s *Store) Audit(ctx context.Context, actor, action, target string, detail any) error {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	var prev string
	err := s.db.QueryRowContext(ctx, `SELECT hash FROM audit ORDER BY id DESC LIMIT 1`).Scan(&prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	d := toJSON(detail)
	if d == "" {
		d = "{}"
	}
	ts := now()
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s|%s|%s|%s", prev, ts, actor, action, target, d)))
	_, err = s.db.ExecContext(ctx, `INSERT INTO audit(ts, actor, action, target, detail, prev_hash, hash) VALUES (?,?,?,?,?,?,?)`,
		ts, actor, action, target, d, prev, hex.EncodeToString(h[:]))
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit int, before int64, action string) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT id, ts, actor, action, target, detail, hash, prev_hash FROM audit WHERE 1=1`
	var args []any
	if before > 0 {
		q += ` AND id < ?`
		args = append(args, before)
	}
	if action != "" {
		q += ` AND action LIKE ?`
		args = append(args, action+"%")
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var ts int64
		var d string
		if err := rows.Scan(&e.ID, &ts, &e.Actor, &e.Action, &e.Target, &d, &e.Hash, &e.PrevHash); err != nil {
			return nil, err
		}
		e.Time, e.Detail = ms(ts), json.RawMessage(d)
		out = append(out, e)
	}
	return out, rows.Err()
}

// VerifyAudit recomputes the chain and returns the first broken record ID (0 = intact).
func (s *Store) VerifyAudit(ctx context.Context) (int64, int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, ts, actor, action, target, detail, prev_hash, hash FROM audit ORDER BY id`)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	prev := ""
	n := 0
	for rows.Next() {
		var id, ts int64
		var actor, action, target, detail, ph, h string
		if err := rows.Scan(&id, &ts, &actor, &action, &target, &detail, &ph, &h); err != nil {
			return 0, n, err
		}
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s|%s|%s|%s", prev, ts, actor, action, target, detail)))
		if ph != prev || hex.EncodeToString(sum[:]) != h {
			return id, n, nil
		}
		prev = h
		n++
	}
	return 0, n, rows.Err()
}
