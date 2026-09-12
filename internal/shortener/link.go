package shortener

import (
	"fmt"
	"net/url"
	"strings"
)

func CleanLink(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("cleanlink: empty url")
	}

	lower := strings.ToLower(rawURL)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		rawURL = "https://" + rawURL
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("cleanlink: parse %q: %w", rawURL, err)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("cleanlink: no host in %q", rawURL)
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)

	if parsed.Path == "/" {
		parsed.Path = ""
	}

	return parsed.String(), nil
}
