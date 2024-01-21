package model

import (
	"encoding/json"
	"io"
	"net/http"
)

type Checkpoint struct {
	ID        string
	Sandbox   string
	Container string
	Round     int
	Ref       string
	Committed bool
}

func NewCheckpointFromRequest(r *http.Request) (*Checkpoint, error) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var checkpoint Checkpoint
	err = json.Unmarshal(data, &checkpoint)
	if err != nil {
		return nil, err
	}
	return &checkpoint, nil
}

func (c *Checkpoint) WriteToResponse(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(c); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}
