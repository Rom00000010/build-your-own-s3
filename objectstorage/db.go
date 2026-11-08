package objectstorage

import (
	"database/sql"
	"encoding/json"
	"errors"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type objectDB struct {
	conn *sql.DB
}

type objectFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type object struct {
	Files []objectFile
	Size  int64
	ETag  string
}

type uploadPart struct {
	PartNumber int
	File       objectFile
}

func openObjectDB(connectionString string) (*objectDB, error) {
	conn, err := sql.Open("pgx", connectionString)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)

	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS buckets (
			name TEXT PRIMARY KEY
		)`,
		`CREATE TABLE IF NOT EXISTS objects (
			bucket_name TEXT NOT NULL,
			key TEXT NOT NULL,
			files TEXT NOT NULL,
			size BIGINT NOT NULL,
			etag TEXT NOT NULL,
			PRIMARY KEY (bucket_name, key),
			FOREIGN KEY (bucket_name) REFERENCES buckets(name)
		)`,
		`CREATE TABLE IF NOT EXISTS multipart_uploads (
			upload_id TEXT PRIMARY KEY,
			bucket_name TEXT NOT NULL,
			key TEXT NOT NULL,
			total_parts BIGINT NOT NULL,
			FOREIGN KEY (bucket_name) REFERENCES buckets(name) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS upload_parts (
			upload_id TEXT NOT NULL,
			part_number BIGINT NOT NULL,
			file_name TEXT NOT NULL UNIQUE,
			size BIGINT NOT NULL,
			PRIMARY KEY (upload_id, part_number),
			FOREIGN KEY (upload_id) REFERENCES multipart_uploads(upload_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS gc_files (
			file_name TEXT PRIMARY KEY
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
	result, err := db.conn.Exec(`INSERT INTO buckets(name) VALUES ($1) ON CONFLICT(name) DO NOTHING`, name)
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
	result, err := db.conn.Exec(`DELETE FROM buckets WHERE name = $1
		AND NOT EXISTS (SELECT 1 FROM objects WHERE bucket_name = $1)`, name)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (db *objectDB) bucketExists(name string) (bool, error) {
	var found int
	err := db.conn.QueryRow(`SELECT 1 FROM buckets WHERE name = $1`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (db *objectDB) getObject(bucket, key string) (object, bool, error) {
	var data string
	var metadata object
	err := db.conn.QueryRow(`SELECT files, size, etag FROM objects WHERE bucket_name = $1 AND key = $2`, bucket, key).Scan(&data, &metadata.Size, &metadata.ETag)
	if errors.Is(err, sql.ErrNoRows) {
		return object{}, false, nil
	}
	if err != nil {
		return object{}, false, err
	}
	if err := json.Unmarshal([]byte(data), &metadata.Files); err != nil {
		return object{}, false, err
	}
	return metadata, true, nil
}

func (db *objectDB) setObject(bucket, key string, object object) error {
	data, err := json.Marshal(object.Files)
	if err != nil {
		return err
	}
	_, err = db.conn.Exec(`INSERT INTO objects(bucket_name, key, files, size, etag) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT(bucket_name, key) DO UPDATE SET files = excluded.files, size = excluded.size, etag = excluded.etag`,
		bucket, key, string(data), object.Size, object.ETag)
	return err
}

func (db *objectDB) deleteObject(bucket, key string) error {
	_, err := db.conn.Exec(`DELETE FROM objects WHERE bucket_name = $1 AND key = $2`, bucket, key)
	return err
}

func (db *objectDB) listObjects(bucket string) ([]string, error) {
	rows, err := db.conn.Query(`SELECT key FROM objects WHERE bucket_name = $1`, bucket)
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
	_, err := db.conn.Exec(`INSERT INTO multipart_uploads(upload_id, bucket_name, key, total_parts) VALUES ($1, $2, $3, $4)`,
		uploadID, bucket, key, totalParts)
	return err
}

func (db *objectDB) getMultipartUpload(bucket, key, uploadID string) (int, bool, error) {
	var totalParts int
	err := db.conn.QueryRow(`SELECT total_parts FROM multipart_uploads WHERE upload_id = $1 AND bucket_name = $2 AND key = $3`,
		uploadID, bucket, key).Scan(&totalParts)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return totalParts, err == nil, err
}

func (db *objectDB) getPart(uploadID string, partNumber int) (objectFile, bool, error) {
	var file objectFile
	err := db.conn.QueryRow(`SELECT file_name, size FROM upload_parts WHERE upload_id = $1 AND part_number = $2`,
		uploadID, partNumber).Scan(&file.Name, &file.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return objectFile{}, false, nil
	}
	return file, err == nil, err
}

func (db *objectDB) setPart(uploadID string, partNumber int, file objectFile) error {
	_, err := db.conn.Exec(`INSERT INTO upload_parts(upload_id, part_number, file_name, size) VALUES ($1, $2, $3, $4)
		ON CONFLICT(upload_id, part_number) DO UPDATE SET file_name = excluded.file_name, size = excluded.size`,
		uploadID, partNumber, file.Name, file.Size)
	return err
}

func (db *objectDB) listParts(uploadID string) ([]uploadPart, error) {
	rows, err := db.conn.Query(`SELECT part_number, file_name, size FROM upload_parts WHERE upload_id = $1 ORDER BY part_number`, uploadID)
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

func (db *objectDB) completeMultipartUpload(bucket, key, uploadID string, object object) error {
	data, err := json.Marshal(object.Files)
	if err != nil {
		return err
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO objects(bucket_name, key, files, size, etag) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT(bucket_name, key) DO UPDATE SET files = excluded.files, size = excluded.size, etag = excluded.etag`,
		bucket, key, string(data), object.Size, object.ETag); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM multipart_uploads WHERE upload_id = $1`, uploadID); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *objectDB) deleteMultipartUpload(uploadID string) error {
	_, err := db.conn.Exec(`DELETE FROM multipart_uploads WHERE upload_id = $1`, uploadID)
	return err
}

func (db *objectDB) listBucketUploadFiles(bucket string) ([]objectFile, error) {
	rows, err := db.conn.Query(`SELECT file_name, size FROM upload_parts
		WHERE upload_id IN (SELECT upload_id FROM multipart_uploads WHERE bucket_name = $1)`, bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := make([]objectFile, 0)
	for rows.Next() {
		var file objectFile
		if err := rows.Scan(&file.Name, &file.Size); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return files, nil
}

func (db *objectDB) addGCFiles(names []string) error {
	if len(names) == 0 {
		return nil
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, name := range names {
		if _, err := tx.Exec(`INSERT INTO gc_files(file_name) VALUES ($1) ON CONFLICT(file_name) DO NOTHING`, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *objectDB) listGCFiles() ([]string, error) {
	rows, err := db.conn.Query(`SELECT file_name FROM gc_files`)
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
	return names, rows.Err()
}

func (db *objectDB) deleteGCFiles(names []string) error {
	if len(names) == 0 {
		return nil
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, name := range names {
		if _, err := tx.Exec(`DELETE FROM gc_files WHERE file_name = $1`, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}
