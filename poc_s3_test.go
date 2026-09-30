package simplecloud_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mtgban/simplecloud"
)

// fakeS3 records the S3 operations that reach it and fails every UploadPart,
// which is what makes manager.Uploader abort the multipart upload.
type fakeS3 struct {
	mu   sync.Mutex
	ops  []string
	fail bool
}

func (f *fakeS3) record(op string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, op)
}

func (f *fakeS3) seen(op string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.ops {
		if o == op {
			return true
		}
	}
	return false
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch {
	case r.Method == "POST" && q.Has("uploads"):
		f.record("CreateMultipartUpload")
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, `<?xml version="1.0"?><InitiateMultipartUploadResult><Bucket>b</Bucket><Key>k</Key><UploadId>upload-1</UploadId></InitiateMultipartUploadResult>`)
	case r.Method == "PUT" && q.Has("partNumber"):
		f.record("UploadPart")
		if f.fail {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `<?xml version="1.0"?><Error><Code>InternalError</Code><Message>refused</Message></Error>`)
			return
		}
		w.Header().Set("ETag", `"etag"`)
	case r.Method == "DELETE" && q.Has("uploadId"):
		f.record("AbortMultipartUpload")
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "POST" && q.Has("uploadId"):
		f.record("CompleteMultipartUpload")
		io.WriteString(w, `<?xml version="1.0"?><CompleteMultipartUploadResult><Bucket>b</Bucket><Key>k</Key></CompleteMultipartUploadResult>`)
	case r.Method == "PUT":
		f.record("PutObject")
		w.Header().Set("ETag", `"etag"`)
	default:
		f.record(r.Method + " " + r.URL.Path + "?" + r.URL.RawQuery)
		http.NotFound(w, r)
	}
}

// bigThenFail yields n bytes and then fails, so the uploader has committed at
// least one part before the source dies.
type bigThenFail struct{ left int }

func (b *bigThenFail) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, errors.New("network died mid-stream")
	}
	if len(p) > b.left {
		p = p[:b.left]
	}
	for i := range p {
		p[i] = 'x'
	}
	b.left -= len(p)
	return len(p), nil
}

type bigBucket struct{ size int }

func (b *bigBucket) NewReader(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(&bigThenFail{left: b.size}), nil
}

// TestPOC_S3AbortAgainstHTTPTest checks whether todo/008 is actually blocked on
// credentials, or whether an httptest fake reaches the S3 abort path the way
// refusingB2 reaches B2's.
func TestPOC_S3AbortAgainstHTTPTest(t *testing.T) {
	fake := &fakeS3{fail: true}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	bucket, err := simplecloud.NewS3Client(context.Background(), "ak", "sk", "b", srv.URL, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}

	// 12 MiB, so manager.Uploader's 5 MiB default part size means multipart.
	_, err = simplecloud.Copy(context.Background(), &bigBucket{size: 12 << 20}, bucket, "src.bin", "dst.bin")
	if err == nil {
		t.Fatal("expected the copy to fail")
	}
	t.Logf("copy error: %v", err)

	fake.mu.Lock()
	t.Logf("operations the fake saw: %s", strings.Join(fake.ops, ", "))
	fake.mu.Unlock()

	if !fake.seen("CreateMultipartUpload") {
		t.Error("never reached multipart upload; the fake is not exercising the real path")
	}
	if fake.seen("CompleteMultipartUpload") {
		t.Error("INVARIANT 1 VIOLATED: completed a multipart upload from a failed transfer")
	}
	if !fake.seen("AbortMultipartUpload") {
		t.Error("INVARIANT 6 NOT COVERED: no AbortMultipartUpload reached the server")
	}
}
