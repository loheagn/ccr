package model

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/lib/pq"
)

type Checkpoint struct {
	ID        string `gorm:"primaryKey"`
	Sandbox   string
	Container string
	Round     int
	Ref       string
	Committed bool
	// Mount     CCRMount `gorm:"embedded;embeddedPrefix:mount_"`
}

type Im struct {
	ID        string `gorm:"primaryKey"`
	Original  string
	Converted string
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

func NewImFromRequest(r *http.Request) (*Im, error) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var im Im
	err = json.Unmarshal(data, &im)
	if err != nil {
		return nil, err
	}
	return &im, nil
}

func (c *Checkpoint) WriteToResponse(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(c); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

// func (c *Checkpoint) ToMount() mount.Mount {
// 	return mount.Mount{
// 		Type:    c.Mount.Type,
// 		Source:  c.Mount.Source,
// 		Options: c.Mount.Options,
// 	}
// }

// func (c *Checkpoint) RemotePath() string {
// 	switch c.Mount.Type {
// 	case "nfs":
// 		return strings.Split(c.Mount.Source, ":")[1]
// 	default:
// 		return ""
// 	}
// }

func (c *Im) WriteToResponse(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(c); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}
