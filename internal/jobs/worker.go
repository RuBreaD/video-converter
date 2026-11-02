package jobs

import (
	"context"
	"errors"
	"log"
	"os"
	"time"

	"videojobs/internal/ffmpeg"
)

func (s *Service) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case id := <-s.queue:
			s.mu.Lock()
			j := s.jobs[id]
			if j == nil || j.Status != Queued || s.stopping {
				s.mu.Unlock()
				continue
			}
			ctx, cancel := context.WithTimeout(s.ctx, s.config.Timeout)
			s.cancels[id] = cancel
			j.Status = Processing
			now := time.Now().UTC()
			j.StartedAt = &now
			if err := s.store.save(s.jobs); err != nil {
				j.Status = Failed
				j.Error = "cannot persist processing state: " + err.Error()
				finished := time.Now().UTC()
				j.FinishedAt = &finished
				delete(s.cancels, id)
				_ = s.store.save(s.jobs)
				s.mu.Unlock()
				cancel()
				continue
			}
			s.done[id] = make(chan struct{})
			input, output := j.InputFile, j.OutputFile
			s.mu.Unlock()
			s.run(ctx, id, input, output)
			cancel()
		}
	}
}

func (s *Service) run(ctx context.Context, id, input, output string) {
	partial := output + ".part.mp4"
	_ = os.Remove(partial)
	result := ffmpeg.Convert(ctx, s.config.FFmpeg, input, partial)
	s.mu.Lock()
	j := s.jobs[id]
	if j == nil {
		s.mu.Unlock()
		_ = os.Remove(partial)
		return
	}
	now := time.Now().UTC()
	j.FinishedAt = &now
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		j.Status = TimedOut
		j.Error = "processing time limit exceeded"
	} else if s.canceling[id] || ctx.Err() != nil {
		j.Status = Canceled
		if s.canceling[id] {
			j.Error = "canceled by user"
		} else {
			j.Error = "service stopped"
		}
	} else if result.Err != nil {
		j.Status = Failed
		j.Error = result.Diagnostic
		j.ExitCode = result.ExitCode
	} else if stat, err := os.Stat(partial); err != nil || stat.Size() == 0 {
		j.Status = Failed
		j.Error = "FFmpeg produced no output"
	} else if err := os.Rename(partial, output); err != nil {
		j.Status = Failed
		j.Error = "cannot save output: " + err.Error()
	} else {
		j.Status = Completed
		j.OutputSize = stat.Size()
	}
	if err := s.store.save(s.jobs); err != nil {
		log.Printf("persist job %s: %v", id, err)
	}
	keep := j.Status == Completed
	done := s.done[id]
	delete(s.cancels, id)
	delete(s.done, id)
	delete(s.canceling, id)
	s.mu.Unlock()
	if !keep {
		_ = os.Remove(partial)
	}
	if done != nil {
		close(done)
	}
}
