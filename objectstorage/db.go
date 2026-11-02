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

type uploadPart struct {
	PartNumber int
	File       objectFile
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
		`CREATE TABLE IF NOT EXISTS multipart_uploads (
			upload_id TEXT PRIMARY KEY,
			bucket_name TEXT NOT NULL,
			key TEXT NOT NULL,
			total_parts INTEGER NOT NULL,
			FOREIGN KEY (bucket_name) REFERENCES buckets(name) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS upload_parts (
			upload_id TEXT NOT NULL,
			part_number INTEGER NOT NULL,
			file_name TEXT NOT NULL UNIQUE,
			size INTEGER NOT NULL,
			PRIMARY KEY (upload_id, part_number),
			FOREIGN KEY (upload_id) REFERENCES multipart_uploads(upload_id) ON DELETE CASCADE
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

func (db *objectDB) createMultipartUpload(bucket, key, uploadID string, totalParts int) error {
	_, err := db.conn.Exec(`INSERT INTO multipart_uploads(upload_id, bucket_name, key, total_parts) VALUES (?, ?, ?, ?)`,
		uploadID, bucket, key, totalParts)
	return err
}

func (db *objectDB) getMultipartUpload(bucket, key, uploadID string) (int, bool, error) {
	var totalParts int
	err := db.conn.QueryRow(`SELECT total_parts FROM multipart_uploads WHERE upload_id = ? AND bucket_name = ? AND key = ?`,
		uploadID, bucket, key).Scan(&totalParts)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return totalParts, err == nil, err
}

func (db *objectDB) getPart(uploadID string, partNumber int) (objectFile, bool, error) {
	var file objectFile
	err := db.conn.QueryRow(`SELECT file_name, size FROM upload_parts WHERE upload_id = ? AND part_number = ?`,
		uploadID, partNumber).Scan(&file.Name, &file.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return objectFile{}, false, nil
	}
	return file, err == nil, err
}

func (db *objectDB) setPart(uploadID string, partNumber int, file objectFile) error {
	_, err := db.conn.Exec(`INSERT INTO upload_parts(upload_id, part_number, file_name, size) VALUES (?, ?, ?, ?)
		ON CONFLICT(upload_id, part_number) DO UPDATE SET file_name = excluded.file_name, size = excluded.size`,
		uploadID, partNumber, file.Name, file.Size)
	return err
}

func (db *objectDB) listParts(uploadID string) ([]uploadPart, error) {
	rows, err := db.conn.Query(`SELECT part_number, file_name, size FROM upload_parts WHERE upload_id = ? ORDER BY part_number`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parts := make([]uploadPart, 0)
	for rows.Next() {
		var part uploadPart
		if err := rows.Scan(&part.PartNumber, &part.File.Name, &part.File.Size); err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return parts, nil
}
