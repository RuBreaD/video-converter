package ffmpeg

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"syscall"
)

type Result struct {
	Err        error
	ExitCode   *int
	Diagnostic string
}

type tailBuffer struct{ data []byte }

func (b *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.data = append(b.data, p...)
	if len(b.data) > 8192 {
		b.data = append([]byte(nil), b.data[len(b.data)-8192:]...)
	}
	return n, nil
}

func Convert(ctx context.Context, binary, input, partial string) Result {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", input, "-vf", "scale=w='min(1280,iw)':h='min(720,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2", "-c:v", "libx264", "-preset", "medium", "-crf", "23", "-c:a", "aac", "-movflags", "+faststart", partial}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.Stdout = io.Discard
	var stderr tailBuffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	r := Result{Err: err, Diagnostic: strings.TrimSpace(string(stderr.data))}
	if err != nil {
		if exit := new(exec.ExitError); errors.As(err, &exit) {
			code := exit.ExitCode()
			r.ExitCode = &code
		} else {
			r.Diagnostic = "cannot start FFmpeg: " + err.Error()
		}
		if r.Diagnostic == "" {
			r.Diagnostic = err.Error()
		}
	}
	return r
}
