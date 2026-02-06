package queue

import (
	"encoding/json"

	"github.com/hibiken/asynq"
)

const TaskProcessVideo = "video:process"

type ProcessPayload struct {
	JobID     string `json:"job_id"`
	SourceURL string `json:"source_url"`
}

func NewProcessTask(p ProcessPayload) (*asynq.Task, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TaskProcessVideo, b), nil
}
