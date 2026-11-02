package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"videojobs/internal/jobs"
)

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		apiError(w, 415, "expected multipart/form-data with file field")
		return
	}
	if err := h.service.CanAccept(); err != nil {
		apiError(w, 503, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUpload+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		apiError(w, 400, "invalid multipart request")
		return
	}
	part, err := mr.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		apiError(w, 400, "first multipart field must be a file named file")
		return
	}
	ext := strings.ToLower(filepath.Ext(part.FileName()))
	if !map[string]bool{".mp4": true, ".mov": true, ".mkv": true, ".webm": true, ".avi": true}[ext] {
		apiError(w, 400, "supported extensions: mp4, mov, mkv, webm, avi")
		return
	}
	id, err := jobs.NewID()
	if err != nil {
		apiError(w, 500, "cannot create job ID")
		return
	}
	input := h.service.InputPath(id, ext)
	f, err := os.OpenFile(input, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		apiError(w, 500, "cannot save upload")
		return
	}
	n, copyErr := io.Copy(f, io.LimitReader(part, h.maxUpload+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || n == 0 || n > h.maxUpload {
		_ = os.Remove(input)
		if n > h.maxUpload {
			apiError(w, 413, "file exceeds upload limit")
		} else {
			apiError(w, 400, "could not read file or file is empty")
		}
		return
	}
	more, err := mr.NextPart()
	if err != io.EOF || more != nil {
		_ = os.Remove(input)
		apiError(w, 400, "expected exactly one file field")
		return
	}
	if r.Context().Err() != nil {
		_ = os.Remove(input)
		return
	}
	if _, err := h.service.Enqueue(id, input, n); err != nil {
		_ = os.Remove(input)
		if errors.Is(err, jobs.ErrQueueFull) || errors.Is(err, jobs.ErrStopping) {
			apiError(w, 503, err.Error())
		} else {
			apiError(w, 500, "cannot persist job")
		}
		return
	}
	reply(w, 202, map[string]string{"jobId": id, "status": string(jobs.Queued)})
}
