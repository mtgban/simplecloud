package simplecloud

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// HTTPBucket implements Reader for HTTP and HTTPS sources. It does not support
// writes; use a different backend for upload destinations.
type HTTPBucket struct {
	Client *http.Client
	URL    *url.URL
}

// NewHTTPBucket constructs an HTTPBucket with the given base URL. The scheme,
// host, and any credentials are reused for every request; the path component
// is replaced per call to NewReader. If client is nil, http.DefaultClient is
// used.
func NewHTTPBucket(client *http.Client, path string) (*HTTPBucket, error) {
	if client == nil {
		client = http.DefaultClient
	}
	u, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	return &HTTPBucket{
		Client: client,
		URL:    u,
	}, nil
}

// NewReader issues a GET request for path under the bucket's base URL and
// returns the response body. Non-2xx responses are returned as an error with
// the URL redacted. The caller must close the returned ReadCloser when done.
func (h *HTTPBucket) NewReader(ctx context.Context, path string) (io.ReadCloser, error) {
	u := new(url.URL)
	*u = *h.URL
	if h.URL.User != nil {
		u.User = new(url.Userinfo)
		*u.User = *h.URL.User
	}

	u.Path = path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", u.Redacted(), resp.Status)
	}

	return resp.Body, nil
}
