package simplecloud

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

type stubReadCloser struct {
	r   io.Reader
	err error
}

func (s stubReadCloser) Read(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	return s.r.Read(p)
}

func (s stubReadCloser) Close() error { return nil }

// blazer's b2err is unexported, so the 404 leg is only reachable against a
// live bucket. What is testable here is that the wrapper is otherwise inert:
// it must not turn an unrelated failure into a missing object, and must not
// disturb a healthy read.
func TestB2NotFoundReaderLeavesOtherErrorsAlone(t *testing.T) {
	want := errors.New("connection reset")
	reader := b2NotFoundReader{stubReadCloser{err: want}}

	_, err := reader.Read(make([]byte, 8))
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unrelated failure reported as a missing object: %v", err)
	}
}

func TestB2NotFoundReaderPassesContentThrough(t *testing.T) {
	const want = "payload"
	reader := b2NotFoundReader{stubReadCloser{r: strings.NewReader(want)}}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestErrNotExistIsBothThings(t *testing.T) {
	cause := errors.New("GET /obj: 404 Not Found")
	err := errNotExist(cause)

	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%v does not satisfy os.ErrNotExist", err)
	}
	if !errors.Is(err, cause) {
		t.Errorf("%v lost its cause", err)
	}
}
