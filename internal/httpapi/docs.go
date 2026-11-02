package httpapi

import (
	"embed"
	"net/http"
)

//go:embed openapi.yaml docs.html
var docs embed.FS

func (h *Handler) spec(w http.ResponseWriter, r *http.Request) {
	b, err := docs.ReadFile("openapi.yaml")
	if err != nil {
		apiError(w, 500, "API specification unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write(b)
}

func (h *Handler) swagger(w http.ResponseWriter, r *http.Request) {
	b, err := docs.ReadFile("docs.html")
	if err != nil {
		apiError(w, 500, "Swagger UI unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}
