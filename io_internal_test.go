package simplecloud

import "testing"

func TestCleanPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"b2 url", "b2://mtgban-dumps/magic/hareruya_sealed/retail/HASealed.json.xz", "/magic/hareruya_sealed/retail/HASealed.json.xz"},
		{"gs url", "gs://bucket/a/b/c.json.gz", "/a/b/c.json.gz"},
		{"s3 url", "s3://bucket/key.csv", "/key.csv"},
		{"http url strips host and query", "https://host.example/a/b.xz?sig=abc", "/a/b.xz"},
		{"scheme without path", "b2://bucket", ""},
		{"local absolute path untouched", "/data/retail/foo.json.xz", "/data/retail/foo.json.xz"},
		{"local relative path untouched", "retail/foo.json.xz", "retail/foo.json.xz"},
		{"local query stripped", "/data/foo.gz?x=1", "/data/foo.gz"},
		{"percent preserved in key", "b2://bucket/dir/a%b.gz", "/dir/a%b.gz"},
		{"hash preserved in key", "b2://bucket/dir/a#b.gz", "/dir/a#b.gz"},
		{"percent preserved local", "/data/a%b.gz", "/data/a%b.gz"},
		{"colon preserved in key", "2024:report.gz", "2024:report.gz"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanPath(tt.in); got != tt.want {
				t.Errorf("cleanPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
