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
	speed        INTEGER NOT NULL DEFAULT 2400,
	minutes_used INTEGER NOT NULL DEFAULT 0,
	minutes_day  TEXT    NOT NULL DEFAULT '',
	npc_turns    INTEGER NOT NULL DEFAULT 0,
	npc_day      TEXT    NOT NULL DEFAULT '',
	admin        INTEGER NOT NULL DEFAULT 0,
	heat         INTEGER NOT NULL DEFAULT 0,
	heat_at      INTEGER NOT NULL DEFAULT 0,
	lang         TEXT    NOT NULL DEFAULT 'nl',
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
	pending   INTEGER NOT NULL DEFAULT 0,
	posted_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS posts_board ON posts(board_id);
CREATE TABLE IF NOT EXISTS host_state (
	fingerprint    TEXT NOT NULL,
	host_id        TEXT NOT NULL,
	cracked        INTEGER NOT NULL DEFAULT 0,
	first_cracked  INTEGER NOT NULL DEFAULT 0,
	cooldown_until INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (fingerprint, host_id)
);
CREATE TABLE IF NOT EXISTS breaches (
	host_id TEXT NOT NULL,
	handle  TEXT NOT NULL,
	at      INTEGER NOT NULL,
	PRIMARY KEY (host_id, handle)
);
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
	// Idempotent column migrations for DBs created before the column existed.
	// CREATE TABLE IF NOT EXISTS never alters an existing table, so add new
	// columns explicitly and swallow the "duplicate column" error.
	for _, mig := range []string{
		`ALTER TABLE players ADD COLUMN admin INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE posts ADD COLUMN pending INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE players ADD COLUMN heat INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE players ADD COLUMN heat_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE players ADD COLUMN lang TEXT NOT NULL DEFAULT 'nl'`,
	} {
		if _, err := db.Exec(mig); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("migrate (%s): %w", mig, err)
		}
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

const playerCols = "fingerprint, handle, this_member, level, flags, banned, speed, minutes_used, minutes_day, admin, heat, heat_at, lang, created_at, last_seen"

func scanPlayer(row interface{ Scan(...any) error }) (*store.Player, error) {
	var p store.Player
	var handle sql.NullString
	var flagsJSON string
	var created, seen, heatAt int64
	err := row.Scan(&p.Fingerprint, &handle, &p.ThisMember, &p.Level, &flagsJSON,
		&p.Banned, &p.Speed, &p.MinutesUsed, &p.MinutesDay, &p.Admin, &p.Heat, &heatAt, &p.Lang, &created, &seen)
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
	if heatAt > 0 {
		p.HeatAt = time.Unix(heatAt, 0)
	}
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

// CountRegistered counts players who completed signup (chose a handle).
// Feeds the public website's stats endpoint; not part of store.Store.
func (s *Store) CountRegistered(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM players WHERE handle IS NOT NULL AND handle != ''").Scan(&n)
	return n, err
}

func (s *Store) SetBanned(ctx context.Context, fp string, banned bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE players SET banned = ? WHERE fingerprint = ?", banned, fp)
	return err
}

func (s *Store) SetLang(ctx context.Context, fp, lang string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE players SET lang = ? WHERE fingerprint = ?", lang, fp)
	return err
}

func (s *Store) SetAdmin(ctx context.Context, fp string, admin bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE players SET admin = ? WHERE fingerprint = ?", admin, fp)
	return err
}

func (s *Store) AddHeat(ctx context.Context, fp string, delta int) (int, error) {
	var raw int
	var at int64
	if err := s.db.QueryRowContext(ctx,
		"SELECT heat, heat_at FROM players WHERE fingerprint = ?", fp).Scan(&raw, &at); err != nil {
		return 0, err
	}
	now := time.Now()
	var since time.Time
	if at > 0 {
		since = time.Unix(at, 0)
	}
	v := store.DecayedHeat(raw, since, now) + delta
	if v < 0 {
		v = 0
	}
	if v > store.HeatMax {
		v = store.HeatMax
	}
	_, err := s.db.ExecContext(ctx,
		"UPDATE players SET heat = ?, heat_at = ? WHERE fingerprint = ?", v, now.Unix(), fp)
	return v, err
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

func (s *Store) AddNPCTurns(ctx context.Context, fp string, day string, n int) (int, error) {
	_, err := s.db.ExecContext(ctx, `
		UPDATE players SET
			npc_turns = CASE WHEN npc_day = ? THEN npc_turns + ? ELSE ? END,
			npc_day = ?
		WHERE fingerprint = ?`, day, n, n, day, fp)
	if err != nil {
		return 0, err
	}
	var used int
	err = s.db.QueryRowContext(ctx, "SELECT npc_turns FROM players WHERE fingerprint = ?", fp).Scan(&used)
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
		INSERT INTO posts (board_id, author, level, subject, body, reply_to, pending, posted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.BoardID, m.Author, m.Level, m.Subject, m.Body, m.ReplyTo, m.Pending, m.PostedAt.Unix())
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func (s *Store) PostsForBoard(ctx context.Context, boardID string) ([]store.SavedMessage, error) {
	// Only published posts are visible; drafts (pending=1) await sysop review.
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, board_id, author, level, subject, body, reply_to, posted_at
		FROM posts WHERE board_id = ? AND pending = 0 ORDER BY id`, boardID)
	if err != nil {
		return nil, err
	}
	return scanPosts(rows)
}

func (s *Store) PendingPosts(ctx context.Context) ([]store.SavedMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, board_id, author, level, subject, body, reply_to, posted_at
		FROM posts WHERE pending = 1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return scanPosts(rows)
}

// scanPosts drains a posts query with the shared column list.
func scanPosts(rows *sql.Rows) ([]store.SavedMessage, error) {
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

func (s *Store) DeletePost(ctx context.Context, boardID string, id int) (bool, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM posts WHERE board_id = ? AND id = ?", boardID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) PublishPost(ctx context.Context, id int) (bool, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE posts SET pending = 0 WHERE id = ? AND pending = 1", id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) DeletePendingPost(ctx context.Context, id int) (bool, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM posts WHERE id = ? AND pending = 1", id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) HostState(ctx context.Context, fp, hostID string) (store.HostState, error) {
	var hs store.HostState
	var until int64
	err := s.db.QueryRowContext(ctx, `
		SELECT cracked, first_cracked, cooldown_until FROM host_state
		WHERE fingerprint = ? AND host_id = ?`, fp, hostID).
		Scan(&hs.Cracked, &hs.FirstCracked, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return store.HostState{}, nil
	}
	if err != nil {
		return store.HostState{}, err
	}
	if until > 0 {
		hs.CooldownUntil = time.Unix(until, 0)
	}
	return hs, nil
}

func (s *Store) SetHostCracked(ctx context.Context, fp, hostID string, cracked bool) (bool, error) {
	prev, err := s.HostState(ctx, fp, hostID)
	if err != nil {
		return false, err
	}
	first := cracked && !prev.FirstCracked
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO host_state (fingerprint, host_id, cracked, first_cracked, cooldown_until)
		VALUES (?, ?, ?, ?, 0)
		ON CONFLICT(fingerprint, host_id) DO UPDATE SET
			cracked = excluded.cracked,
			first_cracked = MAX(first_cracked, excluded.first_cracked),
			cooldown_until = 0`,
		fp, hostID, cracked, cracked)
	return first, err
}

func (s *Store) SetHostCooldown(ctx context.Context, fp, hostID string, until time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO host_state (fingerprint, host_id, cracked, first_cracked, cooldown_until)
		VALUES (?, ?, 0, 0, ?)
		ON CONFLICT(fingerprint, host_id) DO UPDATE SET
			cracked = 0,
			cooldown_until = excluded.cooldown_until`,
		fp, hostID, until.Unix())
	return err
}

func (s *Store) ResetProgress(ctx context.Context, fp, handle string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"UPDATE players SET level = 0, flags = '[]', heat = 0, heat_at = 0 WHERE fingerprint = ?", fp); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM host_state WHERE fingerprint = ?", fp); err != nil {
		return err
	}
	if handle != "" {
		if _, err := tx.ExecContext(ctx, "DELETE FROM breaches WHERE handle = ?", handle); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RecordBreach(ctx context.Context, hostID, handle string, at time.Time) error {
	if handle == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO breaches (host_id, handle, at) VALUES (?, ?, ?)",
		hostID, handle, at.Unix())
	return err
}

func (s *Store) BreachInfo(ctx context.Context, hostID string) (store.BreachInfo, error) {
	var bi store.BreachInfo
	var handle sql.NullString
	var at sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       (SELECT handle FROM breaches WHERE host_id = ? ORDER BY at, handle LIMIT 1),
		       (SELECT MIN(at) FROM breaches WHERE host_id = ?)
		FROM breaches WHERE host_id = ?`, hostID, hostID, hostID).
		Scan(&bi.Count, &handle, &at)
	if err != nil {
		return store.BreachInfo{}, err
	}
	bi.FirstHandle = handle.String
	if at.Valid {
		bi.FirstAt = time.Unix(at.Int64, 0)
	}
	return bi, nil
}

func (s *Store) Leaderboard(ctx context.Context, limit int) ([]store.Notoriety, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.handle, p.level,
		       (SELECT COUNT(*) FROM breaches b WHERE b.handle = p.handle) AS breaches
		FROM players p
		WHERE p.this_member = 1 AND p.handle <> '' AND p.banned = 0
		ORDER BY p.level DESC, breaches DESC, p.handle ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Notoriety
	for rows.Next() {
		var n store.Notoriety
		if err := rows.Scan(&n.Handle, &n.Level, &n.Breaches); err != nil {
			return nil, err
		}
		out = append(out, n)
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
