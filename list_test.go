package simplecloud_test

import (
	"context"
	"errors"
	"iter"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mtgban/simplecloud"
)

// listBucket is a Lister whose pages and failure point are controlled by the
// test, standing in for a backend's paginator.
type listBucket struct {
	keys     []string
	failAt   int // index at which to yield an error; -1 for never
	consumed int // how many objects the iterator actually produced
}

func (b *listBucket) List(_ context.Context, prefix string) iter.Seq2[simplecloud.ObjectInfo, error] {
	return func(yield func(simplecloud.ObjectInfo, error) bool) {
		for i, k := range b.keys {
			if !strings.HasPrefix(k, strings.TrimLeft(prefix, "/")) {
				continue
			}
			if i == b.failAt {
				yield(simplecloud.ObjectInfo{}, errors.New("simulated page failure"))
				return
			}
			b.consumed++
			if !yield(simplecloud.ObjectInfo{Key: k, Size: int64(len(k))}, nil) {
				return
			}
		}
	}
}

func TestList_PrefixFilteringAndFields(t *testing.T) {
	b := &listBucket{keys: []string{"magic/a.json.xz", "magic/b.json.xz", "pokemon/c.json.xz"}, failAt: -1}

	var got []string
	for obj, err := range b.List(ctx, "magic/") {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got = append(got, obj.Key)
		if obj.Size != int64(len(obj.Key)) {
			t.Errorf("Size not carried through for %q", obj.Key)
		}
	}

	want := []string{"magic/a.json.xz", "magic/b.json.xz"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestList_LeadingSlashPrefixIsStripped(t *testing.T) {
	// A prefix is normalised the same way keys are elsewhere in the package.
	b := &listBucket{keys: []string{"magic/a.json.xz"}, failAt: -1}

	n := 0
	for _, err := range b.List(ctx, "/magic/") {
		if err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != 1 {
		t.Fatalf("got %d objects for a leading-slash prefix, want 1", n)
	}
}

func TestList_BreakStopsIteration(t *testing.T) {
	// Abandoning the iterator must stop it, so a caller that wants one object
	// does not pay for the rest of the bucket.
	b := &listBucket{keys: []string{"a", "b", "c", "d"}, failAt: -1}

	for range b.List(ctx, "") {
		break
	}
	if b.consumed != 1 {
		t.Fatalf("iterator produced %d objects after break, want 1", b.consumed)
	}
}

func TestList_ErrorIsYieldedAndEndsIteration(t *testing.T) {
	// A failure surfaces as a final pair with a non-nil error, and nothing
	// follows it.
	b := &listBucket{keys: []string{"a", "b", "c"}, failAt: 1}

	var seen int
	var gotErr error
	for obj, err := range b.List(ctx, "") {
		seen++
		if err != nil {
			gotErr = err
			if obj.Key != "" {
				t.Errorf("error pair carried a key %q, want zero value", obj.Key)
			}
		}
	}
	if gotErr == nil {
		t.Fatal("expected an error from the iterator, got none")
	}
	if seen != 2 {
		t.Fatalf("iteration produced %d pairs, want 2 (one object then the error)", seen)
	}
}

// Lister is optional, and the documented contract is that the filesystem and
// HTTP backends do not implement it. Go cannot assert the negative at compile
// time, so it is pinned here: accidentally adding List to either would change
// the documented API surface silently.
func TestList_FileAndHTTPDoNotImplementLister(t *testing.T) {
	if _, ok := any(&simplecloud.FileBucket{}).(simplecloud.Lister); ok {
		t.Error("FileBucket implements Lister; SPECIFICATIONS §10 says it does not")
	}
	if _, ok := any(&simplecloud.HTTPBucket{}).(simplecloud.Lister); ok {
		t.Error("HTTPBucket implements Lister; HTTP has no listing operation")
	}
}

// TestList_LiveB2 exercises the real B2 implementation. It is skipped unless
// credentials are present, so CI does not depend on it — but mocks have been
// insufficient in this package before, and the offline tests above only prove
// a fake, so every property claimed for the live backend is pinned here rather
// than measured by hand once.
//
//	B2_APPLICATION_KEY_ID_DATASTORE=... B2_APPLICATION_KEY_DATASTORE=... \
//	  SIMPLECLOUD_TEST_B2_BUCKET=my-bucket go test -run TestList_LiveB2 -v
func TestList_LiveB2(t *testing.T) {
	id := os.Getenv("B2_APPLICATION_KEY_ID_DATASTORE")
	key := os.Getenv("B2_APPLICATION_KEY_DATASTORE")
	bucket := os.Getenv("SIMPLECLOUD_TEST_B2_BUCKET")
	if id == "" || key == "" || bucket == "" {
		t.Skip("B2 credentials or bucket not set")
	}

	ctx := context.Background()
	b, err := simplecloud.NewB2Client(ctx, id, key, bucket)
	if err != nil {
		t.Fatal(err)
	}

	// collect gathers up to limit keys under prefix, failing on any error.
	collect := func(t *testing.T, prefix string, limit int) []simplecloud.ObjectInfo {
		t.Helper()
		var got []simplecloud.ObjectInfo
		for obj, err := range b.List(ctx, prefix) {
			if err != nil {
				t.Fatalf("List(%q): %v", prefix, err)
			}
			got = append(got, obj)
			if len(got) >= limit {
				break
			}
		}
		return got
	}

	sample := collect(t, "", 25)
	if len(sample) == 0 {
		t.Skip("bucket is empty; nothing to assert")
	}
	t.Logf("listed %d objects from %q", len(sample), bucket)

	t.Run("fields are populated", func(t *testing.T) {
		for _, obj := range sample {
			if obj.Key == "" {
				t.Error("empty key in listing")
			}
			if obj.LastModified.IsZero() {
				t.Errorf("zero LastModified for %q; the UploadTimestamp fallback should prevent this", obj.Key)
			}
			if obj.LastModified.After(time.Now().Add(time.Hour)) {
				t.Errorf("LastModified in the future for %q: %s", obj.Key, obj.LastModified)
			}
		}
	})

	// Derive a prefix that actually exists rather than hard-coding one, so the
	// test runs against any bucket.
	prefix := ""
	for _, obj := range sample {
		if i := strings.IndexByte(obj.Key, '/'); i >= 0 {
			prefix = obj.Key[:i+1]
			break
		}
	}
	if prefix == "" && len(sample[0].Key) > 1 {
		prefix = sample[0].Key[:1]
	}

	t.Run("prefix filters", func(t *testing.T) {
		if prefix == "" {
			t.Skip("no usable prefix in this bucket")
		}
		got := collect(t, prefix, 100)
		if len(got) == 0 {
			t.Fatalf("prefix %q returned nothing, but was taken from a listed key", prefix)
		}
		for _, obj := range got {
			if !strings.HasPrefix(obj.Key, prefix) {
				t.Errorf("key %q returned for prefix %q", obj.Key, prefix)
			}
		}
	})

	t.Run("leading slash in prefix is equivalent", func(t *testing.T) {
		if prefix == "" {
			t.Skip("no usable prefix in this bucket")
		}
		bare := collect(t, prefix, 100)
		slashed := collect(t, "/"+prefix, 100)

		if len(bare) != len(slashed) {
			t.Fatalf("%q returned %d objects, %q returned %d", prefix, len(bare), "/"+prefix, len(slashed))
		}
		for i := range bare {
			if bare[i].Key != slashed[i].Key {
				t.Errorf("index %d: %q vs %q", i, bare[i].Key, slashed[i].Key)
			}
		}
	})

	t.Run("non-matching prefix is empty and not an error", func(t *testing.T) {
		missing := "simplecloud-no-such-prefix-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "/"
		n := 0
		for _, err := range b.List(ctx, missing) {
			if err != nil {
				t.Fatalf("List(%q) errored on a prefix matching nothing: %v", missing, err)
			}
			n++
		}
		if n != 0 {
			t.Errorf("prefix %q returned %d objects, want 0", missing, n)
		}
	})

	t.Run("break stops iteration cleanly", func(t *testing.T) {
		n := 0
		for _, err := range b.List(ctx, "") {
			if err != nil {
				t.Fatal(err)
			}
			n++
			break
		}
		if n != 1 {
			t.Fatalf("iterator produced %d objects after break, want 1", n)
		}
	})
}
