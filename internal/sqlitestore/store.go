package sqlitestore

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/banshanhanfu/agentmail"
	_ "modernc.org/sqlite"
)

// Store implements agentmail.Store with SQLite.
type Store struct {
	db  *sql.DB
	mu  sync.Mutex
}

// New opens or creates the SQLite database and runs migrations.
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("sqlite migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS identities (
			email TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			token TEXT NOT NULL,
			sign_key TEXT NOT NULL DEFAULT '',
			domain TEXT NOT NULL DEFAULT '',
			active INTEGER NOT NULL DEFAULT 1,
			created INTEGER NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_identities_token ON identities(token)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			identity TEXT NOT NULL,
			folder TEXT NOT NULL DEFAULT 'inbox',
			from_addr TEXT NOT NULL DEFAULT '',
			to_addr TEXT NOT NULL DEFAULT '[]',
			cc TEXT NOT NULL DEFAULT '[]',
			subject TEXT NOT NULL DEFAULT '',
			body_text TEXT NOT NULL DEFAULT '',
			body_html TEXT NOT NULL DEFAULT '',
			flags TEXT NOT NULL DEFAULT '[]',
			message_id TEXT NOT NULL DEFAULT '',
			in_reply_to TEXT NOT NULL DEFAULT '',
			refs TEXT NOT NULL DEFAULT '[]',
			received_at INTEGER NOT NULL DEFAULT 0,
			sent_at INTEGER NOT NULL DEFAULT 0,
			size INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_identity_folder ON messages(identity, folder)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_identity_subj ON messages(identity, subject)`,
		`CREATE TABLE IF NOT EXISTS attachments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id INTEGER NOT NULL,
			filename TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT '',
			size INTEGER NOT NULL DEFAULT 0,
			path TEXT NOT NULL DEFAULT '',
			cid TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_attachments_msg ON attachments(message_id)`,
		`CREATE TABLE IF NOT EXISTS rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			identity TEXT NOT NULL,
			match_from TEXT NOT NULL DEFAULT '',
			match_to TEXT NOT NULL DEFAULT '',
			match_subj TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			action_val TEXT NOT NULL DEFAULT '',
			active INTEGER NOT NULL DEFAULT 1,
			priority INTEGER NOT NULL DEFAULT 0,
			created INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rules_identity ON rules(identity)`,
	}
	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// ── 身份 ──

func (s *Store) CreateIdentity(email, name, token, signKey, domain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		"INSERT INTO identities (email, name, token, sign_key, domain, active, created) VALUES (?, ?, ?, ?, ?, 1, ?)",
		email, name, token, signKey, domain, time.Now().Unix(),
	)
	return err
}

func (s *Store) GetIdentity(email string) (*agentmail.Identity, error) {
	row := s.db.QueryRow("SELECT email, name, token, sign_key, domain, active, created FROM identities WHERE email = ?", email)
	var id agentmail.Identity
	err := row.Scan(&id.Email, &id.Name, &id.Token, &id.SignKey, &id.Domain, &id.Active, &id.Created)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("identity not found")
	}
	return &id, err
}

func (s *Store) GetIdentityByToken(token string) (*agentmail.Identity, error) {
	row := s.db.QueryRow("SELECT email, name, token, sign_key, domain, active, created FROM identities WHERE token = ?", token)
	var id agentmail.Identity
	err := row.Scan(&id.Email, &id.Name, &id.Token, &id.SignKey, &id.Domain, &id.Active, &id.Created)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("identity not found")
	}
	return &id, err
}

func (s *Store) ListIdentities() ([]agentmail.Identity, error) {
	rows, err := s.db.Query("SELECT email, name, token, sign_key, domain, active, created FROM identities ORDER BY created DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []agentmail.Identity
	for rows.Next() {
		var id agentmail.Identity
		if err := rows.Scan(&id.Email, &id.Name, &id.Token, &id.SignKey, &id.Domain, &id.Active, &id.Created); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *Store) UpdateIdentity(email, name, token, signKey string) error {
	_, err := s.db.Exec("UPDATE identities SET name=?, token=?, sign_key=? WHERE email=?", name, token, signKey, email)
	return err
}

func (s *Store) DeleteIdentity(email string) error {
	_, err := s.db.Exec("DELETE FROM identities WHERE email=?", email)
	return err
}

// ── 邮件 ──

func jsonArr(vals []string) string {
	b, _ := json.Marshal(vals)
	return string(b)
}

func scanStrings(s string) []string {
	var v []string
	json.Unmarshal([]byte(s), &v)
	if v == nil {
		return []string{}
	}
	return v
}

func (s *Store) SaveMessage(msg *agentmail.Message) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(
		`INSERT INTO messages (identity, folder, from_addr, to_addr, cc, subject, body_text, body_html, flags, message_id, in_reply_to, refs, received_at, sent_at, size)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.Identity, msg.Folder, msg.From,
		jsonArr(msg.To), jsonArr(msg.Cc),
		msg.Subject, msg.BodyText, msg.BodyHTML,
		jsonArr(msg.Flags), msg.MessageID, msg.InReplyTo,
		jsonArr(msg.References), msg.ReceivedAt, msg.SentAt, msg.Size,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetMessage(id int64) (*agentmail.Message, error) {
	row := s.db.QueryRow(
		`SELECT id, identity, folder, from_addr, to_addr, cc, subject, body_text, body_html, flags, message_id, in_reply_to, refs, received_at, sent_at, size
		FROM messages WHERE id=?`, id)
	var msg agentmail.Message
	var toStr, ccStr, flagsStr, refsStr string
	err := row.Scan(&msg.ID, &msg.Identity, &msg.Folder, &msg.From,
		&toStr, &ccStr, &msg.Subject, &msg.BodyText, &msg.BodyHTML,
		&flagsStr, &msg.MessageID, &msg.InReplyTo, &refsStr,
		&msg.ReceivedAt, &msg.SentAt, &msg.Size)
	if err != nil {
		return nil, err
	}
	msg.To = scanStrings(toStr)
	msg.Cc = scanStrings(ccStr)
	msg.Flags = scanStrings(flagsStr)
	msg.References = scanStrings(refsStr)
	return &msg, nil
}

func (s *Store) ListMessages(identity, folder string, limit, offset int) ([]agentmail.Message, error) {
	rows, err := s.db.Query(
		`SELECT id, identity, folder, from_addr, to_addr, cc, subject, body_text, body_html, flags, message_id, in_reply_to, refs, received_at, sent_at, size
		FROM messages WHERE identity=? AND folder=? ORDER BY received_at DESC LIMIT ? OFFSET ?`,
		identity, folder, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *Store) SearchMessages(identity, query string, limit, offset int) ([]agentmail.Message, error) {
	q := "%" + query + "%"
	rows, err := s.db.Query(
		`SELECT id, identity, folder, from_addr, to_addr, cc, subject, body_text, body_html, flags, message_id, in_reply_to, refs, received_at, sent_at, size
		FROM messages WHERE identity=? AND (subject LIKE ? OR from_addr LIKE ? OR body_text LIKE ?)
		ORDER BY received_at DESC LIMIT ? OFFSET ?`,
		identity, q, q, q, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *Store) CountMessages(identity, folder string) (int, error) {
	var c int
	err := s.db.QueryRow("SELECT COUNT(*) FROM messages WHERE identity=? AND folder=?", identity, folder).Scan(&c)
	return c, err
}

func (s *Store) UpdateMessageFlags(id int64, flags []string) error {
	_, err := s.db.Exec("UPDATE messages SET flags=? WHERE id=?", jsonArr(flags), id)
	return err
}

func (s *Store) MoveMessage(id int64, folder string) error {
	_, err := s.db.Exec("UPDATE messages SET folder=? WHERE id=?", folder, id)
	return err
}

func (s *Store) DeleteMessage(id int64) error {
	_, err := s.db.Exec("DELETE FROM messages WHERE id=?", id)
	return err
}

// ── 附件 ──

func (s *Store) SaveAttachment(att *agentmail.Attachment) (int64, error) {
	res, err := s.db.Exec(
		"INSERT INTO attachments (message_id, filename, mime_type, size, path, cid) VALUES (?, ?, ?, ?, ?, ?)",
		att.MessageID, att.Filename, att.MimeType, att.Size, att.Path, att.CID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetAttachment(id int64) (*agentmail.Attachment, error) {
	row := s.db.QueryRow("SELECT id, message_id, filename, mime_type, size, path, cid FROM attachments WHERE id=?", id)
	var att agentmail.Attachment
	err := row.Scan(&att.ID, &att.MessageID, &att.Filename, &att.MimeType, &att.Size, &att.Path, &att.CID)
	return &att, err
}

func (s *Store) ListAttachments(messageID int64) ([]agentmail.Attachment, error) {
	rows, err := s.db.Query("SELECT id, message_id, filename, mime_type, size, path, cid FROM attachments WHERE message_id=?", messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var atts []agentmail.Attachment
	for rows.Next() {
		var att agentmail.Attachment
		if err := rows.Scan(&att.ID, &att.MessageID, &att.Filename, &att.MimeType, &att.Size, &att.Path, &att.CID); err != nil {
			return nil, err
		}
		atts = append(atts, att)
	}
	return atts, nil
}

// ── 规则 ──

func (s *Store) CreateRule(rule *agentmail.Rule) (int64, error) {
	res, err := s.db.Exec(
		"INSERT INTO rules (identity, match_from, match_to, match_subj, action, action_val, active, priority, created) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		rule.Identity, rule.MatchFrom, rule.MatchTo, rule.MatchSubj, rule.Action, rule.ActionVal, btoi(rule.Active), rule.Priority, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetRule(id int64) (*agentmail.Rule, error) {
	row := s.db.QueryRow("SELECT id, identity, match_from, match_to, match_subj, action, action_val, active, priority, created FROM rules WHERE id=?", id)
	var r agentmail.Rule
	var active int
	err := row.Scan(&r.ID, &r.Identity, &r.MatchFrom, &r.MatchTo, &r.MatchSubj, &r.Action, &r.ActionVal, &active, &r.Priority, &r.Created)
	r.Active = active == 1
	return &r, err
}

func (s *Store) ListRules(identity string) ([]agentmail.Rule, error) {
	rows, err := s.db.Query("SELECT id, identity, match_from, match_to, match_subj, action, action_val, active, priority, created FROM rules WHERE identity=? ORDER BY priority ASC, created DESC", identity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []agentmail.Rule
	for rows.Next() {
		var r agentmail.Rule
		var active int
		if err := rows.Scan(&r.ID, &r.Identity, &r.MatchFrom, &r.MatchTo, &r.MatchSubj, &r.Action, &r.ActionVal, &active, &r.Priority, &r.Created); err != nil {
			return nil, err
		}
		r.Active = active == 1
		rules = append(rules, r)
	}
	return rules, nil
}

func (s *Store) UpdateRule(rule *agentmail.Rule) error {
	_, err := s.db.Exec(
		"UPDATE rules SET match_from=?, match_to=?, match_subj=?, action=?, action_val=?, active=?, priority=? WHERE id=? AND identity=?",
		rule.MatchFrom, rule.MatchTo, rule.MatchSubj, rule.Action, rule.ActionVal, btoi(rule.Active), rule.Priority, rule.ID, rule.Identity)
	return err
}

func (s *Store) DeleteRule(id int64) error {
	_, err := s.db.Exec("DELETE FROM rules WHERE id=?", id)
	return err
}

func (s *Store) GetMatchingRules(identity, fromAddr, toAddr, subject string) ([]agentmail.Rule, error) {
	all, err := s.ListRules(identity)
	if err != nil {
		return nil, err
	}
	var matched []agentmail.Rule
	for _, r := range all {
		if !r.Active {
			continue
		}
		if r.MatchFrom != "" && !matchGlob(r.MatchFrom, fromAddr) {
			continue
		}
		if r.MatchSubj != "" && !matchGlob(r.MatchSubj, subject) {
			continue
		}
		matched = append(matched, r)
	}
	return matched, nil
}

// ── 工具 ──

func scanMessages(rows *sql.Rows) ([]agentmail.Message, error) {
	var msgs []agentmail.Message
	for rows.Next() {
		var msg agentmail.Message
		var toStr, ccStr, flagsStr, refsStr string
		if err := rows.Scan(&msg.ID, &msg.Identity, &msg.Folder, &msg.From,
			&toStr, &ccStr, &msg.Subject, &msg.BodyText, &msg.BodyHTML,
			&flagsStr, &msg.MessageID, &msg.InReplyTo, &refsStr,
			&msg.ReceivedAt, &msg.SentAt, &msg.Size); err != nil {
			return nil, err
		}
		msg.To = scanStrings(toStr)
		msg.Cc = scanStrings(ccStr)
		msg.Flags = scanStrings(flagsStr)
		msg.References = scanStrings(refsStr)
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func matchGlob(pattern, value string) bool {
	// 简单子串匹配，支持 * 通配符
	if pattern == "*" || pattern == "" {
		return true
	}
	if strings.Contains(pattern, "*") {
		parts := strings.Split(pattern, "*")
		idx := 0
		for _, part := range parts {
			if part == "" {
				continue
			}
			n := strings.Index(value[idx:], part)
			if n < 0 {
				return false
			}
			idx += n + len(part)
		}
		return true
	}
	return strings.Contains(value, pattern)
}

// 确保实现了 agentmail.Store
var _ agentmail.Store = (*Store)(nil)

var _ = log.Println // suppress unused
