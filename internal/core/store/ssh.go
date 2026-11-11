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
