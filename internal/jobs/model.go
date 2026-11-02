package jobs

import "time"

type Status string

const (
	Queued     Status = "queued"
	Processing Status = "processing"
	Completed  Status = "completed"
	Failed     Status = "failed"
	Canceled   Status = "canceled"
	TimedOut   Status = "timed_out"
)

type Job struct {
	ID         string     `json:"id"`
	InputFile  string     `json:"inputFile"`
	OutputFile string     `json:"outputFile"`
	Status     Status     `json:"status"`
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Error      string     `json:"error,omitempty"`
	ExitCode   *int       `json:"exitCode,omitempty"`
	InputSize  int64      `json:"inputSize"`
	OutputSize int64      `json:"outputSize,omitempty"`
}
