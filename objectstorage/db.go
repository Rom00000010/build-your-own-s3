package objectstorage

import (
	"database/sql"
	"encoding/json"
	"errors"

	_ "github.com/mattn/go-sqlite3"
)

type objectDB struct {
	conn *sql.DB
}

type objectFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
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
			files TEXT NOT NULL,
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

func (db *objectDB) getObject(bucket, key string) ([]objectFile, int64, bool, error) {
	var data string
	var size int64
	err := db.conn.QueryRow(`SELECT files, size FROM objects WHERE bucket_name = ? AND key = ?`, bucket, key).Scan(&data, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	var files []objectFile
	if err := json.Unmarshal([]byte(data), &files); err != nil {
		return nil, 0, false, err
	}
	return files, size, true, nil
}

func (db *objectDB) setObject(bucket, key string, files []objectFile, size int64) error {
	data, err := json.Marshal(files)
	if err != nil {
		return err
	}
	_, err = db.conn.Exec(`INSERT INTO objects(bucket_name, key, files, size) VALUES (?, ?, ?, ?)
		ON CONFLICT(bucket_name, key) DO UPDATE SET files = excluded.files, size = excluded.size`, bucket, key, string(data), size)
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
