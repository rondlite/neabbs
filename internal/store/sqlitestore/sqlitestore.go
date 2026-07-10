// Package sqlitestore implements store.Store on SQLite via modernc.org/sqlite
// (pure Go, no CGO). One DB file, WAL mode.
package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/rondlite/neabbs/internal/store"
)

const schema = `
CREATE TABLE IF NOT EXISTS players (
	fingerprint  TEXT PRIMARY KEY,
	handle       TEXT UNIQUE,
	this_member  INTEGER NOT NULL DEFAULT 0,
	level        INTEGER NOT NULL DEFAULT 0,
	flags        TEXT    NOT NULL DEFAULT '[]',
	banned       INTEGER NOT NULL DEFAULT 0,
	speed        INTEGER NOT NULL DEFAULT 1200,
	minutes_used INTEGER NOT NULL DEFAULT 0,
	minutes_day  TEXT    NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	last_seen    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS callers (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	handle TEXT NOT NULL,
	at     INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS last_read (
	fingerprint TEXT NOT NULL,
	board_id    TEXT NOT NULL,
	msg_id      INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (fingerprint, board_id)
);
CREATE TABLE IF NOT EXISTS posts (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	board_id  TEXT NOT NULL,
	author    TEXT NOT NULL,
	level     INTEGER NOT NULL DEFAULT 0,
	subject   TEXT NOT NULL,
	body      TEXT NOT NULL,
	reply_to  INTEGER NOT NULL DEFAULT 0,
	posted_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS posts_board ON posts(board_id);
`

// Store is the SQLite-backed store.Store.
type Store struct {
	db *sql.DB
}

var _ store.Store = (*Store)(nil)

// Open opens (creating if needed) the DB at path and applies the schema.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// modernc sqlite is single-writer; serialize access.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	// Player post IDs start at 10000 so they can never collide with
	// YAML-seeded message IDs (content lint caps those below 10000).
	if _, err := db.Exec(`INSERT INTO sqlite_sequence (name, seq) SELECT 'posts', 9999
		WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'posts')`); err != nil {
		db.Close()
		return nil, fmt.Errorf("seed post id sequence: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

const playerCols = "fingerprint, handle, this_member, level, flags, banned, speed, minutes_used, minutes_day, created_at, last_seen"

func scanPlayer(row interface{ Scan(...any) error }) (*store.Player, error) {
	var p store.Player
	var handle sql.NullString
	var flagsJSON string
	var created, seen int64
	err := row.Scan(&p.Fingerprint, &handle, &p.ThisMember, &p.Level, &flagsJSON,
		&p.Banned, &p.Speed, &p.MinutesUsed, &p.MinutesDay, &created, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Handle = handle.String
	var flags []string
	if err := json.Unmarshal([]byte(flagsJSON), &flags); err != nil {
		return nil, fmt.Errorf("flags for %s: %w", p.Fingerprint, err)
	}
	p.Flags = make(map[string]bool, len(flags))
	for _, f := range flags {
		p.Flags[f] = true
	}
	p.CreatedAt = time.Unix(created, 0)
	p.LastSeen = time.Unix(seen, 0)
	return &p, nil
}

func (s *Store) PlayerByFingerprint(ctx context.Context, fp string) (*store.Player, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+playerCols+" FROM players WHERE fingerprint = ?", fp)
	return scanPlayer(row)
}

func (s *Store) PlayerByHandle(ctx context.Context, handle string) (*store.Player, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+playerCols+" FROM players WHERE handle = ?", handle)
	return scanPlayer(row)
}

func (s *Store) CreatePlayer(ctx context.Context, fp string) (*store.Player, error) {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO players (fingerprint, created_at, last_seen) VALUES (?, ?, ?)", fp, now, now)
	if err != nil {
		return nil, err
	}
	return s.PlayerByFingerprint(ctx, fp)
}

func (s *Store) SetHandle(ctx context.Context, fp, handle string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE players SET handle = ? WHERE fingerprint = ?", handle, fp)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return store.ErrHandleTaken
	}
	return err
}

func (s *Store) TouchLastSeen(ctx context.Context, fp string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE players SET last_seen = ? WHERE fingerprint = ?", time.Now().Unix(), fp)
	return err
}

func (s *Store) SetBanned(ctx context.Context, fp string, banned bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE players SET banned = ? WHERE fingerprint = ?", banned, fp)
	return err
}

func (s *Store) SetLevel(ctx context.Context, fp string, level int) error {
	if level < 0 || level > 9 {
		return fmt.Errorf("level %d out of range 0-9", level)
	}
	_, err := s.db.ExecContext(ctx, "UPDATE players SET level = ? WHERE fingerprint = ?", level, fp)
	return err
}

func (s *Store) SetThisMember(ctx context.Context, fp string, member bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE players SET this_member = ? WHERE fingerprint = ?", member, fp)
	return err
}

func (s *Store) SetSpeed(ctx context.Context, fp string, speed int) error {
	_, err := s.db.ExecContext(ctx, "UPDATE players SET speed = ? WHERE fingerprint = ?", speed, fp)
	return err
}

func (s *Store) GrantFlags(ctx context.Context, fp string, flags ...string) error {
	if len(flags) == 0 {
		return nil
	}
	p, err := s.PlayerByFingerprint(ctx, fp)
	if err != nil {
		return err
	}
	for _, f := range flags {
		p.Flags[f] = true
	}
	all := make([]string, 0, len(p.Flags))
	for f := range p.Flags {
		all = append(all, f)
	}
	sort.Strings(all)
	buf, err := json.Marshal(all)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE players SET flags = ? WHERE fingerprint = ?", string(buf), fp)
	return err
}

func (s *Store) AddMinutes(ctx context.Context, fp string, day string, n int) (int, error) {
	_, err := s.db.ExecContext(ctx, `
		UPDATE players SET
			minutes_used = CASE WHEN minutes_day = ? THEN minutes_used + ? ELSE ? END,
			minutes_day = ?
		WHERE fingerprint = ?`, day, n, n, day, fp)
	if err != nil {
		return 0, err
	}
	var used int
	err = s.db.QueryRowContext(ctx, "SELECT minutes_used FROM players WHERE fingerprint = ?", fp).Scan(&used)
	return used, err
}

func (s *Store) RecordCall(ctx context.Context, handle string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO callers (handle, at) VALUES (?, ?)", handle, at.Unix())
	return err
}

func (s *Store) LastCallers(ctx context.Context, n int) ([]store.Caller, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT handle, at FROM callers ORDER BY id DESC LIMIT ?", n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Caller
	for rows.Next() {
		var c store.Caller
		var at int64
		if err := rows.Scan(&c.Handle, &at); err != nil {
			return nil, err
		}
		c.At = time.Unix(at, 0)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) LastRead(ctx context.Context, fp, boardID string) (int, error) {
	var id int
	err := s.db.QueryRowContext(ctx,
		"SELECT msg_id FROM last_read WHERE fingerprint = ? AND board_id = ?", fp, boardID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s *Store) SetLastRead(ctx context.Context, fp, boardID string, msgID int) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO last_read (fingerprint, board_id, msg_id) VALUES (?, ?, ?)
		ON CONFLICT(fingerprint, board_id) DO UPDATE SET msg_id = MAX(msg_id, excluded.msg_id)`,
		fp, boardID, msgID)
	return err
}

func (s *Store) SavePost(ctx context.Context, m *store.SavedMessage) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO posts (board_id, author, level, subject, body, reply_to, posted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.BoardID, m.Author, m.Level, m.Subject, m.Body, m.ReplyTo, m.PostedAt.Unix())
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func (s *Store) PostsForBoard(ctx context.Context, boardID string) ([]store.SavedMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, board_id, author, level, subject, body, reply_to, posted_at
		FROM posts WHERE board_id = ? ORDER BY id`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.SavedMessage
	for rows.Next() {
		var m store.SavedMessage
		var at int64
		if err := rows.Scan(&m.ID, &m.BoardID, &m.Author, &m.Level, &m.Subject, &m.Body, &m.ReplyTo, &at); err != nil {
			return nil, err
		}
		m.PostedAt = time.Unix(at, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) AllPlayers(ctx context.Context) ([]store.Player, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+playerCols+" FROM players ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Player
	for rows.Next() {
		p, err := scanPlayer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}
