package simplecloud

import (
	"context"
	"io"
	"net/http"
)

type HTTPBucket struct {
	Client *http.Client
}

func NewHTTPBucket(client *http.Client) *HTTPBucket {
	return &HTTPBucket{
		Client: client,
	}
}

func (h *HTTPBucket) NewReader(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}
