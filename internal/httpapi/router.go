package httpapi

import (
	"encoding/json"
	"net/http"
	"videojobs/internal/jobs"
)

type Handler struct {
	service   *jobs.Service
	maxUpload int64
}

func NewHandler(service *jobs.Service, maxUpload int64) http.Handler {
	h := &Handler{service: service, maxUpload: maxUpload}
	m := http.NewServeMux()
	m.HandleFunc("POST /videos", h.upload)
	m.HandleFunc("GET /jobs", h.list)
	m.HandleFunc("GET /jobs/{id}", h.get)
	m.HandleFunc("POST /jobs/{id}/cancel", h.cancel)
	m.HandleFunc("GET /jobs/{id}/result", h.result)
	m.HandleFunc("GET /openapi.yaml", h.spec)
	m.HandleFunc("GET /docs", h.swagger)
	return m
}

func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func apiError(w http.ResponseWriter, status int, message string) {
	reply(w, status, map[string]string{"error": message})
}
