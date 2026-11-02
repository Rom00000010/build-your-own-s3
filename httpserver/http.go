package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
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
	CreateMultipartUpload(string, string, int) (string, error)
	UploadPart(string, string, string, int, io.Reader) (objectstorage.PartInfo, error)
	ListParts(string, string, string) ([]objectstorage.PartInfo, error)
	CompleteMultipartUpload(string, string, string) error
	AbortMultipartUpload(string, string, string) error
}

type handler struct {
	service objectService
	mux     *http.ServeMux
}

// NewHandler 将 HTTP 请求映射到对象存储操作。
func NewHandler(service objectService) http.Handler {
	h := &handler{service: service, mux: http.NewServeMux()}
	h.mux.HandleFunc("PUT /{$}", h.createBucket)
	h.mux.HandleFunc("PUT /{bucket}", h.createBucket)
	h.mux.HandleFunc("GET /{$}", h.listBuckets)
	h.mux.HandleFunc("GET /{bucket}", h.listObjects)
	h.mux.HandleFunc("DELETE /{bucket}", h.deleteBucket)
	h.mux.HandleFunc("PUT /{bucket}/{key...}", h.objectRequest)
	h.mux.HandleFunc("GET /{bucket}/{key...}", h.objectRequest)
	h.mux.HandleFunc("DELETE /{bucket}/{key...}", h.objectRequest)
	h.mux.HandleFunc("POST /{bucket}/{key...}", h.objectRequest)
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

func (h *handler) objectRequest(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || query.Has("partNumber") && r.Method != http.MethodPut {
		writeError(w, objectstorage.InvalidMultipartRequestErr)
		return
	}
	if query.Has("uploads") {
		if r.Method != http.MethodPost || query.Has("uploadId") || query.Has("partNumber") {
			writeError(w, objectstorage.InvalidMultipartRequestErr)
			return
		}
		h.createMultipartUpload(w, r)
		return
	}
	if query.Has("uploadId") || query.Has("partNumber") {
		if query.Get("uploadId") == "" {
			writeError(w, objectstorage.InvalidMultipartRequestErr)
			return
		}
		switch r.Method {
		case http.MethodPut:
			h.uploadPart(w, r)
		case http.MethodGet, http.MethodHead:
			h.listParts(w, r)
		case http.MethodPost:
			h.completeMultipartUpload(w, r)
		case http.MethodDelete:
			h.abortMultipartUpload(w, r)
		}
		return
	}
	switch r.Method {
	case http.MethodPut:
		h.putObject(w, r)
	case http.MethodGet, http.MethodHead:
		h.getObject(w, r)
	case http.MethodDelete:
		h.deleteObject(w, r)
	default:
		writeError(w, objectstorage.InvalidMultipartRequestErr)
	}
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

func (h *handler) createMultipartUpload(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, objectstorage.InvalidMultipartRequestErr)
		return
	}
	var request struct {
		TotalParts int `json:"totalParts"`
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		writeError(w, objectstorage.InvalidMultipartRequestErr)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, objectstorage.InvalidMultipartRequestErr)
		return
	}
	uploadID, err := h.service.CreateMultipartUpload(r.PathValue("bucket"), r.PathValue("key"), request.TotalParts)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(struct {
		UploadID string `json:"uploadId"`
	}{UploadID: uploadID})
}

type partResponse struct {
	PartNumber int   `json:"partNumber"`
	Size       int64 `json:"size"`
}

func (h *handler) uploadPart(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	partNumber, err := strconv.Atoi(query.Get("partNumber"))
	if err != nil {
		writeError(w, objectstorage.InvalidMultipartRequestErr)
		return
	}
	part, err := h.service.UploadPart(r.PathValue("bucket"), r.PathValue("key"), query.Get("uploadId"), partNumber, r.Body)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(partResponse{PartNumber: part.PartNumber, Size: part.Size})
}

func (h *handler) listParts(w http.ResponseWriter, r *http.Request) {
	parts, err := h.service.ListParts(r.PathValue("bucket"), r.PathValue("key"), r.URL.Query().Get("uploadId"))
	if err != nil {
		writeError(w, err)
		return
	}
	response := make([]partResponse, len(parts))
	for i, part := range parts {
		response[i] = partResponse{PartNumber: part.PartNumber, Size: part.Size}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Parts []partResponse `json:"parts"`
	}{Parts: response})
}

func (h *handler) completeMultipartUpload(w http.ResponseWriter, r *http.Request) {
	if err := h.service.CompleteMultipartUpload(r.PathValue("bucket"), r.PathValue("key"), r.URL.Query().Get("uploadId")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) abortMultipartUpload(w http.ResponseWriter, r *http.Request) {
	if err := h.service.AbortMultipartUpload(r.PathValue("bucket"), r.PathValue("key"), r.URL.Query().Get("uploadId")); err != nil {
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
	case errors.Is(err, objectstorage.InvalidMultipartRequestErr):
		status, name = http.StatusBadRequest, "InvalidMultipartRequest"
	case errors.Is(err, objectstorage.InvalidPartErr):
		status, name = http.StatusBadRequest, "InvalidPart"
	case errors.Is(err, objectstorage.IncompleteUploadErr):
		status, name = http.StatusBadRequest, "IncompleteUpload"
	case errors.Is(err, objectstorage.UploadNotExistErr):
		status, name = http.StatusNotFound, "UploadNotExist"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: name})
}
