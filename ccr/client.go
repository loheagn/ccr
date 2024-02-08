package ccr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/containerd/containerd/v2/ccr/endpoint"
	"github.com/containerd/containerd/v2/ccr/model"
)

var client *Client

type Client struct {
	c       *http.Client
	baseURL string
}

func init() {
	endpoint := os.Getenv("CCR_SERVER_ENDPOINT")
	if endpoint == "" {
		panic("ccr server endpoint should not be empty")
	}
	client = &Client{
		baseURL: endpoint,
		c: &http.Client{
			Timeout: 30 * time.Minute,
		},
	}
}

func (c *Client) RequestForCheckpoint(url string, req *model.Checkpoint) (*model.Checkpoint, error) {
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.c.Post(c.baseURL+url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status code %d", resp.StatusCode)
	}

	var checkpoint model.Checkpoint
	err = json.NewDecoder(resp.Body).Decode(&checkpoint)
	if err != nil {
		return nil, err
	}

	return &checkpoint, nil
}

func CreateCheckpoint(sandbox, container string) (*model.Checkpoint, error) {
	req := &model.Checkpoint{
		Sandbox:   sandbox,
		Container: container,
	}
	return client.RequestForCheckpoint(endpoint.CreateCheckpoint, req)
}

func CommitCheckpoint(id string) (*model.Checkpoint, error) {
	req := &model.Checkpoint{
		ID: id,
	}
	return client.RequestForCheckpoint(endpoint.CommitCheckpoint, req)
}

func GetCheckpoint(sandbox, container string) (*model.Checkpoint, error) {
	req := &model.Checkpoint{
		Sandbox:   sandbox,
		Container: container,
	}
	return client.RequestForCheckpoint(endpoint.GetCheckpoint, req)
}

func UploadTar(id string, tarPath string) (*model.Checkpoint, error) {
	reqReader, err := os.Open(tarPath)
	if err != nil {
		return nil, err
	}

	resp, err := client.c.Post(client.baseURL+endpoint.UploadTar+"?id="+id, "application/octet-stream", reqReader)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status code %d", resp.StatusCode)
	}

	var checkpoint model.Checkpoint
	err = json.NewDecoder(resp.Body).Decode(&checkpoint)
	if err != nil {
		return nil, err
	}

	return &checkpoint, nil
}
