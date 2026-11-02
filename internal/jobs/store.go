package jobs

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
)

type store struct{ dir string }

func (s store) path() string { return filepath.Join(s.dir, "jobs.json") }

func (s store) load() (map[string]*Job, error) {
	b, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]*Job), nil
	}
	if err != nil {
		return nil, err
	}
	jobs := make(map[string]*Job)
	if err := json.Unmarshal(b, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s store) save(jobs map[string]*Job) error {
	f, err := os.CreateTemp(s.dir, "jobs-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		err = json.NewEncoder(f).Encode(jobs)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), s.path()); err != nil {
		return err
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		log.Printf("open jobs directory for sync: %v", err)
		return nil
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		log.Printf("sync jobs directory: %v", err)
	}
	return nil
}
