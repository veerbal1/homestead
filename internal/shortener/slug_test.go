package shortener

import (
	"strings"
	"testing"
)

func TestGenerateSlug(t *testing.T) {
	for i := 0; i < 1000; i++ {
		slug, err := GenerateSlug(5)
		if err != nil {
			t.Fatalf("iteration %d: GenerateSlug(5) unexpected error: %v", i, err)
		}
		if len(slug) != 5 {
			t.Fatalf("iteration %d: GenerateSlug(5) length = %d, want 5", i, len(slug))
		}
		for _, r := range slug {
			if !strings.ContainsRune(alphabet, r) {
				t.Fatalf("iteration %d: GenerateSlug(5) = %q, rune %q not in alphabet", i, slug, r)
			}
		}
	}
}
