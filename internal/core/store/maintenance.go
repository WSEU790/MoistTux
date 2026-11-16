package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Maintenance jobs and runs are stored as JSON documents (their shape is
// owned by internal/core/maintenance).

func (s *Store) PutMaintenanceJob(ctx context.Context, id, serverID string, doc any) error {
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO maintenance_jobs(id, server_id, json, created_at) VALUES (?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET server_id = excluded.server_id, json = excluded.json`, id, serverID, string(b), now())
	return err
}

func (s *Store) MaintenanceJobs(ctx context.Context) ([][]byte, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT json FROM maintenance_jobs ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			return nil, err
		}
		out = append(out, []byte(j))
	}
	return out, rows.Err()
}

func (s *Store) MaintenanceJob(ctx context.Context, id string) ([]byte, error) {
	var j string
	err := s.db.QueryRowContext(ctx, `SELECT json FROM maintenance_jobs WHERE id = ?`, id).Scan(&j)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return []byte(j), err
}

func (s *Store) DeleteMaintenanceJob(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM maintenance_jobs WHERE id = ?`, id)
	return err
}

func (s *Store) PutMaintenanceRun(ctx context.Context, id, jobID, serverID string, started int64, status string, doc any) error {
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO maintenance_runs(id, job_id, server_id, started, status, json) VALUES (?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET status = excluded.status, json = excluded.json`, id, jobID, serverID, started, status, string(b))
	return err
}

// MaintenanceRuns returns the newest runs, optionally for one job.
func (s *Store) MaintenanceRuns(ctx context.Context, jobID string, limit int) ([][]byte, error) {
	q := `SELECT json FROM maintenance_runs ORDER BY started DESC LIMIT ?`
	args := []any{limit}
	if jobID != "" {
		q = `SELECT json FROM maintenance_runs WHERE job_id = ? ORDER BY started DESC LIMIT ?`
		args = []any{jobID, limit}
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			return nil, err
		}
		out = append(out, []byte(j))
	}
	return out, rows.Err()
}
