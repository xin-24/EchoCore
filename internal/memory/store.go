package memory

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func openDB(ctx context.Context, path string) (*sql.DB, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return nil, ErrStorage
	}
	if e = os.MkdirAll(filepath.Dir(abs), 0700); e != nil {
		return nil, ErrStorage
	}
	// 数据库及已有旁路文件拒绝符号链接；不修改用户已有父目录权限。
	for _, p := range []string{abs, abs + "-journal", abs + "-wal", abs + "-shm"} {
		if i, e := os.Lstat(p); e == nil && (!i.Mode().IsRegular() || i.Mode()&os.ModeSymlink != 0) {
			return nil, ErrStorage
		}
	}
	f, e := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, ErrStorage
	}
	e = f.Chmod(0600)
	f.Close()
	if e != nil {
		return nil, ErrStorage
	}
	u := url.URL{Scheme: "file", Path: abs}
	db, e := sql.Open("sqlite3", u.String()+"?_busy_timeout=2000&_foreign_keys=on&_secure_delete=on")
	if e != nil {
		return nil, ErrStorage
	}
	db.SetMaxOpenConns(1)
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		db.Close()
		return nil, ErrStorage
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS memory_schema(version INTEGER NOT NULL);`)
	if e != nil {
		tx.Rollback()
		db.Close()
		return nil, ErrStorage
	}
	var v int
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM memory_schema`).Scan(&v); e != nil || v > 2 {
		tx.Rollback()
		db.Close()
		return nil, ErrStorage
	}
	if v == 0 {
		_, e = tx.ExecContext(ctx, `CREATE TABLE memories (
 id INTEGER PRIMARY KEY AUTOINCREMENT, platform TEXT NOT NULL, self_id TEXT NOT NULL, conversation_type TEXT NOT NULL, conversation_id TEXT NOT NULL,
 subject TEXT NOT NULL,kind TEXT NOT NULL,content TEXT NOT NULL,source_type TEXT NOT NULL,source_message_id TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'active',
 created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,expires_at INTEGER NOT NULL,revision INTEGER NOT NULL DEFAULT 1);
 CREATE INDEX memory_scope ON memories(platform,self_id,conversation_type,conversation_id);
 CREATE TABLE memory_receipts(platform TEXT NOT NULL,self_id TEXT NOT NULL,conversation_type TEXT NOT NULL,conversation_id TEXT NOT NULL,source_id TEXT NOT NULL,PRIMARY KEY(platform,self_id,conversation_type,conversation_id,source_id));
 CREATE TABLE memory_epochs(platform TEXT NOT NULL,self_id TEXT NOT NULL,conversation_type TEXT NOT NULL,conversation_id TEXT NOT NULL,epoch INTEGER NOT NULL,PRIMARY KEY(platform,self_id,conversation_type,conversation_id));
 INSERT INTO memory_schema VALUES(1);`)
	}
	if e == nil && v < 2 {
		_, e = tx.ExecContext(ctx, `ALTER TABLE memories ADD COLUMN topic TEXT NOT NULL DEFAULT '';
 ALTER TABLE memories ADD COLUMN origin TEXT NOT NULL DEFAULT 'manual';
 ALTER TABLE memories ADD COLUMN batch_id INTEGER NOT NULL DEFAULT 0;
 ALTER TABLE memories ADD COLUMN evidence TEXT NOT NULL DEFAULT '';
 CREATE TABLE memory_auto_state(platform TEXT NOT NULL,self_id TEXT NOT NULL,conversation_type TEXT NOT NULL,conversation_id TEXT NOT NULL,paused INTEGER NOT NULL DEFAULT 0,barrier INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(platform,self_id,conversation_type,conversation_id));
 CREATE TABLE memory_batches(id INTEGER PRIMARY KEY AUTOINCREMENT,platform TEXT NOT NULL,self_id TEXT NOT NULL,conversation_type TEXT NOT NULL,conversation_id TEXT NOT NULL,created_at INTEGER NOT NULL,status TEXT NOT NULL);
 CREATE TABLE memory_candidates(id INTEGER PRIMARY KEY AUTOINCREMENT,platform TEXT NOT NULL,self_id TEXT NOT NULL,conversation_type TEXT NOT NULL,conversation_id TEXT NOT NULL,subject TEXT NOT NULL,kind TEXT NOT NULL,topic TEXT NOT NULL,content TEXT NOT NULL,evidence TEXT NOT NULL,batch_id INTEGER NOT NULL,reason TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 1,expires_at INTEGER NOT NULL);
 CREATE INDEX candidate_scope ON memory_candidates(platform,self_id,conversation_type,conversation_id);
 CREATE TABLE memory_suppressions(id INTEGER PRIMARY KEY AUTOINCREMENT,platform TEXT NOT NULL,self_id TEXT NOT NULL,conversation_type TEXT NOT NULL,conversation_id TEXT NOT NULL,subject TEXT NOT NULL,topic TEXT NOT NULL,fingerprint TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 1);
 CREATE UNIQUE INDEX suppression_scope ON memory_suppressions(platform,self_id,conversation_type,conversation_id,subject,topic,fingerprint);
 UPDATE memory_schema SET version=2;`)
	}
	if e != nil {
		tx.Rollback()
		db.Close()
		return nil, ErrStorage
	}
	if tx.Commit() != nil {
		db.Close()
		return nil, ErrStorage
	}
	return db, nil
}

const columns = `id,platform,self_id,conversation_type,conversation_id,subject,kind,content,source_type,source_message_id,status,created_at,updated_at,expires_at,revision,topic,origin,batch_id,evidence`
const scopeWhere = `platform=? AND self_id=? AND conversation_type=? AND conversation_id=?`

func scopeArgs(s Scope) []any { return []any{s.Platform, s.SelfID, s.Type, s.ConversationID} }

type scanner interface{ Scan(...any) error }

func scanEntry(r scanner) (Entry, error) {
	var x Entry
	e := r.Scan(&x.ID, &x.Scope.Platform, &x.Scope.SelfID, &x.Scope.Type, &x.Scope.ConversationID, &x.Subject, &x.Kind, &x.Content, &x.SourceType, &x.SourceMessageID, &x.Status, &x.CreatedAt, &x.UpdatedAt, &x.ExpiresAt, &x.Revision, &x.Topic, &x.Origin, &x.BatchID, &x.Evidence)
	return x, e
}
func listDB(ctx context.Context, db *sql.DB, s Scope) ([]Entry, error) {
	return listEntries(ctx, db, s, true)
}

func listEntries(ctx context.Context, db *sql.DB, s Scope, activeOnly bool) ([]Entry, error) {
	args := scopeArgs(s)
	filter := ""
	if activeOnly {
		filter = " AND status='active' AND (expires_at=0 OR expires_at>?)"
		args = append(args, time.Now().Unix())
	}
	rows, e := db.QueryContext(ctx, `SELECT `+columns+` FROM memories WHERE `+scopeWhere+filter+` ORDER BY id DESC LIMIT 501`, args...)
	if e != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		x, e := scanEntry(rows)
		if e != nil {
			return nil, ErrStorage
		}
		out = append(out, x)
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return out, nil
}
func getTx(ctx context.Context, tx *sql.Tx, s Scope, id int64) (Entry, error) {
	x, e := scanEntry(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM memories WHERE `+scopeWhere+` AND id=?`, append(scopeArgs(s), id)...))
	if errors.Is(e, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	if e != nil {
		return Entry{}, ErrStorage
	}
	return x, nil
}
func bump(ctx context.Context, tx *sql.Tx, s Scope) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO memory_epochs VALUES(?,?,?,?,1) ON CONFLICT(platform,self_id,conversation_type,conversation_id) DO UPDATE SET epoch=epoch+1`, scopeArgs(s)...)
	return e
}
