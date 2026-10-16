package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const schema = `CREATE TABLE IF NOT EXISTS registry_containers(
 id TEXT PRIMARY KEY,host TEXT NOT NULL,pod TEXT NOT NULL,container TEXT NOT NULL,
 scope TEXT NOT NULL,networks_json TEXT NOT NULL CHECK(json_valid(networks_json)),
 listeners_json TEXT NOT NULL CHECK(json_valid(listeners_json)),note TEXT NOT NULL,updated_at TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS registry_host ON registry_containers(host);
 CREATE TABLE IF NOT EXISTS podport_meta(key TEXT PRIMARY KEY,value TEXT NOT NULL);`

type Store struct {
	db   *sql.DB
	path string
	mu   sync.Mutex
}
type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func openStore(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	// Create privately without changing permissions on an existing user database.
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db, path: abs}
	fail := func(err error) (*Store, error) { db.Close(); return nil, err }
	for _, p := range []string{"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=DELETE", "PRAGMA synchronous=FULL", "PRAGMA temp_store=MEMORY", "PRAGMA cache_size=-2048", "PRAGMA mmap_size=0", schema} {
		if _, err = db.Exec(p); err != nil {
			return fail(err)
		}
	}
	if err = s.migrateLegacy(); err != nil {
		return fail(fmt.Errorf("旧数据迁移失败，原表已保留：%w", err))
	}
	return s, nil
}

func loadEntries(ctx context.Context, q querier) ([]Entry, error) {
	rows, err := q.QueryContext(ctx, "SELECT id,host,pod,container,scope,networks_json,listeners_json,note,updated_at FROM registry_containers ORDER BY updated_at DESC,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var nets, listeners string
		if err = rows.Scan(&e.ID, &e.Host, &e.Pod, &e.Container, &e.Scope, &nets, &listeners, &e.Note, &e.UpdatedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(nets), &e.Networks); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(listeners), &e.Listeners); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) list() ([]Entry, error) { return loadEntries(context.Background(), s.db) }
func insertEntry(ctx context.Context, c *sql.Conn, e Entry) error {
	nets, err := json.Marshal(e.Networks)
	if err != nil {
		return err
	}
	listeners, err := json.Marshal(e.Listeners)
	if err != nil {
		return err
	}
	_, err = c.ExecContext(ctx, "INSERT INTO registry_containers VALUES(?,?,?,?,?,?,?,?,?)", e.ID, e.Host, e.Pod, e.Container, e.Scope, string(nets), string(listeners), e.Note, e.UpdatedAt)
	return err
}
func (s *Store) write(fn func(context.Context, *sql.Conn) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	if err = fn(ctx, conn); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

var errNotFound = fmt.Errorf("容器登记不存在")

func (s *Store) save(e Entry, edit bool) (Entry, error) {
	e, err := normalized(e)
	if err != nil {
		return e, err
	}
	if !edit {
		e.ID = newID()
	}
	e.UpdatedAt = timestamp()
	err = s.write(func(ctx context.Context, c *sql.Conn) error {
		all, err := loadEntries(ctx, c)
		if err != nil {
			return err
		}
		if edit {
			found := false
			for _, v := range all {
				if v.ID == e.ID {
					found = true
				}
			}
			if !found {
				return errNotFound
			}
		}
		if found := entryConflicts(e, all); len(found) > 0 {
			return &ConflictError{found}
		}
		out, err := mergedEntries(e, all)
		if err != nil {
			return err
		}
		// Only the edited container and shared Pod network fields are written.
		for _, v := range out {
			if v.ID != e.ID {
				if v.Scope == "pod" && e.Scope == "pod" && strings.EqualFold(v.Host, e.Host) && v.Pod == e.Pod {
					nets, _ := json.Marshal(v.Networks)
					if _, err = c.ExecContext(ctx, "UPDATE registry_containers SET networks_json=?,updated_at=? WHERE id=?", string(nets), v.UpdatedAt, v.ID); err != nil {
						return err
					}
				}
				continue
			}
			if edit {
				if _, err = c.ExecContext(ctx, "DELETE FROM registry_containers WHERE id=?", v.ID); err != nil {
					return err
				}
			}
			if err = insertEntry(ctx, c, v); err != nil {
				return err
			}
		}
		return nil
	})
	return e, err
}
func (s *Store) remove(id string) error {
	return s.write(func(ctx context.Context, c *sql.Conn) error {
		r, err := c.ExecContext(ctx, "DELETE FROM registry_containers WHERE id=?", id)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			return errNotFound
		}
		return nil
	})
}
func (s *Store) replace(entries []Entry) error {
	all, err := validateEntries(entries)
	if err != nil {
		return err
	}
	return s.write(func(ctx context.Context, c *sql.Conn) error {
		if _, err := c.ExecContext(ctx, "DELETE FROM registry_containers"); err != nil {
			return err
		}
		for _, e := range all {
			if err := insertEntry(ctx, c, e); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) backup() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.CreateTemp(filepath.Dir(s.path), ".podport-backup-*.db")
	if err != nil {
		return "", err
	}
	dest := file.Name()
	file.Close()
	if _, err = s.db.Exec("VACUUM INTO '" + strings.ReplaceAll(dest, "'", "''") + "'"); err != nil {
		os.Remove(dest)
		return "", err
	}
	return dest, nil
}

// Copy earlier schemas once, retaining their original tables for recovery.
func (s *Store) migrateLegacy() error {
	var done string
	err := s.db.QueryRow("SELECT value FROM podport_meta WHERE key='v3_migrated'").Scan(&done)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	var present int
	if err = s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='port_entries'").Scan(&present); err != nil {
		return err
	}
	data := map[string]any{}
	tables := []string{}
	if present > 0 {
		data["schema_version"] = 2
		tables = []string{"port_entries"}
	} else {
		if err = s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='listeners'").Scan(&present); err != nil {
			return err
		}
		if present > 0 {
			data["schema_version"] = 1
			tables = []string{"hosts", "pods", "containers", "listeners", "publications"}
		}
	}
	for _, table := range tables {
		rows, err := s.db.Query("SELECT * FROM " + table)
		if err != nil {
			return err
		}
		names, _ := rows.Columns()
		out := []map[string]any{}
		for rows.Next() {
			vals := make([]any, len(names))
			ptr := make([]any, len(names))
			for i := range vals {
				ptr[i] = &vals[i]
			}
			if err = rows.Scan(ptr...); err != nil {
				rows.Close()
				return err
			}
			row := map[string]any{}
			for i, n := range names {
				if b, ok := vals[i].([]byte); ok {
					row[n] = string(b)
				} else {
					row[n] = vals[i]
				}
			}
			out = append(out, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		key := table
		if key == "port_entries" {
			key = "entries"
		}
		data[key] = out
	}
	entries := []Entry{}
	if len(tables) > 0 {
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		entries, err = decodeSnapshot(raw)
		if err != nil {
			return err
		}
	}
	return s.write(func(ctx context.Context, c *sql.Conn) error {
		for _, e := range entries {
			if err := insertEntry(ctx, c, e); err != nil {
				return err
			}
		}
		_, err := c.ExecContext(ctx, "INSERT INTO podport_meta VALUES('v3_migrated','yes')")
		return err
	})
}
