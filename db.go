package s3

import (
	"database/sql"
	"errors"

	_ "github.com/mattn/go-sqlite3"
)

type objectDB struct {
	conn *sql.DB
}

func openObjectDB(path string) (*objectDB, error) {
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)

	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS buckets (
			name TEXT PRIMARY KEY
		)`,
		`CREATE TABLE IF NOT EXISTS objects (
			bucket_name TEXT NOT NULL,
			key TEXT NOT NULL,
			file_name TEXT NOT NULL UNIQUE,
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

func (db *objectDB) deleteEmptyBucket(name string) (bool, error) {
	result, err := db.conn.Exec(`DELETE FROM buckets WHERE name = ?
		AND NOT EXISTS (SELECT 1 FROM objects WHERE bucket_name = ?)`, name, name)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (db *objectDB) bucketExists(name string) (bool, error) {
	var found int
	err := db.conn.QueryRow(`SELECT 1 FROM buckets WHERE name = ?`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (db *objectDB) objectFile(bucket, key string) (string, bool, error) {
	var name string
	err := db.conn.QueryRow(`SELECT file_name FROM objects WHERE bucket_name = ? AND key = ?`, bucket, key).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return name, err == nil, err
}

func (db *objectDB) setObjectFile(bucket, key, name string) error {
	_, err := db.conn.Exec(`INSERT INTO objects(bucket_name, key, file_name) VALUES (?, ?, ?)
		ON CONFLICT(bucket_name, key) DO UPDATE SET file_name = excluded.file_name`, bucket, key, name)
	return err
}

func (db *objectDB) deleteObject(bucket, key string) error {
	_, err := db.conn.Exec(`DELETE FROM objects WHERE bucket_name = ? AND key = ?`, bucket, key)
	return err
}

func (db *objectDB) listObjects(bucket string) ([]string, error) {
	rows, err := db.conn.Query(`SELECT key FROM objects WHERE bucket_name = ?`, bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}
