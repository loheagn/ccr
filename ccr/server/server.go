package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/containerd/containerd/v2/archive"
	"github.com/containerd/containerd/v2/ccr/endpoint"
	"github.com/containerd/containerd/v2/ccr/model"
	"github.com/containerd/containerd/v2/rrw"
	"github.com/google/uuid"
	"github.com/hanwen/go-fuse/v2/fuse"
)

var (
	db        *gorm.DB
	storePath string

	mountRecord map[string]*fuse.Server = make(map[string]*fuse.Server)

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

func getRef(tag string) string {
	return fmt.Sprintf("%s/test/%s-2:%s", registryHost, registryNamespace, tag)
}

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

	ref := getRef(fmt.Sprintf("checkpoint-%s-%s-v%d", req.Sandbox, req.Container, round))

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
	if err := db.Order("round desc").First(&got, "sandbox = ? AND container = ? and committed = ?", req.Sandbox, req.Container, true).Error; err != nil {
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

	// {
	// 	lastCheckpoint := model.Checkpoint{}
	// 	if result := latestCheckpointQuery(c.Sandbox, c.Container).First(&lastCheckpoint); result.Error == nil {
	// 		prePath := lastCheckpoint.RemotePath()
	// 		copy.Copy(prePath, remotePath)
	// 	}
	// }

	archive.Apply(r.Context(), remotePath, r.Body)

	// c.Mount = model.CCRMount{
	// 	Type:   "nfs",
	// 	Source: "127.0.0.1:" + remotePath,
	// 	Options: []string{
	// 		"vers=4",
	// 		"addr=127.0.0.1",
	// 	},
	// }

	db.Save(&c)

	c.WriteToResponse(w)
}

func convertImageHandle(w http.ResponseWriter, r *http.Request) {
	req, err := model.NewImFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	converted, err := simpleConvertImage(req.Original)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	newIm := &model.Im{
		ID:        uuid.NewString(),
		Original:  req.Original,
		Converted: converted,
	}

	if err := db.Create(newIm).Error; err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	newIm.WriteToResponse(w)
}

func remoteMount(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	tarName := query.Get("tarname")
	path := query.Get("path")

	server, err := rrw.RemoteMount(tarName, path)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	mountRecord[path] = server

	w.WriteHeader(http.StatusOK)
}

func remoteUnmount(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	path := query.Get("path")

	server, ok := mountRecord[path]
	if !ok {
		w.WriteHeader(http.StatusOK)
		return
	}

	if server != nil {
		server.Unmount()
	}
	delete(mountRecord, path)

	w.WriteHeader(http.StatusOK)
}

func setupDB() {
	var err error
	dsn := os.Getenv("CCR_DB_STRING")

	// 使用 GORM 连接到数据库
	db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		panic("failed to connect database")
	}
	db.AutoMigrate(&model.Checkpoint{}, &model.Im{})
}

func main() {
	setupDB()

	defer func() {
		for _, server := range mountRecord {
			if server != nil {
				server.Unmount()
			}
		}
	}()

	storePath = os.Getenv("CCR_STORE_PATH")

	http.HandleFunc(endpoint.GetCheckpoint, getCheckpoint)
	http.HandleFunc(endpoint.CreateCheckpoint, createCheckpoint)
	http.HandleFunc(endpoint.UploadTar, uploadTar)
	http.HandleFunc(endpoint.CommitCheckpoint, commitCheckpoint)
	http.HandleFunc(endpoint.ConvertImage, convertImageHandle)
	http.HandleFunc(endpoint.Mount, remoteMount)
	http.HandleFunc(endpoint.Unmount, remoteUnmount)

	// Starting the server on port 8000.
	fmt.Println("Server is running on port 8000...")
	err := http.ListenAndServe(":8000", nil)
	if err != nil {
		fmt.Println("Error starting server: ", err)
	}
}
