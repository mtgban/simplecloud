package simplecloud_test

import (
	"context"
	"errors"
	"io"
	"iter"
	"maps"
	"os"
	"path/filepath"
	"slices"
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

// Lister is optional, and the documented contract is that the HTTP backend
// does not implement it. Go cannot assert the negative at compile time, so it
// is pinned here: accidentally adding List to it would change the documented
// API surface silently.
func TestList_HTTPDoesNotImplementLister(t *testing.T) {
	if _, ok := any(&simplecloud.HTTPBucket{}).(simplecloud.Lister); ok {
		t.Error("HTTPBucket implements Lister; HTTP has no listing operation")
	}
}

// ---- FileBucket.List ---------------------------------------------------

// newFileListFixture lays out a small nested tree under a fresh t.TempDir()
// and returns its root plus the content written to each file, keyed by the
// path relative to root (forward-slashed, matching a listed Key once made
// relative to root).
func newFileListFixture(t *testing.T) (root string, files map[string]string) {
	t.Helper()
	root = t.TempDir()
	files = map[string]string{
		"dumps/lorcana/coolstuffinc/retail/CSI.json.xz": "csi-data",
		"dumps/lorcana/cardmarket/retail/CM.json.xz":    "cm-data",
		"dumps/magic/tcgplayer/retail/TCG.json.xz":      "tcg-data",
		"other.txt": "unrelated",
	}
	for rel, content := range files {
		writeFile(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
	return root, files
}

// assertFileList runs bucket.List(ctx, prefix), failing on any error, and
// checks the returned keys against wantRel (relative to root; both sides are
// sorted, since order is not part of the contract). Every returned object is
// also round-tripped through NewReader against the content it was written
// with, and checked for a non-zero Size and LastModified.
func assertFileList(t *testing.T, bucket *simplecloud.FileBucket, root string, files map[string]string, prefix string, wantRel []string) {
	t.Helper()

	var gotRel []string
	for obj, err := range bucket.List(ctx, prefix) {
		if err != nil {
			t.Fatalf("List(%q): %v", prefix, err)
		}

		// obj.Key is only relative to root as a filesystem path, not as a
		// string: an absolute prefix yields absolute keys, while listing ""
		// against a chdir'd root yields keys already relative to it.
		rel := obj.Key
		if filepath.IsAbs(rel) {
			var err error
			rel, err = filepath.Rel(root, obj.Key)
			if err != nil {
				t.Fatalf("key %q is not under root %q: %v", obj.Key, root, err)
			}
		}
		rel = filepath.ToSlash(rel)
		gotRel = append(gotRel, rel)

		content, ok := files[rel]
		if !ok {
			t.Fatalf("unexpected key %q (relative %q)", obj.Key, rel)
		}
		if obj.Size != int64(len(content)) {
			t.Errorf("%q: Size = %d, want %d", rel, obj.Size, len(content))
		}
		if obj.LastModified.IsZero() {
			t.Errorf("%q: LastModified is zero", rel)
		}
		if obj.LastModified.After(time.Now().Add(time.Minute)) {
			t.Errorf("%q: LastModified %s is in the future", rel, obj.LastModified)
		}

		r, err := bucket.NewReader(ctx, obj.Key)
		if err != nil {
			t.Fatalf("NewReader(%q): %v", obj.Key, err)
		}
		if got := readAll(t, r); got != content {
			t.Errorf("%q: round-tripped content = %q, want %q", rel, got, content)
		}
	}

	slices.Sort(gotRel)
	wantSorted := slices.Clone(wantRel)
	slices.Sort(wantSorted)
	if !slices.Equal(gotRel, wantSorted) {
		t.Fatalf("List(%q) keys = %v, want %v", prefix, gotRel, wantSorted)
	}
}

func TestFileBucket_List_PrefixMatching(t *testing.T) {
	root, files := newFileListFixture(t)
	bucket := &simplecloud.FileBucket{}

	lorcana := []string{
		"dumps/lorcana/coolstuffinc/retail/CSI.json.xz",
		"dumps/lorcana/cardmarket/retail/CM.json.xz",
	}

	cases := []struct {
		name   string
		prefix string // joined onto root + "/" below; "" lists root itself
		want   []string
	}{
		{
			name:   "nested directory prefix without trailing slash",
			prefix: "dumps/lorcana",
			want:   lorcana,
		},
		{
			name:   "nested directory prefix with trailing slash",
			prefix: "dumps/lorcana/",
			want:   lorcana,
		},
		{
			// The "./" sits after root, so it never leads the joined path,
			// but filepath.Clean drops a "." element wherever it falls.
			name:   "dot-slash segment in an otherwise clean prefix",
			prefix: "./dumps/lorcana/",
			want:   lorcana,
		},
		{
			name:   "doubled separator in the prefix",
			prefix: "dumps//lorcana/",
			want:   lorcana,
		},
		{
			name:   "dot-dot segment in the prefix",
			prefix: "dumps/magic/../lorcana/",
			want:   lorcana,
		},
		{
			name:   "partial-name prefix",
			prefix: "dumps/lorcana/cool",
			want:   []string{"dumps/lorcana/coolstuffinc/retail/CSI.json.xz"},
		},
		{
			name:   "whole tree",
			prefix: "",
			want: []string{
				"dumps/lorcana/coolstuffinc/retail/CSI.json.xz",
				"dumps/lorcana/cardmarket/retail/CM.json.xz",
				"dumps/magic/tcgplayer/retail/TCG.json.xz",
				"other.txt",
			},
		},
		{
			// "dumps" exists, so this exercises a walk that finds the
			// directory but nothing under it matching the prefix.
			name:   "no match within an existing directory",
			prefix: "dumps/riftbound",
			want:   nil,
		},
		{
			// Unlike the case above, "dumps/riftbound" itself does not
			// exist: the trailing slash makes it the walk's own root, so
			// this exercises the missing-directory path instead.
			name:   "missing directory yields nothing and no error",
			prefix: "dumps/riftbound/",
			want:   nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prefix := root + "/" + tc.prefix
			assertFileList(t, bucket, root, files, prefix, tc.want)
		})
	}
}

// TestFileBucket_List_EmptyPrefix pins the literal-empty-string case, which
// SPECIFICATIONS §10.4 documents as listing the working directory rather than
// a bucket root: it needs a real chdir, so it is kept separate from the
// table above, which always joins a relative prefix onto an absolute root.
func TestFileBucket_List_EmptyPrefix(t *testing.T) {
	root, files := newFileListFixture(t)
	t.Chdir(root)

	bucket := &simplecloud.FileBucket{}
	assertFileList(t, bucket, root, files, "", slices.Collect(maps.Keys(files)))
}

func TestFileBucket_List_BreakStopsIteration(t *testing.T) {
	root, _ := newFileListFixture(t)
	bucket := &simplecloud.FileBucket{}

	// The fixture has more than one matching file, so a bug that kept
	// walking after a false yield would attempt a second yield call, which
	// range-over-func turns into a runtime panic rather than a silent extra
	// iteration.
	n := 0
	for range bucket.List(ctx, root+"/") {
		n++
		break
	}
	if n != 1 {
		t.Fatalf("iterator produced %d objects after break, want 1", n)
	}
}

func TestFileBucket_List_CancelledContext(t *testing.T) {
	root, _ := newFileListFixture(t)
	bucket := &simplecloud.FileBucket{}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	var seen int
	var gotErr error
	for obj, err := range bucket.List(cancelled, root+"/") {
		seen++
		if err != nil {
			gotErr = err
			if obj.Key != "" {
				t.Errorf("error pair carried a key %q, want zero value", obj.Key)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("iteration produced %d pairs, want 1 (just the cancellation error)", seen)
	}
	if !errors.Is(gotErr, context.Canceled) {
		t.Errorf("got error %v, want one wrapping context.Canceled", gotErr)
	}
}

func TestFileBucket_List_Symlinks(t *testing.T) {
	root, _ := newFileListFixture(t)
	bucket := &simplecloud.FileBucket{}

	// A symlink to a regular file is followed and yielded, sized and stamped
	// from the target rather than the link itself.
	target := filepath.Join(root, filepath.FromSlash("dumps/lorcana/coolstuffinc/retail/CSI.json.xz"))
	targetInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link-to-csi.json.xz")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	// A symlink to a directory must not be recursed into, matching
	// filepath.WalkDir's own behaviour for a symlinked directory.
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "hidden.txt"), "should not be listed")
	if err := os.Symlink(outside, filepath.Join(root, "dumps", "lorcana-alias")); err != nil {
		t.Fatal(err)
	}

	// A dangling symlink has nothing to list, and is skipped without an error.
	if err := os.Symlink(filepath.Join(root, "missing.json.xz"), filepath.Join(root, "dangling.json.xz")); err != nil {
		t.Fatal(err)
	}

	var sawLink bool
	for obj, err := range bucket.List(ctx, root+"/") {
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		rel, relErr := filepath.Rel(root, obj.Key)
		if relErr != nil {
			t.Fatalf("key %q not under root: %v", obj.Key, relErr)
		}
		rel = filepath.ToSlash(rel)

		if strings.Contains(rel, "lorcana-alias") {
			t.Errorf("List recursed into a symlinked directory: %q", rel)
		}
		if rel == "dangling.json.xz" {
			t.Errorf("List yielded a dangling symlink: %q", rel)
		}
		if rel == "link-to-csi.json.xz" {
			sawLink = true
			if obj.Size != targetInfo.Size() {
				t.Errorf("symlink Size = %d, want target's %d", obj.Size, targetInfo.Size())
			}
			if !obj.LastModified.Equal(targetInfo.ModTime()) {
				t.Errorf("symlink LastModified = %s, want target's %s", obj.LastModified, targetInfo.ModTime())
			}
		}
	}
	if !sawLink {
		t.Error("List did not yield the symlink to a regular file")
	}
}

// ---- FileBucket with a Root ---------------------------------------------

// TestFileBucket_Root_RoundTrip writes a bucket-shaped tree through NewWriter,
// lists prefixes of it and reads every listed key back through NewReader, all
// under a Root and with no chdir. The keys must be exactly what a bucket
// returns: relative to Root and slash-separated.
func TestFileBucket_Root_RoundTrip(t *testing.T) {
	root := t.TempDir()
	bucket := &simplecloud.FileBucket{Root: root}

	files := map[string]string{
		"magic/tcgplayer/retail/TCG.json.xz":         "tcg-data",
		"magic/cardkingdom/buylist/CK.json.xz":       "ck-data",
		"magic-archive/tcgplayer/retail/TCG.json.xz": "archived-tcg-data",
		"lorcana/coolstuffinc/retail/CSI.json.xz":    "csi-data",
		"mage.txt": "partial-name sibling",
	}
	for key, content := range files {
		w, err := bucket.NewWriter(ctx, key)
		if err != nil {
			t.Fatalf("NewWriter(%q): %v", key, err)
		}
		_, err = io.WriteString(w, content)
		if err != nil {
			t.Fatal(err)
		}
		err = w.Close()
		if err != nil {
			t.Fatal(err)
		}

		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(key)))
		if err != nil {
			t.Fatalf("%q was not written under Root: %v", key, err)
		}
		if string(raw) != content {
			t.Fatalf("%q under Root holds %q, want %q", key, raw, content)
		}
	}

	magic := []string{
		"magic/tcgplayer/retail/TCG.json.xz",
		"magic/cardkingdom/buylist/CK.json.xz",
	}
	magicPartial := append(slices.Clone(magic), "magic-archive/tcgplayer/retail/TCG.json.xz")
	all := slices.Collect(maps.Keys(files))

	cases := []struct {
		name   string
		prefix string
		want   []string
	}{
		{name: "directory with trailing slash", prefix: "magic/", want: magic},
		{name: "partial name", prefix: "magic", want: magicPartial},
		{name: "shorter partial name", prefix: "mag", want: append(slices.Clone(magicPartial), "mage.txt")},
		{name: "empty prefix lists all of Root", prefix: "", want: all},
		{name: "dot lists all of Root", prefix: ".", want: all},
		{name: "leading slash is ignored", prefix: "/magic/", want: magic},
		{name: "lone slash lists all of Root", prefix: "/", want: all},
		{
			name:   "unclean prefix",
			prefix: "./magic//tcgplayer/../cardkingdom/",
			want:   []string{"magic/cardkingdom/buylist/CK.json.xz"},
		},
		{
			name:   "full key",
			prefix: "lorcana/coolstuffinc/retail/CSI.json.xz",
			want:   []string{"lorcana/coolstuffinc/retail/CSI.json.xz"},
		},
		{name: "no match within an existing directory", prefix: "magic/starcitygames", want: nil},
		{name: "missing directory", prefix: "riftbound/", want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for obj, err := range bucket.List(ctx, tc.prefix) {
				if err != nil {
					t.Fatalf("List(%q): %v", tc.prefix, err)
				}
				got = append(got, obj.Key)

				content, ok := files[obj.Key]
				if !ok {
					t.Fatalf("List(%q) yielded %q, which is not a key relative to Root", tc.prefix, obj.Key)
				}
				if obj.Size != int64(len(content)) {
					t.Errorf("%q: Size = %d, want %d", obj.Key, obj.Size, len(content))
				}
				r, err := bucket.NewReader(ctx, obj.Key)
				if err != nil {
					t.Fatalf("NewReader(%q): %v", obj.Key, err)
				}
				data := readAll(t, r)
				if data != content {
					t.Errorf("%q: read back %q, want %q", obj.Key, data, content)
				}
			}

			slices.Sort(got)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("List(%q) keys = %v, want %v", tc.prefix, got, want)
			}
		})
	}
}

func TestFileBucket_Root_MissingRootListsNothing(t *testing.T) {
	bucket := &simplecloud.FileBucket{Root: filepath.Join(t.TempDir(), "missing")}

	for _, prefix := range []string{"", "magic/"} {
		for obj, err := range bucket.List(ctx, prefix) {
			t.Errorf("List(%q) on a missing Root yielded (%q, %v), want nothing", prefix, obj.Key, err)
		}
	}
}

func TestFileBucket_Root_ListSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "magic", "TCG.json.xz")
	writeFile(t, target, "tcg-data")
	targetInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}

	// Relative links inside Root: to a file, which is listed under the link's
	// own key; to a directory, which is not recursed into; and a dangling one,
	// which is skipped.
	links := map[string]string{
		"magic/latest.json.xz":   "TCG.json.xz",
		"magic-alias":            "magic",
		"magic/dangling.json.xz": "missing.json.xz",
	}
	for link, dest := range links {
		err := os.Symlink(dest, filepath.Join(root, filepath.FromSlash(link)))
		if err != nil {
			t.Fatal(err)
		}
	}

	bucket := &simplecloud.FileBucket{Root: root}
	listed := func(t *testing.T, prefix string) map[string]simplecloud.ObjectInfo {
		t.Helper()
		got := map[string]simplecloud.ObjectInfo{}
		for obj, err := range bucket.List(ctx, prefix) {
			if err != nil {
				t.Fatalf("List(%q): %v", prefix, err)
			}
			got[obj.Key] = obj
		}
		return got
	}

	got := listed(t, "")
	want := []string{"magic/TCG.json.xz", "magic/latest.json.xz"}
	if !slices.Equal(slices.Sorted(maps.Keys(got)), want) {
		t.Fatalf("List keys = %v, want %v", slices.Sorted(maps.Keys(got)), want)
	}
	link := got["magic/latest.json.xz"]
	if link.Size != targetInfo.Size() || !link.LastModified.Equal(targetInfo.ModTime()) {
		t.Errorf("link listed with size %d at %s, want the target's %d at %s",
			link.Size, link.LastModified, targetInfo.Size(), targetInfo.ModTime())
	}

	// The directory a walk starts from is walked even when it is a link.
	got = listed(t, "magic-alias/")
	want = []string{"magic-alias/TCG.json.xz", "magic-alias/latest.json.xz"}
	if !slices.Equal(slices.Sorted(maps.Keys(got)), want) {
		t.Errorf("List(%q) keys = %v, want %v", "magic-alias/", slices.Sorted(maps.Keys(got)), want)
	}

	// A link os.Root will not follow ends the listing with an error: one out
	// of Root, and an absolute one even when it points back inside.
	refused := func(t *testing.T, root string) {
		t.Helper()
		var gotErr error
		for obj, err := range (&simplecloud.FileBucket{Root: root}).List(ctx, "") {
			if err != nil {
				gotErr = err
				continue
			}
			if obj.Key == "link.json.xz" {
				t.Errorf("List yielded %q, a link os.Root refuses", obj.Key)
			}
		}
		if gotErr == nil {
			t.Fatal("List followed the link without an error")
		}
		t.Logf("List: %v", gotErr)
	}

	t.Run("relative link out of Root", func(t *testing.T) {
		root, outside := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(outside, "secret.json.xz"), "outside Root")
		up, err := filepath.Rel(root, outside)
		if err != nil {
			t.Fatal(err)
		}
		err = os.Symlink(filepath.Join(up, "secret.json.xz"), filepath.Join(root, "link.json.xz"))
		if err != nil {
			t.Fatal(err)
		}
		refused(t, root)
	})

	t.Run("absolute link back inside Root", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "a.json.xz"), "inside Root")
		err := os.Symlink(filepath.Join(root, "a.json.xz"), filepath.Join(root, "link.json.xz"))
		if err != nil {
			t.Fatal(err)
		}
		refused(t, root)
	})
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
