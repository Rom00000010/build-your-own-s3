package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"build_your_own_s3/objectstorage"
)

type objectService interface {
	CreateBucket(string) (string, error)
	ListBuckets() ([]string, error)
	DeleteBucket(string) error
	PutObject(string, string, io.Reader) error
	GetObject(string, string) (io.ReadCloser, int64, error)
	DeleteObject(string, string) error
	ListObjects(string) ([]string, error)
}

type handler struct {
	service objectService
	mux     *http.ServeMux
}

// NewHandler 将 HTTP 请求映射到七个对象存储操作。
func NewHandler(service objectService) http.Handler {
	h := &handler{service: service, mux: http.NewServeMux()}
	h.mux.HandleFunc("PUT /{$}", h.createBucket)
	h.mux.HandleFunc("PUT /{bucket}", h.createBucket)
	h.mux.HandleFunc("GET /{$}", h.listBuckets)
	h.mux.HandleFunc("GET /{bucket}", h.listObjects)
	h.mux.HandleFunc("DELETE /{bucket}", h.deleteBucket)
	h.mux.HandleFunc("PUT /{bucket}/{key...}", h.putObject)
	h.mux.HandleFunc("GET /{bucket}/{key...}", h.getObject)
	h.mux.HandleFunc("DELETE /{bucket}/{key...}", h.deleteObject)
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *handler) createBucket(w http.ResponseWriter, r *http.Request) {
	_, err := h.service.CreateBucket(r.PathValue("bucket"))
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *handler) listBuckets(w http.ResponseWriter, _ *http.Request) {
	names, err := h.service.ListBuckets()
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Buckets []string `json:"buckets"`
	}{Buckets: names})
}

func (h *handler) deleteBucket(w http.ResponseWriter, r *http.Request) {
	if err := h.service.DeleteBucket(r.PathValue("bucket")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) listObjects(w http.ResponseWriter, r *http.Request) {
	keys, err := h.service.ListObjects(r.PathValue("bucket"))
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Keys []string `json:"keys"`
	}{Keys: keys})
}

func (h *handler) putObject(w http.ResponseWriter, r *http.Request) {
	if err := h.service.PutObject(r.PathValue("bucket"), r.PathValue("key"), r.Body); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) getObject(w http.ResponseWriter, r *http.Request) {
	stream, size, err := h.service.GetObject(r.PathValue("bucket"), r.PathValue("key"))
	if err != nil {
		writeError(w, err)
		return
	}
	defer stream.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, stream)
}

func (h *handler) deleteObject(w http.ResponseWriter, r *http.Request) {
	if err := h.service.DeleteObject(r.PathValue("bucket"), r.PathValue("key")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeError(w http.ResponseWriter, err error) {
	status, name := http.StatusInternalServerError, "InternalError"
	switch {
	case errors.Is(err, objectstorage.InvalidBucketNameErr):
		status, name = http.StatusBadRequest, "InvalidBucketName"
	case errors.Is(err, objectstorage.InvalidObjectKeyErr):
		status, name = http.StatusBadRequest, "InvalidObjectKey"
	case errors.Is(err, objectstorage.BucketNameExistErr):
		status, name = http.StatusConflict, "BucketNameExist"
	case errors.Is(err, objectstorage.BucketNotEmptyErr):
		status, name = http.StatusConflict, "BucketNotEmpty"
	case errors.Is(err, objectstorage.BucketNotExistErr):
		status, name = http.StatusNotFound, "BucketNotExist"
	case errors.Is(err, objectstorage.ObjectNotExistErr):
		status, name = http.StatusNotFound, "ObjectNotExist"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: name})
}
