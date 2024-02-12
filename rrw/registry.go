package rrw

import (
	"fmt"
	"os"

	"github.com/bloodorangeio/reggie"
)

var (
	registryHost      = os.Getenv("CCR_REGISTRY_HOST")
	registryNamespace = os.Getenv("CCR_REGISTRY_NAMESPACE")
)

func NewRegistry() *Registry {
	client, err := reggie.NewClient(
		registryHost,
		reggie.WithDefaultName(registryNamespace),
		reggie.WithInsecureSkipTLSVerify(false),
	)
	if err != nil {
		panic(err)
	}

	return &Registry{
		client: client,
	}
}

type Registry struct {
	client *reggie.Client
}

func (r *Registry) GetBlobRange(digest string, offset, length uint64) ([]byte, error) {
	req := r.client.NewRequest(reggie.GET, "/v2/<name>/blobs/<digest>",
		reggie.WithDigest(digest)).
		SetHeader("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get blob <%s>: %w", digest, err)
	}

	return resp.Body(), nil
}
