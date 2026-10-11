// Package storage 保存配置；不保存 QQ 聊天正文或模型密钥明文。
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/xin-24/EchoCore/internal/settings"
)

type Settings struct{ db *sql.DB }

func OpenSettings(ctx context.Context, path string) (*Settings, error) {
	if path == "" {
		return nil, errors.New("配置数据库路径不能为空")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	// 拒绝已有符号链接，避免无意间覆盖其他文件的访问权限。
	if info, err := os.Lstat(abs); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, errors.New("配置数据库必须是普通文件")
	}
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	err = file.Chmod(0600)
	file.Close()
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	db, err := sql.Open("sqlite3", u.String()+"?_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Settings{db: db}
	if err := store.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Settings) Close() error { return s.db.Close() }

func (s *Settings) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY);`); err != nil {
		return err
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return errors.New("配置数据库版本高于当前程序，拒绝降级打开")
	}
	if version == 0 {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE settings (id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL, updated_at TEXT NOT NULL, value_json TEXT NOT NULL); INSERT INTO schema_migrations(version) VALUES (1);`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Settings) Load(ctx context.Context) (settings.Record, bool, error) {
	var r settings.Record
	var timestamp, payload string
	err := s.db.QueryRowContext(ctx, `SELECT revision, updated_at, value_json FROM settings WHERE id=1`).Scan(&r.Revision, &timestamp, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	r.UpdatedAt, err = time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return r, false, errors.New("配置数据库时间字段无效")
	}
	if json.Unmarshal([]byte(payload), &r.Values) != nil {
		return r, false, errors.New("配置数据库内容无效")
	}
	return r, true, nil
}

func (s *Settings) Initialize(ctx context.Context, values settings.Values) (settings.Record, error) {
	if err := settings.Validate(values); err != nil {
		return settings.Record{}, err
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return settings.Record{}, err
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO settings(id,revision,updated_at,value_json) VALUES(1,1,?,?)`, now.Format(time.RFC3339Nano), string(payload)); err != nil {
		return settings.Record{}, err
	}
	r, _, err := s.Load(ctx)
	return r, err
}

func (s *Settings) Commit(ctx context.Context, revision uint64, values settings.Values) (settings.Record, error) {
	if err := settings.Validate(values); err != nil {
		return settings.Record{}, err
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return settings.Record{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return settings.Record{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE settings SET revision=revision+1,updated_at=?,value_json=? WHERE id=1 AND revision=?`, now.Format(time.RFC3339Nano), string(payload), revision)
	if err != nil {
		return settings.Record{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return settings.Record{}, err
	}
	if count != 1 {
		return settings.Record{}, settings.ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return settings.Record{}, err
	}
	return settings.Record{Revision: revision + 1, UpdatedAt: now, Values: values}, nil
}
