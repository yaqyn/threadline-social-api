package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestChirpDetailSupportsConditionalRequests(t *testing.T) {
	chirp := Chirp{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), UpdatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Body: "hello"}
	first := httptest.NewRecorder()
	respondWithChirp(first, httptest.NewRequest("GET", "/api/chirps/1", nil), chirp)
	etag := first.Header().Get("ETag")
	request := httptest.NewRequest("GET", "/api/chirps/1", nil)
	request.Header.Set("If-None-Match", etag)
	conditional := httptest.NewRecorder()
	respondWithChirp(conditional, request, chirp)
	if etag == "" || conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
		t.Fatalf("etag=%q status=%d body=%q", etag, conditional.Code, conditional.Body.String())
	}
}

func TestChirpDetailCanBeRenderedAsPlainText(t *testing.T) {
	chirp := Chirp{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), UpdatedAt: time.Now(), Body: "hello\nworld"}
	request := httptest.NewRequest("GET", "/api/chirps/1", nil)
	request.Header.Set("Accept", "application/json; q=0.5, text/plain; q=0.8")
	response := httptest.NewRecorder()
	respondWithChirp(response, request, chirp)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" || response.Body.String() != chirp.Body {
		t.Fatalf("status=%d type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	request = httptest.NewRequest("GET", "/api/chirps/1", nil)
	request.Header.Set("Accept", "text/plain; q=0")
	response = httptest.NewRecorder()
	respondWithChirp(response, request, chirp)
	if response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("zero-quality plain text should fall back to JSON: %v", response.Header())
	}
}

func TestChirpDetailPrefersHigherQualityJSON(t *testing.T) {
	chirp := Chirp{ID: uuid.New(), UpdatedAt: time.Now(), Body: "hello"}
	request := httptest.NewRequest("GET", "/api/chirps/1", nil)
	request.Header.Set("Accept", "text/plain;q=0.2, application/json;q=0.9")
	response := httptest.NewRecorder()
	respondWithChirp(response, request, chirp)
	if response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q", response.Header().Get("Content-Type"))
	}
}

func TestPublicDirectoryIsolation(t *testing.T) {
	cfg := &apiConfig{}
	for _, path := range []string{"/app/.env", "/app/main.go", "/app/go.mod", "/app/../.env"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			cfg.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code == http.StatusOK {
				t.Fatalf("private path served: %s", path)
			}
		})
	}
	response := httptest.NewRecorder()
	cfg.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("public page: %d", response.Code)
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing security header")
	}
	if response.Header().Get("Permissions-Policy") == "" || response.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Fatal("missing browser isolation headers")
	}
}

func TestAuthenticationGuards(t *testing.T) {
	cfg := &apiConfig{jwtSecret: "test-secret", polkaKey: "test-key"}
	for _, endpoint := range []struct{ method, path string }{
		{"POST", "/api/chirps"},
		{"DELETE", "/api/chirps/" + "00000000-0000-0000-0000-000000000001"},
		{"PUT", "/api/users"},
		{"POST", "/api/refresh"},
		{"POST", "/api/revoke"},
		{"POST", "/api/polka/webhooks"},
	} {
		t.Run(endpoint.method+endpoint.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			cfg.routes().ServeHTTP(response, httptest.NewRequest(endpoint.method, endpoint.path, nil))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("got %d", response.Code)
			}
		})
	}
	response := httptest.NewRecorder()
	cfg.routes().ServeHTTP(response, httptest.NewRequest("POST", "/admin/reset", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("reset outside dev: %d", response.Code)
	}
}

func TestJSONResponsesDeclareUTF8(t *testing.T) {
	response := httptest.NewRecorder()
	respondWithJSON(response, http.StatusOK, map[string]string{"message": "hello"})
	if response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("content type: %q", response.Header().Get("Content-Type"))
	}
}
