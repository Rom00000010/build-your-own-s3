package objectstorage

import (
	"database/sql"
	"errors"
	"net/url"

	_ "github.com/mattn/go-sqlite3"
)

type objectDB struct {
	conn *sql.DB
}

func openObjectDB(path string) (*objectDB, error) {
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: url.Values{
		"_busy_timeout": {"5000"},
		"_foreign_keys": {"on"},
		"_journal_mode": {"WAL"},
		"_txlock":       {"immediate"},
	}.Encode()}).String()
	conn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(4)
	conn.SetMaxIdleConns(4)

	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS buckets (
			name TEXT PRIMARY KEY
		)`,
		`CREATE TABLE IF NOT EXISTS objects (
			bucket_name TEXT NOT NULL,
			key TEXT NOT NULL,
			file_name TEXT NOT NULL UNIQUE,
			size INTEGER NOT NULL,
			PRIMARY KEY (bucket_name, key),
			FOREIGN KEY (bucket_name) REFERENCES buckets(name)
		)`,
	} {
		if _, err := conn.Exec(statement); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return &objectDB{conn: conn}, nil
}

func (db *objectDB) close() error {
	return db.conn.Close()
}

func (db *objectDB) createBucket(name string) (bool, error) {
	result, err := db.conn.Exec(`INSERT INTO buckets(name) VALUES (?) ON CONFLICT(name) DO NOTHING`, name)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (db *objectDB) listBuckets() ([]string, error) {
	rows, err := db.conn.Query(`SELECT name FROM buckets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func (db *objectDB) deleteEmptyBucket(name string) (deleted, exists bool, err error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`DELETE FROM buckets WHERE name = ?
		AND NOT EXISTS (SELECT 1 FROM objects WHERE bucket_name = ?)`, name, name)
	if err != nil {
		return false, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, false, err
	}
	if n == 0 {
		var found int
		err = tx.QueryRow(`SELECT 1 FROM buckets WHERE name = ?`, name).Scan(&found)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, false, err
		}
		exists = err == nil
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	return n == 1, exists, nil
}

func (db *objectDB) bucketExists(name string) (bool, error) {
	var found int
	err := db.conn.QueryRow(`SELECT 1 FROM buckets WHERE name = ?`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (db *objectDB) objectFile(bucket, key string) (string, int64, bool, bool, error) {
	var name sql.NullString
	var size sql.NullInt64
	err := db.conn.QueryRow(`SELECT o.file_name, o.size FROM buckets AS b
		LEFT JOIN objects AS o ON o.bucket_name = b.name AND o.key = ?
		WHERE b.name = ?`, key, bucket).Scan(&name, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, false, nil
	}
	if err != nil {
		return "", 0, false, false, err
	}
	return name.String, size.Int64, true, name.Valid, nil
}

func (db *objectDB) setObjectFile(bucket, key, name string, size int64) (string, bool, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	var old sql.NullString
	err = tx.QueryRow(`SELECT o.file_name FROM buckets AS b
		LEFT JOIN objects AS o ON o.bucket_name = b.name AND o.key = ?
		WHERE b.name = ?`, key, bucket).Scan(&old)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	_, err = tx.Exec(`INSERT INTO objects(bucket_name, key, file_name, size) VALUES (?, ?, ?, ?)
		ON CONFLICT(bucket_name, key) DO UPDATE SET file_name = excluded.file_name, size = excluded.size`, bucket, key, name, size)
	if err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return old.String, true, nil
}

func (db *objectDB) deleteObject(bucket, key string) (string, bool, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	var name sql.NullString
	err = tx.QueryRow(`SELECT o.file_name FROM buckets AS b
		LEFT JOIN objects AS o ON o.bucket_name = b.name AND o.key = ?
		WHERE b.name = ?`, key, bucket).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if name.Valid {
		if _, err := tx.Exec(`DELETE FROM objects WHERE bucket_name = ? AND key = ?`, bucket, key); err != nil {
			return "", false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return name.String, true, nil
}

func (db *objectDB) listObjects(bucket string) ([]string, bool, error) {
	rows, err := db.conn.Query(`SELECT o.key FROM buckets AS b
		LEFT JOIN objects AS o ON o.bucket_name = b.name
		WHERE b.name = ?`, bucket)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	keys := make([]string, 0)
	exists := false
	for rows.Next() {
		exists = true
		var key sql.NullString
		if err := rows.Scan(&key); err != nil {
			return nil, false, err
		}
		if key.Valid {
			keys = append(keys, key.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return keys, exists, nil
}
