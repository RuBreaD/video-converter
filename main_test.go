package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"time"
	"videojobs/internal/config"
	"videojobs/internal/httpapi"
	"videojobs/internal/jobs"
)

type localServer struct {
	URL    string
	Client *http.Client
}
type localTransport struct{ handler http.Handler }

func (t localTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, r)
	return w.Result(), nil
}

func fakeFFmpeg(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nfor arg do last=$arg; done\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func serviceForTest(t *testing.T, ffmpeg string, workers, queue int, timeout time.Duration) (*jobs.Service, *localServer) {
	t.Helper()
	s, err := jobs.NewService(config.Config{DataDir: t.TempDir(), FFmpeg: ffmpeg, Workers: workers, QueueSize: queue, MaxUpload: 1024, Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	h := &localServer{URL: "http://local.test", Client: &http.Client{Transport: localTransport{httpapi.NewHandler(s, 1024)}}}
	t.Cleanup(func() { s.Shutdown() })
	return s, h
}

func uploadTest(t *testing.T, h *localServer) (string, int) {
	t.Helper()
	return uploadTestData(t, h, "sample.mp4", []byte("test video"))
}

func uploadTestData(t *testing.T, h *localServer, filename string, data []byte) (string, int) {
	t.Helper()
	var b bytes.Buffer
	m := multipart.NewWriter(&b)
	f, err := m.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write(data)
	_ = m.Close()
	r, err := h.Client.Post(h.URL+"/videos", m.FormDataContentType(), &b)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var v struct {
		JobID string `json:"jobId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&v)
	return v.JobID, r.StatusCode
}

func TestUploadSizeLimit(t *testing.T) {
	ff := fakeFFmpeg(t, `printf result > "$last"`)
	s, h := serviceForTest(t, ff, 1, 1, time.Second)
	_, status := uploadTestData(t, h, "too-large.mp4", bytes.Repeat([]byte("x"), 1025))
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload returned %d, want 413", status)
	}
	if got := len(s.List()); got != 0 {
		t.Fatalf("oversized upload created %d jobs", got)
	}
}

func getJob(t *testing.T, h *localServer, id string) jobs.Job {
	t.Helper()
	r, err := h.Client.Get(h.URL + "/jobs/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var j jobs.Job
	if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
		t.Fatal(err)
	}
	return j
}

func waitStatus(t *testing.T, h *localServer, id string, want jobs.Status) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j := getJob(t, h, id)
		if j.Status == want {
			return j
		}
		if j.Status == jobs.Completed || j.Status == jobs.Failed || j.Status == jobs.Canceled || j.Status == jobs.TimedOut {
			t.Fatalf("wanted %s, got %s: %s", want, j.Status, j.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach %s", id, want)
	return jobs.Job{}
}

func TestCompletedAndPersistence(t *testing.T) {
	ff := fakeFFmpeg(t, `printf result > "$last"`)
	s, h := serviceForTest(t, ff, 1, 2, time.Second)
	id, status := uploadTest(t, h)
	if status != 202 {
		t.Fatalf("upload: %d", status)
	}
	completed := waitStatus(t, h, id, jobs.Completed)
	r, err := h.Client.Get(h.URL + "/jobs/" + id + "/result")
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("result: %d", r.StatusCode)
	}
	post, err := h.Client.Post(h.URL+"/jobs/"+id+"/cancel", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = post.Body.Close()
	if post.StatusCode != 409 {
		t.Fatalf("cancel completed: %d", post.StatusCode)
	}
	s.Shutdown()
	restarted, err := jobs.NewService(config.Config{DataDir: filepath.Dir(filepath.Dir(completed.InputFile)), FFmpeg: ff, Workers: 1, QueueSize: 2, MaxUpload: 1024, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown()
	restored, ok := restarted.Get(id)
	if !ok || restored.Status != jobs.Completed {
		t.Fatal("completed job was not restored")
	}
}

func TestRecoveryRemovesIncompleteOutputs(t *testing.T) {
	dataDir := t.TempDir()
	outputDir := filepath.Join(dataDir, "outputs")
	if err := os.MkdirAll(outputDir, 0700); err != nil {
		t.Fatal(err)
	}
	queued := jobs.Job{ID: "queued", OutputFile: filepath.Join(outputDir, "queued.mp4"), Status: jobs.Queued}
	canceled := jobs.Job{ID: "canceled", OutputFile: filepath.Join(outputDir, "canceled.mp4"), Status: jobs.Canceled}
	for _, path := range []string{queued.OutputFile, queued.OutputFile + ".part.mp4", canceled.OutputFile, canceled.OutputFile + ".part.mp4"} {
		if err := os.WriteFile(path, []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	metadata, err := json.Marshal(map[string]jobs.Job{"queued": queued, "canceled": canceled})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "jobs.json"), metadata, 0600); err != nil {
		t.Fatal(err)
	}
	svc, err := jobs.NewService(config.Config{DataDir: dataDir, FFmpeg: "/missing/ffmpeg", Workers: 1, QueueSize: 1, MaxUpload: 1024, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Shutdown()
	if got, _ := svc.Get("queued"); got.Status != jobs.Failed {
		t.Fatalf("queued job recovered as %s, want failed", got.Status)
	}
	if got, _ := svc.Get("canceled"); got.Status != jobs.Canceled {
		t.Fatalf("canceled job changed to %s", got.Status)
	}
	for _, path := range []string{queued.OutputFile, queued.OutputFile + ".part.mp4", canceled.OutputFile, canceled.OutputFile + ".part.mp4"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("incomplete output remains at %s: %v", path, err)
		}
	}
}

func TestFailedStartAndExit(t *testing.T) {
	for _, tc := range []struct {
		name, ffmpeg string
		exit         bool
	}{
		{"start", "/missing/ffmpeg", false},
		{"exit", fakeFFmpeg(t, `echo broken >&2; exit 7`), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, h := serviceForTest(t, tc.ffmpeg, 1, 1, time.Second)
			id, status := uploadTest(t, h)
			if status != 202 {
				t.Fatal(status)
			}
			j := waitStatus(t, h, id, jobs.Failed)
			if j.Error == "" {
				t.Fatal("missing error")
			}
			if tc.exit && (j.ExitCode == nil || *j.ExitCode != 7 || !strings.Contains(j.Error, "broken")) {
				t.Fatalf("diagnostics: %+v", j)
			}
			if _, err := os.Stat(j.OutputFile + ".part.mp4"); !os.IsNotExist(err) {
				t.Fatalf("partial file remains: %v", err)
			}
		})
	}
}

func TestTimeoutAndCancellation(t *testing.T) {
	ff := fakeFFmpeg(t, `sleep 30 & child=$!; if [ -n "$PID_FILE" ]; then echo "$child" > "$PID_FILE"; fi; wait; printf result > "$last"`)
	t.Run("timeout", func(t *testing.T) {
		pidFile := filepath.Join(t.TempDir(), "child.pid")
		t.Setenv("PID_FILE", pidFile)
		_, h := serviceForTest(t, ff, 1, 1, 500*time.Millisecond)
		id, _ := uploadTest(t, h)
		j := waitStatus(t, h, id, jobs.TimedOut)
		assertProcessGone(t, waitPID(t, pidFile))
		if _, err := os.Stat(j.OutputFile); !os.IsNotExist(err) {
			t.Fatalf("unexpected result: %v", err)
		}
	})
	t.Run("queued and processing", func(t *testing.T) {
		pidFile := filepath.Join(t.TempDir(), "child.pid")
		t.Setenv("PID_FILE", pidFile)
		_, h := serviceForTest(t, ff, 1, 2, 10*time.Second)
		first, _ := uploadTest(t, h)
		waitStatus(t, h, first, jobs.Processing)
		pid := waitPID(t, pidFile)
		second, _ := uploadTest(t, h)
		if j := getJob(t, h, second); j.Status != jobs.Queued {
			t.Fatalf("second job: %s", j.Status)
		}
		for _, id := range []string{second, first} {
			r, err := h.Client.Post(h.URL+"/jobs/"+id+"/cancel", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			_ = r.Body.Close()
			if r.StatusCode != 200 {
				t.Fatalf("cancel: %d", r.StatusCode)
			}
			if j := getJob(t, h, id); j.Status != jobs.Canceled {
				t.Fatalf("cancelled job: %s", j.Status)
			}
		}
		assertProcessGone(t, pid)
	})
}

func TestShutdownStopsAdmissionAndWaitsForProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("PID_FILE", pidFile)
	ff := fakeFFmpeg(t, `sleep 30 & child=$!; echo "$child" > "$PID_FILE"; wait`)
	s, h := serviceForTest(t, ff, 1, 1, 10*time.Second)
	id, status := uploadTest(t, h)
	if status != http.StatusAccepted {
		t.Fatalf("upload: %d", status)
	}
	waitStatus(t, h, id, jobs.Processing)
	pid := waitPID(t, pidFile)
	s.StopAccepting()
	if _, status := uploadTest(t, h); status != http.StatusServiceUnavailable {
		t.Fatalf("upload after StopAccepting returned %d", status)
	}
	s.Shutdown()
	if got := getJob(t, h, id); got.Status != jobs.Canceled {
		t.Fatalf("job after shutdown: %s", got.Status)
	}
	assertProcessGone(t, pid)
}

func waitPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err == nil {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("FFmpeg child PID was not written to %s", path)
	return 0
}

func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d is still alive after job termination", pid)
}

func TestQueueCapacityAndWorkerLimit(t *testing.T) {
	ff := fakeFFmpeg(t, `sleep 1; printf result > "$last"`)
	_, h := serviceForTest(t, ff, 2, 1, 5*time.Second)
	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		id, status := uploadTest(t, h)
		if status != 202 {
			t.Fatalf("upload %d: %d", i, status)
		}
		ids = append(ids, id)
	}
	deadline := time.Now().Add(2 * time.Second)
	observed := false
	for time.Now().Before(deadline) {
		processing, queued := 0, 0
		for _, id := range ids {
			switch getJob(t, h, id).Status {
			case jobs.Processing:
				processing++
			case jobs.Queued:
				queued++
			}
		}
		if processing == 2 && queued == 1 {
			observed = true
			break
		}
		if processing > 2 {
			t.Fatal("worker limit exceeded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !observed {
		t.Fatal("did not observe two processing jobs and one queued job")
	}
	_, status := uploadTest(t, h)
	if status != 503 {
		t.Fatalf("full queue returned %d", status)
	}
	for _, id := range ids {
		r, err := h.Client.Post(fmt.Sprintf("%s/jobs/%s/cancel", h.URL, id), "", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
	}
}

func TestEmbeddedDocumentation(t *testing.T) {
	_, h := serviceForTest(t, "/missing/ffmpeg", 1, 1, time.Second)
	for _, path := range []string{"/docs", "/openapi.yaml"} {
		r, err := h.Client.Get(h.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatalf("%s: status %d", path, r.StatusCode)
		}
	}
}
