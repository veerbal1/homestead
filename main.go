package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
)

var version = "dev"

type Code string
type URL string

type ShortenRequest struct {
	URL URL `json:"url"`
}

const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func GenerateSlug(length int) (string, error) {
	result := make([]byte, length)
	alphabetLength := big.NewInt(int64(len(alphabet)))

	for i := 0; i < length; i++ {
		num, err := rand.Int(rand.Reader, alphabetLength)
		if err != nil {
			return "", err
		}
		result[i] = alphabet[num.Int64()]
	}

	return string(result), nil
}

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

type JSONResponse struct {
	Code Code `json:"code"`
}

func main() {
	urlsMap := make(map[Code]URL)

	http.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s", version)
	})

	http.HandleFunc("GET /r/{code}", func(w http.ResponseWriter, r *http.Request) {
		codeStr := r.PathValue("code")
		if strings.TrimSpace(string(codeStr)) == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}

		url, found := urlsMap[Code(codeStr)]
		if !found {
			http.Error(w, "invalid code", http.StatusNotFound)
			return
		}

		http.Redirect(w, r, string(url), http.StatusTemporaryRedirect)
	})

	http.HandleFunc("POST /shorten", func(w http.ResponseWriter, r *http.Request) {
		var requestBody ShortenRequest
		err := json.NewDecoder(r.Body).Decode(&requestBody)
		if err != nil {
			http.Error(w, "failed to decode json", http.StatusBadRequest)
			return
		}

		code, _ := GenerateSlug(6)

		link, err := CleanLink(string(requestBody.URL))
		if err != nil {
			http.Error(w, "failed to get parse URL", http.StatusBadRequest)
			return
		}

		urlsMap[Code(code)] = URL(link)

		err = json.NewEncoder(w).Encode(JSONResponse{
			Code: Code(code),
		})

		if err != nil {
			http.Error(w, "failed to shorten the URL", http.StatusBadRequest)
			return
		}
	})

	fmt.Println("Listening on port :8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatal(err)
	}
}
