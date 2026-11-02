package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"videojobs/internal/config"
)

var (
	ErrNotFound  = errors.New("job not found")
	ErrFinished  = errors.New("job is already finished")
	ErrQueueFull = errors.New("queue is full")
	ErrStopping  = errors.New("service is stopping")
)

type Service struct {
	mu              sync.Mutex
	jobs            map[string]*Job
	cancels         map[string]context.CancelFunc
	done            map[string]chan struct{}
	canceling       map[string]bool
	queue           chan string
	store           store
	config          config.Config
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	stopping        bool
	shutdownStarted bool
}

func NewService(c config.Config) (*Service, error) {
	if err := os.MkdirAll(filepath.Join(c.DataDir, "inputs"), 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(c.DataDir, "outputs"), 0700); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{config: c, store: store{dir: c.DataDir}, cancels: make(map[string]context.CancelFunc), done: make(map[string]chan struct{}), canceling: make(map[string]bool), queue: make(chan string, c.QueueSize), ctx: ctx, cancel: cancel}
	var err error
	s.jobs, err = s.store.load()
	if err != nil {
		cancel()
		return nil, err
	}
	if err = s.recoverJobs(); err != nil {
		cancel()
		return nil, err
	}
	for i := 0; i < c.Workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	return s, nil
}

func (s *Service) recoverJobs() error {
	now := time.Now().UTC()
	changed := false
	for _, j := range s.jobs {
		if j.Status == Processing || j.Status == Queued {
			j.Status, j.FinishedAt, j.Error = Failed, &now, "application restarted before job completed"
			changed = true
		}
		if j.Status != Completed && j.OutputFile != "" {
			for _, path := range []string{j.OutputFile, j.OutputFile + ".part.mp4"} {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
		}
	}
	if changed {
		return s.store.save(s.jobs)
	}
	return nil
}

func NewID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Service) InputPath(id, extension string) string {
	return filepath.Join(s.config.DataDir, "inputs", id+extension)
}

func (s *Service) CanAccept() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.admissionErrorLocked()
}

func (s *Service) admissionErrorLocked() error {
	if s.stopping {
		return ErrStopping
	}
	if len(s.queue) == cap(s.queue) {
		return ErrQueueFull
	}
	return nil
}

func (s *Service) Enqueue(id, input string, size int64) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.admissionErrorLocked(); err != nil {
		return Job{}, err
	}
	j := &Job{ID: id, InputFile: input, OutputFile: filepath.Join(s.config.DataDir, "outputs", id+".mp4"), Status: Queued, CreatedAt: time.Now().UTC(), InputSize: size}
	s.jobs[id] = j
	if err := s.store.save(s.jobs); err != nil {
		delete(s.jobs, id)
		return Job{}, err
	}
	s.queue <- id
	return *j, nil
}

func (s *Service) List() []Job {
	s.mu.Lock()
	items := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		items = append(items, *j)
	}
	s.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items
}

func (s *Service) Get(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *j, true
}

func (s *Service) Cancel(id string) (Job, error) {
	s.mu.Lock()
	j, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return Job{}, ErrNotFound
	}
	if j.Status == Processing {
		s.canceling[id] = true
		cancel, done := s.cancels[id], s.done[id]
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}
		job, ok := s.Get(id)
		if !ok {
			return Job{}, ErrNotFound
		}
		return job, nil
	}
	if j.Status != Queued {
		s.mu.Unlock()
		return Job{}, ErrFinished
	}
	previous := *j
	now := time.Now().UTC()
	j.Status = Canceled
	j.FinishedAt = &now
	j.Error = "canceled by user"
	if err := s.store.save(s.jobs); err != nil {
		*j = previous
		s.mu.Unlock()
		return Job{}, err
	}
	copy := *j
	s.mu.Unlock()
	return copy, nil
}

func (s *Service) StopAccepting() {
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
}

func (s *Service) Shutdown() {
	s.mu.Lock()
	if s.shutdownStarted {
		s.mu.Unlock()
		s.wg.Wait()
		return
	}
	s.shutdownStarted = true
	s.stopping = true
	now := time.Now().UTC()
	var cancels []context.CancelFunc
	for id, j := range s.jobs {
		if j.Status == Queued {
			j.Status = Canceled
			j.FinishedAt = &now
			j.Error = "service stopped"
		} else if j.Status == Processing {
			s.canceling[id] = true
		}
		if j.Status == Processing {
			if cancel := s.cancels[id]; cancel != nil {
				cancels = append(cancels, cancel)
			}
		}
	}
	if err := s.store.save(s.jobs); err != nil {
		log.Printf("persist shutdown: %v", err)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	s.cancel()
	s.wg.Wait()
}
