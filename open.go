package simplecloud

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// BucketResolver constructs the backend for a URL's scheme and host (the host
// is the URL authority, typically the bucket name). Returning a non-nil Reader
// selects it; returning (nil, nil) falls through to Open's built-in schemes.
//
// A resolver is the extension point for schemes Open does not handle natively,
// and for taking control of client lifecycle — e.g. returning a shared GCSBucket
// whose *storage.Client you close yourself, rather than the per-call client the
// built-in gs scheme creates and never closes.
type BucketResolver func(ctx context.Context, scheme, host string) (Reader, error)

// OpenOption configures Open. Options are applied in order; a later option of
// the same kind overrides an earlier one.
type OpenOption func(*openOptions)

type openOptions struct {
	resolver            BucketResolver
	httpClient          *http.Client
	b2KeyID             string
	b2AppKey            string
	concurrentDownloads int
	s3AccessKey         string
	s3SecretKey         string
	s3Endpoint          string
	s3Region            string
	gcsServiceAccount   string
}

// WithResolver registers a resolver consulted before the built-in schemes, so
// it can add new schemes or override built-in ones. Returning (nil, nil) from
// the resolver falls through to the built-ins.
func WithResolver(r BucketResolver) OpenOption {
	return func(o *openOptions) { o.resolver = r }
}

// WithHTTPClient sets the client used for http and https paths. When unset (or
// set to nil) http.DefaultClient is used.
func WithHTTPClient(client *http.Client) OpenOption {
	return func(o *openOptions) { o.httpClient = client }
}

// WithB2Credentials sets the Backblaze B2 application key id and key used to
// authenticate b2 paths. They are required for the b2 scheme; the library does
// not source credentials from the environment on its own.
func WithB2Credentials(keyID, appKey string) OpenOption {
	return func(o *openOptions) {
		o.b2KeyID = keyID
		o.b2AppKey = appKey
	}
}

// WithConcurrentDownloads sets the number of parallel range requests used for
// b2 downloads. Zero (the default) uses the blazer library default.
func WithConcurrentDownloads(n int) OpenOption {
	return func(o *openOptions) { o.concurrentDownloads = n }
}

// WithS3Credentials sets the access key and secret key used for s3 paths. If
// left empty, the default AWS credential chain (environment, shared config,
// instance role, …) is used.
func WithS3Credentials(accessKey, secretKey string) OpenOption {
	return func(o *openOptions) {
		o.s3AccessKey = accessKey
		o.s3SecretKey = secretKey
	}
}

// WithS3Endpoint targets an S3-compatible endpoint (e.g. Cloudflare R2, MinIO)
// for s3 paths; path-style addressing is enabled automatically when set.
func WithS3Endpoint(endpoint string) OpenOption {
	return func(o *openOptions) { o.s3Endpoint = endpoint }
}

// WithS3Region sets the region for s3 paths. Empty defaults to "auto".
func WithS3Region(region string) OpenOption {
	return func(o *openOptions) { o.s3Region = region }
}

// WithGCSServiceAccount sets the service-account JSON file used for gs paths.
// Empty uses Application Default Credentials.
func WithGCSServiceAccount(path string) OpenOption {
	return func(o *openOptions) { o.gcsServiceAccount = path }
}

// Open opens path for reading, selecting a backend from the URL scheme:
//
//   - (no scheme)  — local filesystem
//   - http, https  — HTTP(S); the scheme and host of path form the base URL
//   - b2           — Backblaze B2; requires WithB2Credentials
//   - s3           — Amazon S3 or S3-compatible; see WithS3Credentials/Endpoint/Region
//   - gs           — Google Cloud Storage; see WithGCSServiceAccount
//
// For b2, s3, and gs the host is the bucket name. Any other scheme must be
// handled by a WithResolver resolver, which is also consulted first and so can
// override the built-ins. Compression is applied transparently from the path
// extension and any query string is dropped, exactly as in InitReader.
//
// The scheme and host come from url.Parse. A path with no host, or one that
// url.Parse rejects (for example a local path containing a bare '%'), is
// treated as a local filesystem path. Because the original path is passed to
// InitReader, a '#' in an object key is preserved; a remote key containing a
// bare '%' is not addressable through Open, so percent-encode it or construct
// the backend directly.
//
// The s3 and gs schemes construct a client per call and never close it — fine
// for short-lived use, but long-running callers should supply a WithResolver
// that returns a backend whose client they manage. The caller must close the
// returned ReadCloser.
func Open(ctx context.Context, path string, opts ...OpenOption) (io.ReadCloser, error) {
	var o openOptions
	for _, opt := range opts {
		opt(&o)
	}

	// url.Parse identifies the scheme and host; a path with no host (a local
	// file, or a "scheme:opaque" form) or one url.Parse rejects is opened from
	// the local filesystem. The original path still goes to InitReader, whose
	// cleanPath preserves the object key.
	var u *url.URL
	parsed, err := url.Parse(path)
	if err == nil && parsed.Host != "" {
		u = parsed
	}

	bucket, err := o.bucketFor(ctx, u, path)
	if err != nil {
		return nil, err
	}

	return InitReader(ctx, bucket, path)
}

// bucketFor resolves the backend for a URL, consulting a caller-supplied
// resolver before the built-in schemes. A nil u means no scheme was detected,
// selecting the local filesystem.
func (o *openOptions) bucketFor(ctx context.Context, u *url.URL, path string) (Reader, error) {
	scheme, host := "", ""
	if u != nil {
		scheme, host = u.Scheme, u.Host
	}

	if o.resolver != nil {
		bucket, err := o.resolver(ctx, scheme, host)
		if err != nil {
			return nil, err
		}
		if bucket != nil {
			return bucket, nil
		}
	}

	switch scheme {
	case "":
		return &FileBucket{}, nil
	case "http", "https":
		client := o.httpClient
		if client == nil {
			client = http.DefaultClient
		}
		// Base URL is scheme://host (with any userinfo) and no path; InitReader
		// passes the object path, which HTTPBucket joins onto it.
		base := &url.URL{Scheme: u.Scheme, Host: u.Host, User: u.User}
		return &HTTPBucket{Client: client, URL: base}, nil
	case "b2":
		client, err := NewB2Client(ctx, o.b2KeyID, o.b2AppKey, host)
		if err != nil {
			return nil, err
		}
		client.ConcurrentDownloads = o.concurrentDownloads
		return client, nil
	case "s3":
		return NewS3Client(ctx, o.s3AccessKey, o.s3SecretKey, host, o.s3Endpoint, o.s3Region)
	case "gs":
		return NewGCSClient(ctx, o.gcsServiceAccount, host)
	default:
		return nil, fmt.Errorf("simplecloud: unsupported scheme %q in %q", scheme, path)
	}
}
