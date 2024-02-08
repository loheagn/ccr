package model

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/lib/pq"

	"github.com/containerd/containerd/v2/mount"
)

type Checkpoint struct {
	ID        string `gorm:"primaryKey"`
	Sandbox   string
	Container string
	Round     int
	Ref       string
	Committed bool
	Mount     CCRMount `gorm:"embedded;embeddedPrefix:mount_"`
}

type CCRMount struct {
	Type    string
	Source  string
	Options pq.StringArray `gorm:"type:text[]"`
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

func (c *Checkpoint) ToMount() mount.Mount {
	return mount.Mount{
		Type:    c.Mount.Type,
		Source:  c.Mount.Source,
		Options: c.Mount.Options,
	}
}

func (c *Checkpoint) RemotePath() string {
	switch c.Mount.Type {
	case "nfs":
		return strings.Split(c.Mount.Source, ":")[1]
	default:
		return ""
	}
}
