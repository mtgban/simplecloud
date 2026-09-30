package simplecloud_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mtgban/simplecloud"
)

type fakeGCS struct {
	mu  sync.Mutex
	ops []string
}

func (f *fakeGCS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.ops = append(f.ops, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	f.mu.Unlock()
	if strings.Contains(r.URL.RawQuery, "uploadType=resumable") {
		w.Header().Set("Location", "http://"+r.Host+r.URL.Path+"?upload_id=sess-1")
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
	io.WriteString(w, `{"error":{"code":500,"message":"refused"}}`)
}

// TestPOC_GCSViaEmulatorHost checks whether the GCS backend can be exercised
// without credentials via STORAGE_EMULATOR_HOST, as todo/008 would need.
func TestPOC_GCSViaEmulatorHost(t *testing.T) {
	fake := &fakeGCS{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	t.Setenv("STORAGE_EMULATOR_HOST", strings.TrimPrefix(srv.URL, "http://"))

	bucket, err := simplecloud.NewGCSClient(context.Background(), "", "b")
	if err != nil {
		t.Fatalf("NewGCSClient (does it need credentials?): %v", err)
	}

	_, err = simplecloud.Copy(context.Background(), &bigBucket{size: 20 << 20}, bucket, "src.bin", "dst.bin")
	t.Logf("copy error: %v", err)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.ops) == 0 {
		t.Fatal("no request reached the fake: STORAGE_EMULATOR_HOST is not a usable route")
	}
	for _, o := range fake.ops {
		t.Logf("  saw: %s", o)
	}
}
