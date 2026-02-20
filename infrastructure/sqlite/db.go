package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps split read/write Bun connections.
type DB struct {
	WriteSQL *sql.DB
	ReadSQL  *sql.DB
	W        *bun.DB
	R        *bun.DB
}

// OpenDB initializes sqlite handles for immediate writer tx and pooled reads.
func OpenDB(path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}

	writeDSN := fmt.Sprintf("file:%s?_foreign_keys=on&_busy_timeout=5000&_txlock=immediate", path)
	readDSN := fmt.Sprintf("file:%s?_foreign_keys=on&_busy_timeout=5000&mode=ro&_query_only=1", path)

	wsql, err := sql.Open("sqlite3", writeDSN)
	if err != nil {
		return nil, fmt.Errorf("open write db: %w", err)
	}
	wsql.SetMaxOpenConns(1)
	wsql.SetConnMaxLifetime(15 * time.Minute)

	rsql, err := sql.Open("sqlite3", readDSN)
	if err != nil {
		wsql.Close()
		return nil, fmt.Errorf("open read db: %w", err)
	}
	rsql.SetMaxOpenConns(8)
	rsql.SetConnMaxIdleTime(5 * time.Minute)
	rsql.SetConnMaxLifetime(15 * time.Minute)

	// If read-only mode fails because DB is new/missing, fallback to read-write for bootstrap.
	if err := rsql.Ping(); err != nil && strings.Contains(err.Error(), "unable to open database file") {
		rsql.Close()
		rsql, err = sql.Open("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on&_busy_timeout=5000&_query_only=1", path))
		if err != nil {
			wsql.Close()
			return nil, fmt.Errorf("open fallback read db: %w", err)
		}
	}

	if _, err := rsql.Exec("PRAGMA query_only = ON"); err != nil {
		wsql.Close()
		rsql.Close()
		return nil, fmt.Errorf("enable read query_only: %w", err)
	}

	db := &DB{
		WriteSQL: wsql,
		ReadSQL:  rsql,
		W:        bun.NewDB(wsql, sqlitedialect.New()),
		R:        bun.NewDB(rsql, sqlitedialect.New()),
	}
	return db, nil
}

// Close closes read and write handles.
func (db *DB) Close() error {
	if db == nil {
		return nil
	}
	var errs []error
	if db.W != nil {
		errs = appendErr(errs, db.W.Close())
	}
	if db.R != nil {
		errs = appendErr(errs, db.R.Close())
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func appendErr(errs []error, err error) []error {
	if err != nil {
		return append(errs, err)
	}
	return errs
}
