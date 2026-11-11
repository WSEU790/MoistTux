package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ---------------------------------------------------------------- conversations

type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	ServerID  string    `json:"server_id"`
	Mode      string    `json:"mode"`
	Model     string    `json:"model"`
	Origin    string    `json:"origin"` // operator | monitor
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Archived  bool      `json:"archived"`
}

type Message struct {
	ID             int64           `json:"id"`
	ConversationID string          `json:"conversation_id"`
	Role           string          `json:"role"` // user | assistant | tool | event
	Content        string          `json:"content"`
	ToolCalls      json.RawMessage `json:"tool_calls,omitempty"`
	ToolName       string          `json:"tool_name,omitempty"`
	ExecutionID    string          `json:"execution_id,omitempty"`
	Meta           json.RawMessage `json:"meta,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

func (s *Store) CreateConversation(ctx context.Context, c *Conversation) error {
	if c.ID == "" {
		c.ID = NewID("cnv")
	}
	n := now()
	c.CreatedAt, c.UpdatedAt = ms(n), ms(n)
	if c.Origin == "" {
		c.Origin = "operator"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO conversations(id, title, server_id, mode, model, origin, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
		c.ID, c.Title, c.ServerID, c.Mode, c.Model, c.Origin, n, n)
	return err
}

func (s *Store) UpdateConversation(ctx context.Context, c *Conversation) error {
	_, err := s.db.ExecContext(ctx, `UPDATE conversations SET title = ?, server_id = ?, mode = ?, model = ?, archived = ?, updated_at = ? WHERE id = ?`,
		c.Title, c.ServerID, c.Mode, c.Model, boolInt(c.Archived), now(), c.ID)
	return err
}

func (s *Store) GetConversation(ctx context.Context, id string) (*Conversation, error) {
	var c Conversation
	var cr, up int64
	var ar int
	err := s.db.QueryRowContext(ctx, `SELECT id, title, server_id, mode, model, origin, created_at, updated_at, archived FROM conversations WHERE id = ?`, id).
		Scan(&c.ID, &c.Title, &c.ServerID, &c.Mode, &c.Model, &c.Origin, &cr, &up, &ar)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.CreatedAt, c.UpdatedAt, c.Archived = ms(cr), ms(up), ar == 1
	return &c, nil
}

func (s *Store) ListConversations(ctx context.Context, limit int) ([]Conversation, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, server_id, mode, model, origin, created_at, updated_at, archived FROM conversations WHERE archived = 0 ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Conversation{}
	for rows.Next() {
		var c Conversation
		var cr, up int64
		var ar int
		if err := rows.Scan(&c.ID, &c.Title, &c.ServerID, &c.Mode, &c.Model, &c.Origin, &cr, &up, &ar); err != nil {
			return nil, err
		}
		c.CreatedAt, c.UpdatedAt, c.Archived = ms(cr), ms(up), ar == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteConversation(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, id)
	return err
}

func (s *Store) AddMessage(ctx context.Context, m *Message) error {
	n := now()
	m.CreatedAt = ms(n)
	res, err := s.db.ExecContext(ctx, `INSERT INTO messages(conversation_id, role, content, tool_calls, tool_name, execution_id, meta, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		m.ConversationID, m.Role, m.Content, string(m.ToolCalls), m.ToolName, m.ExecutionID, string(m.Meta), n)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	_, _ = s.db.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, n, m.ConversationID)
	return nil
}

func (s *Store) UpdateMessage(ctx context.Context, m *Message) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET content = ?, tool_calls = ?, meta = ? WHERE id = ?`, m.Content, string(m.ToolCalls), string(m.Meta), m.ID)
	return err
}

func (s *Store) ListMessages(ctx context.Context, convID string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, conversation_id, role, content, tool_calls, tool_name, execution_id, meta, created_at FROM messages WHERE conversation_id = ? ORDER BY id`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var tc, meta string
		var cr int64
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &tc, &m.ToolName, &m.ExecutionID, &meta, &cr); err != nil {
			return nil, err
		}
		if tc != "" {
			m.ToolCalls = json.RawMessage(tc)
		}
		if meta != "" {
			m.Meta = json.RawMessage(meta)
		}
		m.CreatedAt = ms(cr)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- executions

type Execution struct {
	ID             string          `json:"id"`
	ConversationID string          `json:"conversation_id"`
	IncidentID     string          `json:"incident_id"`
	ServerID       string          `json:"server_id"`
	Tool           string          `json:"tool"`
	Args           json.RawMessage `json:"args"`
	Risk           string          `json:"risk"`
	RequestedBy    string          `json:"requested_by"` // ai | operator | automation
	Mode           string          `json:"mode"`
	Decision       string          `json:"decision"` // allow | approve | deny
	DecisionReason string          `json:"decision_reason"`
	Status         string          `json:"status"` // pending_approval | running | succeeded | failed | denied | blocked | expired
	Result         json.RawMessage `json:"result,omitempty"`
	Error          string          `json:"error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
}

const execCols = `id, conversation_id, incident_id, server_id, tool, args, risk, requested_by, mode, decision, decision_reason, status, result, error, created_at, started_at, finished_at`

func scanExec(sc interface{ Scan(...any) error }) (*Execution, error) {
	var e Execution
	var args, res string
	var cr int64
	var st, fi sql.NullInt64
	if err := sc.Scan(&e.ID, &e.ConversationID, &e.IncidentID, &e.ServerID, &e.Tool, &args, &e.Risk, &e.RequestedBy, &e.Mode, &e.Decision, &e.DecisionReason, &e.Status, &res, &e.Error, &cr, &st, &fi); err != nil {
		return nil, err
	}
	e.Args = json.RawMessage(args)
	if res != "" {
		e.Result = json.RawMessage(res)
	}
	e.CreatedAt, e.StartedAt, e.FinishedAt = ms(cr), msPtr(st), msPtr(fi)
	return &e, nil
}

func (s *Store) CreateExecution(ctx context.Context, e *Execution) error {
	if e.ID == "" {
		e.ID = NewID("exe")
	}
	n := now()
	e.CreatedAt = ms(n)
	if len(e.Args) == 0 {
		e.Args = json.RawMessage("{}")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO executions(`+execCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,NULL)`,
		e.ID, e.ConversationID, e.IncidentID, e.ServerID, e.Tool, string(e.Args), e.Risk, e.RequestedBy, e.Mode, e.Decision, e.DecisionReason, e.Status, string(e.Result), e.Error, n)
	return err
}

func (s *Store) StartExecution(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE executions SET status = 'running', started_at = ? WHERE id = ?`, now(), id)
	return err
}

func (s *Store) FinishExecution(ctx context.Context, id, status string, result any, errMsg string) error {
	r := ""
	if result != nil {
		if raw, ok := result.(json.RawMessage); ok {
			r = string(raw)
		} else {
			r = toJSON(result)
		}
	}
	_, err := s.db.ExecContext(ctx, `UPDATE executions SET status = ?, result = ?, error = ?, finished_at = ? WHERE id = ?`, status, r, errMsg, now(), id)
	return err
}

func (s *Store) SetExecutionStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE executions SET status = ? WHERE id = ?`, status, id)
	return err
}

func (s *Store) GetExecution(ctx context.Context, id string) (*Execution, error) {
	e, err := scanExec(s.db.QueryRowContext(ctx, `SELECT `+execCols+` FROM executions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

func (s *Store) ListExecutions(ctx context.Context, serverID, conversationID string, limit int) ([]*Execution, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT ` + execCols + ` FROM executions WHERE 1=1`
	var args []any
	if serverID != "" {
		q += ` AND server_id = ?`
		args = append(args, serverID)
	}
	if conversationID != "" {
		q += ` AND conversation_id = ?`
		args = append(args, conversationID)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Execution{}
	for rows.Next() {
		e, err := scanExec(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RecoverInterrupted marks executions that were running when the process
// stopped. Their outcome is unknown, which is recorded explicitly.
func (s *Store) RecoverInterrupted(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE executions SET status = 'failed', error = 'NEXUS stopped while this operation was running; outcome unknown — verify on the server', finished_at = ? WHERE status = 'running'`, now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---------------------------------------------------------------- approvals

type Approval struct {
	ID          string          `json:"id"`
	ExecutionID string          `json:"execution_id"`
	ServerID    string          `json:"server_id"`
	Tool        string          `json:"tool"`
	Risk        string          `json:"risk"`
	Summary     string          `json:"summary"`
	Impact      json.RawMessage `json:"impact,omitempty"`
	Status      string          `json:"status"` // pending | approved | denied | expired
	RequestedAt time.Time       `json:"requested_at"`
	ExpiresAt   time.Time       `json:"expires_at"`
	DecidedAt   *time.Time      `json:"decided_at,omitempty"`
	DecidedBy   string          `json:"decided_by,omitempty"`
	Reason      string          `json:"reason,omitempty"`
	Execution   *Execution      `json:"execution,omitempty"`
}

const apprCols = `id, execution_id, server_id, tool, risk, summary, impact, status, requested_at, expires_at, decided_at, decided_by, reason`

func scanApproval(sc interface{ Scan(...any) error }) (*Approval, error) {
	var a Approval
	var imp string
	var rq, ex int64
	var dc sql.NullInt64
	if err := sc.Scan(&a.ID, &a.ExecutionID, &a.ServerID, &a.Tool, &a.Risk, &a.Summary, &imp, &a.Status, &rq, &ex, &dc, &a.DecidedBy, &a.Reason); err != nil {
		return nil, err
	}
	if imp != "" {
		a.Impact = json.RawMessage(imp)
	}
	a.RequestedAt, a.ExpiresAt, a.DecidedAt = ms(rq), ms(ex), msPtr(dc)
	return &a, nil
}

func (s *Store) CreateApproval(ctx context.Context, a *Approval) error {
	if a.ID == "" {
		a.ID = NewID("apr")
	}
	n := now()
	a.RequestedAt, a.Status = ms(n), "pending"
	_, err := s.db.ExecContext(ctx, `INSERT INTO approvals(`+apprCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,NULL,'','')`,
		a.ID, a.ExecutionID, a.ServerID, a.Tool, a.Risk, a.Summary, string(a.Impact), a.Status, n, a.ExpiresAt.UnixMilli())
	return err
}

// DecideApproval atomically moves a pending approval to approved/denied.
func (s *Store) DecideApproval(ctx context.Context, id, status, by, reason string) (*Approval, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE approvals SET status = ?, decided_at = ?, decided_by = ?, reason = ? WHERE id = ? AND status = 'pending' AND expires_at > ?`,
		status, now(), by, reason, id, now())
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errors.New("approval is not pending (already decided or expired)")
	}
	return s.GetApproval(ctx, id)
}

func (s *Store) GetApproval(ctx context.Context, id string) (*Approval, error) {
	a, err := scanApproval(s.db.QueryRowContext(ctx, `SELECT `+apprCols+` FROM approvals WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Execution, _ = s.GetExecution(ctx, a.ExecutionID)
	return a, nil
}

func (s *Store) ListApprovals(ctx context.Context, status string, limit int) ([]*Approval, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT ` + apprCols + ` FROM approvals`
	var args []any
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY requested_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Approval{}
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	rows.Close()
	for _, a := range out {
		a.Execution, _ = s.GetExecution(ctx, a.ExecutionID)
	}
	return out, nil
}

// ExpireApprovals marks overdue approvals and their executions as expired.
func (s *Store) ExpireApprovals(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, execution_id FROM approvals WHERE status = 'pending' AND expires_at <= ?`, now())
	if err != nil {
		return nil, err
	}
	type pair struct{ a, e string }
	var ps []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.a, &p.e); err == nil {
			ps = append(ps, p)
		}
	}
	rows.Close()
	var ids []string
	for _, p := range ps {
		_, _ = s.db.ExecContext(ctx, `UPDATE approvals SET status = 'expired', decided_at = ? WHERE id = ?`, now(), p.a)
		_, _ = s.db.ExecContext(ctx, `UPDATE executions SET status = 'expired', finished_at = ? WHERE id = ? AND status = 'pending_approval'`, now(), p.e)
		ids = append(ids, p.a)
	}
	return ids, nil
}
