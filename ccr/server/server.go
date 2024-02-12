package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/otiai10/copy"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/containerd/containerd/v2/archive"
	"github.com/containerd/containerd/v2/ccr/endpoint"
	"github.com/containerd/containerd/v2/ccr/model"
	"github.com/google/uuid"
)

var (
	db        *gorm.DB
	storePath string

	registryHost = func() string {
		host := os.Getenv("CCR_REGISTRY_HOST")
		if strings.HasPrefix(host, "http://") {
			return strings.TrimPrefix(host, "http://")
		}
		if strings.HasPrefix(host, "https://") {
			return strings.TrimPrefix(host, "https://")
		}
		return host
	}()

	registryNamespace = os.Getenv("CCR_REGISTRY_NAMESPACE")
)

func latestCheckpointQuery(sandbox, container string) *gorm.DB {
	return db.Model(&model.Checkpoint{}).Where("sandbox = ? AND container = ? AND committed = ?", sandbox, container, true).Order("round desc")
}

func createCheckpoint(w http.ResponseWriter, r *http.Request) {
	req, err := model.NewCheckpointFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	round := 0
	query := latestCheckpointQuery(req.Sandbox, req.Container)
	var count int64
	if query.Count(&count); count == 0 {
		round = 1
	} else {
		var got model.Checkpoint
		if err := query.First(&got).Error; err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		round = got.Round + 1
	}

	ref := fmt.Sprintf("%s/%s:checkpoint-%s-%s-v%d", registryHost, registryNamespace, req.Sandbox, req.Container, round)

	newCheckpoint := &model.Checkpoint{
		ID:        uuid.NewString(),
		Sandbox:   req.Sandbox,
		Container: req.Container,
		Round:     round,
		Ref:       ref,
		Committed: false,
	}
	if err := db.Create(newCheckpoint).Error; err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	newCheckpoint.WriteToResponse(w)
}

func commitCheckpoint(w http.ResponseWriter, r *http.Request) {
	req, err := model.NewCheckpointFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	got := model.Checkpoint{}
	if err := db.First(&got, "id = ?", req.ID).Error; err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	got.Committed = true
	if err := db.Save(&got).Error; err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	got.WriteToResponse(w)

}

func getCheckpoint(w http.ResponseWriter, r *http.Request) {
	req, err := model.NewCheckpointFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	query := latestCheckpointQuery(req.Sandbox, req.Container)
	var count int64
	if query.Count(&count); count == 0 {
		(&model.Checkpoint{}).WriteToResponse(w)
		return
	}

	got := model.Checkpoint{}
	if err := db.Order("round desc").First(&got, "sandbox = ? AND container = ?", req.Sandbox, req.Container).Error; err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	got.WriteToResponse(w)
}

func uploadTar(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c := model.Checkpoint{}
	db.First(&c, c.ID)
	remotePath, err := os.MkdirTemp(storePath, fmt.Sprintf("%s-%s-", c.Sandbox, c.Container))
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	remotePath, _ = filepath.Abs(remotePath)

	{
		lastCheckpoint := model.Checkpoint{}
		if result := latestCheckpointQuery(c.Sandbox, c.Container).First(&lastCheckpoint); result.Error == nil {
			prePath := lastCheckpoint.RemotePath()
			copy.Copy(prePath, remotePath)
		}
	}

	archive.Apply(r.Context(), remotePath, r.Body)

	c.Mount = model.CCRMount{
		Type:   "nfs",
		Source: "127.0.0.1:" + remotePath,
		Options: []string{
			"vers=4",
			"addr=127.0.0.1",
		},
	}

	db.Save(&c)

	c.WriteToResponse(w)
}

func setupDB() {
	var err error
	db, err = gorm.Open(sqlite.Open("main.db"), &gorm.Config{})
	if err != nil {
		panic("failed to connect database")
	}
	db.AutoMigrate(&model.Checkpoint{})
}

func main() {
	setupDB()

	storePath = os.Getenv("CCR_STORE_PATH")

	http.HandleFunc(endpoint.GetCheckpoint, getCheckpoint)
	http.HandleFunc(endpoint.CreateCheckpoint, createCheckpoint)
	http.HandleFunc(endpoint.UploadTar, uploadTar)
	http.HandleFunc(endpoint.CommitCheckpoint, commitCheckpoint)

	// Starting the server on port 8080.
	fmt.Println("Server is running on port 8080...")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		fmt.Println("Error starting server: ", err)
	}
}
