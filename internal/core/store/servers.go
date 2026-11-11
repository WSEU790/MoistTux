package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

type Server struct {
	ID                 string               `json:"id"`
	Name               string               `json:"name"`
	Address            string               `json:"address"`
	Port               int                  `json:"port"`
	AgentID            string               `json:"agent_id"`
	AgentFingerprint   string               `json:"agent_fingerprint"`
	PendingFingerprint string               `json:"pending_fingerprint,omitempty"`
	Environment        string               `json:"environment"`
	Group              string               `json:"group"`
	Tags               []string             `json:"tags"`
	Notes              string               `json:"notes"`
	MonitoringEnabled  bool                 `json:"monitoring_enabled"`
	CreatedAt          time.Time            `json:"created_at"`
	EnrolledAt         *time.Time           `json:"enrolled_at,omitempty"`
	Info               *protocol.SystemInfo `json:"info,omitempty"`
	InfoAt             *time.Time           `json:"info_at,omitempty"`
	Health             *protocol.Health     `json:"health,omitempty"`
	Status             string               `json:"status"`
	StatusReason       string               `json:"status_reason"`
	LastSeen           *time.Time           `json:"last_seen,omitempty"`
	LastSeq            uint64               `json:"-"`
	BootID             string               `json:"boot_id"`
}

const serverCols = `id, name, address, port, agent_id, agent_fingerprint, pending_fingerprint, environment, group_name, tags, notes,
	monitoring_enabled, created_at, enrolled_at, info_json, info_at, health_json, status, status_reason, last_seen, last_seq, boot_id`

func scanServer(sc interface{ Scan(...any) error }) (*Server, error) {
	var s Server
	var tags, info, health string
	var created int64
	var enrolled, infoAt, lastSeen sql.NullInt64
	var mon int
	if err := sc.Scan(&s.ID, &s.Name, &s.Address, &s.Port, &s.AgentID, &s.AgentFingerprint, &s.PendingFingerprint, &s.Environment, &s.Group, &tags, &s.Notes,
		&mon, &created, &enrolled, &info, &infoAt, &health, &s.Status, &s.StatusReason, &lastSeen, &s.LastSeq, &s.BootID); err != nil {
		return nil, err
	}
	s.MonitoringEnabled = mon == 1
	s.CreatedAt = ms(created)
	s.EnrolledAt, s.InfoAt, s.LastSeen = msPtr(enrolled), msPtr(infoAt), msPtr(lastSeen)
	_ = json.Unmarshal([]byte(tags), &s.Tags)
	if s.Tags == nil {
		s.Tags = []string{}
	}
	if info != "" {
		s.Info = &protocol.SystemInfo{}
		_ = json.Unmarshal([]byte(info), s.Info)
	}
	if health != "" {
		s.Health = &protocol.Health{}
		_ = json.Unmarshal([]byte(health), s.Health)
	}
	return &s, nil
}

func (s *Store) CreateServer(ctx context.Context, srv *Server) error {
	if srv.ID == "" {
		srv.ID = NewID("srv")
	}
	if srv.Tags == nil {
		srv.Tags = []string{}
	}
	if srv.Environment == "" {
		srv.Environment = "production"
	}
	srv.CreatedAt = time.Now().UTC()
	var enrolled any
	if srv.EnrolledAt != nil {
		enrolled = srv.EnrolledAt.UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO servers(id, name, address, port, agent_id, agent_fingerprint, environment, group_name, tags, notes, monitoring_enabled, created_at, enrolled_at, status)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		srv.ID, srv.Name, srv.Address, srv.Port, srv.AgentID, srv.AgentFingerprint, srv.Environment, srv.Group, toJSON(srv.Tags), srv.Notes, boolInt(srv.MonitoringEnabled), srv.CreatedAt.UnixMilli(), enrolled, "connecting")
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) GetServer(ctx context.Context, id string) (*Server, error) {
	srv, err := scanServer(s.db.QueryRowContext(ctx, `SELECT `+serverCols+` FROM servers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return srv, err
}

func (s *Store) ListServers(ctx context.Context) ([]*Server, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+serverCols+` FROM servers ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Server{}
	for rows.Next() {
		srv, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

type ServerPatch struct {
	Name              *string   `json:"name"`
	Address           *string   `json:"address"`
	Port              *int      `json:"port"`
	Environment       *string   `json:"environment"`
	Group             *string   `json:"group"`
	Tags              *[]string `json:"tags"`
	Notes             *string   `json:"notes"`
	MonitoringEnabled *bool     `json:"monitoring_enabled"`
}

func (s *Store) PatchServer(ctx context.Context, id string, p ServerPatch) error {
	set := func(col string, v any) error {
		res, err := s.db.ExecContext(ctx, `UPDATE servers SET `+col+` = ? WHERE id = ?`, v, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	}
	if p.Name != nil {
		if err := set("name", *p.Name); err != nil {
			return err
		}
	}
	if p.Address != nil {
		if err := set("address", *p.Address); err != nil {
			return err
		}
	}
	if p.Port != nil {
		if err := set("port", *p.Port); err != nil {
			return err
		}
	}
	if p.Environment != nil {
		if err := set("environment", *p.Environment); err != nil {
			return err
		}
	}
	if p.Group != nil {
		if err := set("group_name", *p.Group); err != nil {
			return err
		}
	}
	if p.Tags != nil {
		if err := set("tags", toJSON(*p.Tags)); err != nil {
			return err
		}
	}
	if p.Notes != nil {
		if err := set("notes", *p.Notes); err != nil {
			return err
		}
	}
	if p.MonitoringEnabled != nil {
		if err := set("monitoring_enabled", boolInt(*p.MonitoringEnabled)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteServer(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id)
	return err
}

func (s *Store) SetServerStatus(ctx context.Context, id, status, reason string, seen bool) error {
	if seen {
		_, err := s.db.ExecContext(ctx, `UPDATE servers SET status = ?, status_reason = ?, last_seen = ? WHERE id = ?`, status, reason, now(), id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE servers SET status = ?, status_reason = ? WHERE id = ?`, status, reason, id)
	return err
}

func (s *Store) SetServerInfo(ctx context.Context, id string, info *protocol.SystemInfo) error {
	_, err := s.db.ExecContext(ctx, `UPDATE servers SET info_json = ?, info_at = ? WHERE id = ?`, toJSON(info), now(), id)
	return err
}

func (s *Store) SetServerHealth(ctx context.Context, id string, h *protocol.Health) error {
	_, err := s.db.ExecContext(ctx, `UPDATE servers SET health_json = ?, boot_id = ?, agent_id = ? WHERE id = ?`, toJSON(h), h.BootID, h.AgentID, id)
	return err
}

func (s *Store) SetLastSeq(ctx context.Context, id string, seq uint64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE servers SET last_seq = ? WHERE id = ?`, seq, id)
	return err
}

func (s *Store) SetFingerprints(ctx context.Context, id, current, pending string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE servers SET agent_fingerprint = ?, pending_fingerprint = ? WHERE id = ?`, current, pending, id)
	return err
}

// ---------------------------------------------------------------- inventory

type Inventory struct {
	Kind      string          `json:"kind"`
	JSON      json.RawMessage `json:"data"`
	Error     string          `json:"error,omitempty"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (s *Store) PutInventory(ctx context.Context, serverID, kind string, v any, errMsg string) error {
	data := "null"
	if v != nil {
		data = toJSON(v)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO inventory(server_id, kind, json, error, updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT(server_id, kind) DO UPDATE SET json = CASE WHEN excluded.error = '' THEN excluded.json ELSE inventory.json END,
		error = excluded.error, updated_at = excluded.updated_at`, serverID, kind, data, errMsg, now())
	return err
}

func (s *Store) GetInventory(ctx context.Context, serverID, kind string, out any) (*Inventory, error) {
	var inv Inventory
	var data string
	var ts int64
	err := s.db.QueryRowContext(ctx, `SELECT kind, json, error, updated_at FROM inventory WHERE server_id = ? AND kind = ?`, serverID, kind).Scan(&inv.Kind, &data, &inv.Error, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	inv.JSON, inv.UpdatedAt = json.RawMessage(data), ms(ts)
	if out != nil && data != "null" {
		if err := json.Unmarshal([]byte(data), out); err != nil {
			return nil, err
		}
	}
	return &inv, nil
}
