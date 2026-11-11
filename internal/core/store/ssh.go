package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SSHCredential is a saved SSH login for one server. Sealed holds the secret
// material encrypted by the platform protector (DPAPI on Windows); it is
// never returned by the API.
type SSHCredential struct {
	ServerID   string     `json:"server_id"`
	Host       string     `json:"host"`
	Port       int        `json:"port"`
	User       string     `json:"user"`
	AuthKind   string     `json:"auth_kind"` // password | key
	HostKey    string     `json:"host_key"`
	Sealed     string     `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func (s *Store) PutSSHCredential(ctx context.Context, c *SSHCredential) error {
	n := now()
	c.CreatedAt = ms(n)
	_, err := s.db.ExecContext(ctx, `INSERT INTO ssh_credentials(server_id, host, port, user, auth_kind, host_key, sealed, created_at) VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(server_id) DO UPDATE SET host = excluded.host, port = excluded.port, user = excluded.user, auth_kind = excluded.auth_kind,
		host_key = excluded.host_key, sealed = excluded.sealed, created_at = excluded.created_at, last_used_at = NULL`,
		c.ServerID, c.Host, c.Port, c.User, c.AuthKind, c.HostKey, c.Sealed, n)
	return err
}

func (s *Store) GetSSHCredential(ctx context.Context, serverID string) (*SSHCredential, error) {
	var c SSHCredential
	var cr int64
	var lu sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT server_id, host, port, user, auth_kind, host_key, sealed, created_at, last_used_at FROM ssh_credentials WHERE server_id = ?`, serverID).
		Scan(&c.ServerID, &c.Host, &c.Port, &c.User, &c.AuthKind, &c.HostKey, &c.Sealed, &cr, &lu)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.CreatedAt, c.LastUsedAt = ms(cr), msPtr(lu)
	return &c, nil
}

func (s *Store) TouchSSHCredential(ctx context.Context, serverID string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE ssh_credentials SET last_used_at = ? WHERE server_id = ?`, now(), serverID)
}

func (s *Store) DeleteSSHCredential(ctx context.Context, serverID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM ssh_credentials WHERE server_id = ?`, serverID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SSHTunnel describes how NEXUS reaches an agent that is not directly
// reachable: an SSH connection to Host:Port as User (host key pinned) that may
// only forward to 127.0.0.1:AgentPort on the server. SealedKey is the
// tunnel-only private key, sealed by the platform protector; AuthorizedKey is
// the restricted line installed in the user's authorized_keys.
type SSHTunnel struct {
	ServerID      string    `json:"server_id"`
	Host          string    `json:"host"`
	Port          int       `json:"port"`
	User          string    `json:"user"`
	HostKey       string    `json:"host_key"`
	AgentPort     int       `json:"agent_port"`
	AuthorizedKey string    `json:"authorized_key"`
	SealedKey     string    `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Store) PutSSHTunnel(ctx context.Context, t *SSHTunnel) error {
	n := now()
	t.CreatedAt = ms(n)
	_, err := s.db.ExecContext(ctx, `INSERT INTO ssh_tunnels(server_id, host, port, user, host_key, agent_port, authorized_key, sealed_key, created_at) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(server_id) DO UPDATE SET host = excluded.host, port = excluded.port, user = excluded.user, host_key = excluded.host_key,
		agent_port = excluded.agent_port, authorized_key = excluded.authorized_key, sealed_key = excluded.sealed_key, created_at = excluded.created_at`,
		t.ServerID, t.Host, t.Port, t.User, t.HostKey, t.AgentPort, t.AuthorizedKey, t.SealedKey, n)
	return err
}

func (s *Store) GetSSHTunnel(ctx context.Context, serverID string) (*SSHTunnel, error) {
	var t SSHTunnel
	var cr int64
	err := s.db.QueryRowContext(ctx, `SELECT server_id, host, port, user, host_key, agent_port, authorized_key, sealed_key, created_at FROM ssh_tunnels WHERE server_id = ?`, serverID).
		Scan(&t.ServerID, &t.Host, &t.Port, &t.User, &t.HostKey, &t.AgentPort, &t.AuthorizedKey, &t.SealedKey, &cr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.CreatedAt = ms(cr)
	return &t, nil
}
