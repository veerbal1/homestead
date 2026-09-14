package shortener

import (
	"strings"
	"testing"
)

func TestCleanLink(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		want    string
		wantErr string
	}{
		{
			name:   "valid https url unchanged",
			rawURL: "https://example.com",
			want:   "https://example.com",
		},
		{
			name:   "valid https url with path unchanged",
			rawURL: "https://example.com/some/path",
			want:   "https://example.com/some/path",
		},
		{
			name:   "missing scheme defaults to https",
			rawURL: "example.com",
			want:   "https://example.com",
		},
		{
			name:   "missing scheme with path defaults to https",
			rawURL: "example.com/foo/bar",
			want:   "https://example.com/foo/bar",
		},
		{
			name:   "http scheme preserved",
			rawURL: "http://example.com",
			want:   "http://example.com",
		},
		{
			name:   "uppercase scheme and host lowercased",
			rawURL: "HTTPS://EXAMPLE.COM",
			want:   "https://example.com",
		},
		{
			name:   "mixed case scheme not double-prefixed",
			rawURL: "HtTp://example.com",
			want:   "http://example.com",
		},
		{
			name:   "uppercase host lowercased path case preserved",
			rawURL: "https://Example.COM/Some/Path",
			want:   "https://example.com/Some/Path",
		},
		{
			name:   "root slash stripped",
			rawURL: "https://example.com/",
			want:   "https://example.com",
		},
		{
			name:   "surrounding whitespace trimmed",
			rawURL: "  https://example.com  ",
			want:   "https://example.com",
		},
		{
			name:   "whitespace with missing scheme",
			rawURL: "  example.com  ",
			want:   "https://example.com",
		},
		{
			name:   "query preserved when root slash stripped",
			rawURL: "https://example.com/?q=1",
			want:   "https://example.com?q=1",
		},
		{
			name:   "query and path preserved",
			rawURL: "https://example.com/search?q=go",
			want:   "https://example.com/search?q=go",
		},
		{
			name:    "empty string errors",
			rawURL:  "",
			wantErr: "cleanlink: empty url",
		},
		{
			name:    "whitespace only errors",
			rawURL:  "   ",
			wantErr: "cleanlink: empty url",
		},
		{
			name:    "scheme only has no host",
			rawURL:  "https://",
			wantErr: "no host",
		},
		{
			name:    "malformed url parse error",
			rawURL:  "http://[::1:80",
			wantErr: "cleanlink:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CleanLink(tt.rawURL)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("CleanLink(%q) = %q, want error containing %q", tt.rawURL, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("CleanLink(%q) error = %q, want error containing %q", tt.rawURL, err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CleanLink(%q) unexpected error: %v", tt.rawURL, err)
			}
			if got != tt.want {
				t.Errorf("CleanLink(%q) = %q, want %q", tt.rawURL, got, tt.want)
			}
		})
	}
}
