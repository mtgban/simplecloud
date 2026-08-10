package simplecloud_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mtgban/simplecloud"
)

// Every backend reports a missing object the same way, so that a caller can
// tell "not published yet" from "the read broke" without knowing which one it
// is talking to.

func TestFileBucket_MissingIsErrNotExist(t *testing.T) {
	bucket := &simplecloud.FileBucket{}

	_, err := bucket.NewReader(ctx, filepath.Join(t.TempDir(), "absent.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v, want os.ErrNotExist", err)
	}
}

func TestHTTPBucket_MissingIsErrNotExist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	bucket, err := simplecloud.NewHTTPBucket(nil, srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = bucket.NewReader(ctx, "/missing")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v, want os.ErrNotExist", err)
	}
}

// A broken backend must stay distinguishable from an absent object: the whole
// point of the contract is that only one of the two is routine.
func TestHTTPBucket_ServerErrorIsNotErrNotExist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	bucket, err := simplecloud.NewHTTPBucket(nil, srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = bucket.NewReader(ctx, "/broken")
	if err == nil {
		t.Fatal("expected an error for 500, got nil")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("500 reported as a missing object: %v", err)
	}
}

// The error still says what actually happened; os.ErrNotExist is added to it,
// not substituted for it.
func TestErrNotExistKeepsTheOriginalMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	bucket, err := simplecloud.NewHTTPBucket(nil, srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = bucket.NewReader(ctx, "/missing")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "404") {
		t.Errorf("original status lost from %q", got)
	}
}
