package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yaqyn/threadline-social-api/internal/auth"

	"github.com/google/uuid"
	"github.com/yaqyn/threadline-social-api/internal/database"
)

// Requires a migrated disposable database; never resets shared data.
func TestAPIIntegration(t *testing.T) {
	url := os.Getenv("TEST_DB_URL")
	if url == "" {
		t.Skip("set TEST_DB_URL to run PostgreSQL integration checks")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := &apiConfig{dbQueries: database.New(db), jwtSecret: strings.Repeat("x", 32), polkaKey: "integration-key"}
	router := cfg.routes()
	request := func(method, path string, payload any, authorization string, want int) map[string]any {
		t.Helper()
		var body bytes.Buffer
		if payload != nil {
			if err := json.NewEncoder(&body).Encode(payload); err != nil {
				t.Fatal(err)
			}
		}
		r := httptest.NewRequest(method, path, &body)
		if payload != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		if want == 204 {
			if w.Body.Len() != 0 {
				t.Fatal("204 has body")
			}
			return nil
		}
		var result map[string]any
		if strings.HasPrefix(w.Body.String(), "{") {
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	email := "integration-" + uuid.NewString() + "@example.com"
	credentials := map[string]string{"email": email, "password": "test-password"}
	user := request("POST", "/api/users", credentials, "", 201)
	id := user["id"].(string)
	defer db.Exec("DELETE FROM users WHERE id=$1", id)
	if user["is_chirpy_red"] != false || user["hashed_password"] != nil {
		t.Fatal("unsafe user response")
	}
	request("POST", "/api/users", credentials, "", 409)
	request("POST", "/api/login", map[string]string{"email": email, "password": "wrong"}, "", 401)
	login := request("POST", "/api/login", credentials, "", 200)
	access := "Bearer " + login["token"].(string)
	refresh := "Bearer " + login["refresh_token"].(string)
	createBody := bytes.NewBufferString(`{"body":"hello KERFUFFLE"}`)
	createRequest := httptest.NewRequest("POST", "/api/chirps", createBody)
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.Header.Set("Authorization", access)
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(createResponse, createRequest)
	var chirp map[string]any
	if err := json.Unmarshal(createResponse.Body.Bytes(), &chirp); err != nil || createResponse.Code != http.StatusCreated {
		t.Fatalf("create chirp: %d %s", createResponse.Code, createResponse.Body.String())
	}
	if createResponse.Header().Get("Location") != "/api/chirps/"+chirp["id"].(string) {
		t.Fatalf("chirp location: %q", createResponse.Header().Get("Location"))
	}
	if chirp["user_id"] != id || chirp["body"] != "hello ****" {
		t.Fatal("chirp author or cleaning wrong")
	}
	path := "/api/chirps/" + chirp["id"].(string)
	otherToken, err := auth.MakeJWT(uuid.New(), cfg.jwtSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request("DELETE", path, nil, "Bearer "+otherToken, 403)
	request("GET", path, nil, "", 200)
	second := request("POST", "/api/chirps", map[string]string{"body": "second post"}, access, 201)
	searchResponse := httptest.NewRecorder()
	router.ServeHTTP(searchResponse, httptest.NewRequest("GET", "/api/chirps?q=second", nil))
	if searchResponse.Code != http.StatusOK || !strings.Contains(searchResponse.Body.String(), "second post") || strings.Contains(searchResponse.Body.String(), "hello ****") {
		t.Fatalf("chirp search: %d %s", searchResponse.Code, searchResponse.Body.String())
	}
	ndjson := httptest.NewRecorder()
	router.ServeHTTP(ndjson, httptest.NewRequest("GET", "/api/chirps?q=second&format=ndjson", nil))
	if ndjson.Code != http.StatusOK || ndjson.Header().Get("Content-Type") != "application/x-ndjson; charset=utf-8" || !strings.Contains(ndjson.Body.String(), "second post") || strings.HasPrefix(ndjson.Body.String(), "[") {
		t.Fatalf("chirp ndjson: %d %v %s", ndjson.Code, ndjson.Header(), ndjson.Body.String())
	}
	conditionalRequest := httptest.NewRequest("GET", "/api/chirps?q=second&format=ndjson", nil)
	conditionalRequest.Header.Set("If-None-Match", ndjson.Header().Get("ETag"))
	conditional := httptest.NewRecorder()
	router.ServeHTTP(conditional, conditionalRequest)
	if ndjson.Header().Get("ETag") == "" || conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
		t.Fatalf("conditional chirps: %d %v %q", conditional.Code, conditional.Header(), conditional.Body.String())
	}
	for _, test := range []struct {
		query   string
		wantID  string
		hasMore string
	}{
		{"?author_id=" + id + "&sort=asc&limit=1", chirp["id"].(string), "true"},
		{"?author_id=" + id + "&sort=desc&limit=1", second["id"].(string), "true"},
		{"?author_id=" + id + "&sort=asc&limit=1&offset=1", second["id"].(string), "false"},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/api/chirps"+test.query, nil))
		var items []Chirp
		if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(items) != 1 || items[0].ID.String() != test.wantID {
			t.Fatalf("query %s: %d %s", test.query, w.Code, w.Body.String())
		}
		if w.Header().Get("X-Has-More") != test.hasMore {
			t.Fatalf("query %s has X-Has-More=%q, want %q", test.query, w.Header().Get("X-Has-More"), test.hasMore)
		}
		if test.hasMore == "true" && w.Header().Get("X-Next-Offset") != "1" {
			t.Fatalf("query %s next offset=%q", test.query, w.Header().Get("X-Next-Offset"))
		}
		if strings.Contains(test.query, "offset=1") && w.Header().Get("X-Previous-Offset") != "0" {
			t.Fatalf("query %s previous offset=%q", test.query, w.Header().Get("X-Previous-Offset"))
		}
	}
	minimalRequest := httptest.NewRequest("POST", "/api/chirps", bytes.NewBufferString(`{"body":"quiet creation"}`))
	minimalRequest.Header.Set("Content-Type", "application/json")
	minimalRequest.Header.Set("Authorization", access)
	minimalRequest.Header.Set("Prefer", "respond-async, return=minimal")
	minimalResponse := httptest.NewRecorder()
	router.ServeHTTP(minimalResponse, minimalRequest)
	if minimalResponse.Code != http.StatusCreated || minimalResponse.Body.Len() != 0 || minimalResponse.Header().Get("Location") == "" || minimalResponse.Header().Get("Preference-Applied") != "return=minimal" {
		t.Fatalf("minimal create: %d headers=%v body=%q", minimalResponse.Code, minimalResponse.Header(), minimalResponse.Body.String())
	}
	deleteRequest := httptest.NewRequest("DELETE", path, nil)
	deleteRequest.Header.Set("Authorization", access)
	deleteRequest.Header.Set("Prefer", "return=representation")
	deleteResponse := httptest.NewRecorder()
	router.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusOK || deleteResponse.Header().Get("Preference-Applied") != "return=representation" || !strings.Contains(deleteResponse.Body.String(), "hello ****") {
		t.Fatalf("delete representation: %d %v %s", deleteResponse.Code, deleteResponse.Header(), deleteResponse.Body.String())
	}

	request("POST", "/api/chirps", map[string]string{"body": strings.Repeat("界", 141)}, access, 400)
	request("POST", "/api/chirps", map[string]string{"body": " "}, access, 400)
	request("POST", "/api/polka/webhooks", map[string]any{"event": "user.upgraded", "data": map[string]string{"user_id": id}}, "ApiKey wrong", 401)
	request("POST", "/api/polka/webhooks", map[string]any{"event": "user.upgraded", "data": map[string]string{"user_id": id}}, "ApiKey integration-key", 204)
	upgraded := request("POST", "/api/login", credentials, "", 200)
	if upgraded["is_chirpy_red"] != true {
		t.Fatal("upgrade not persisted")
	}
	request("POST", "/api/refresh", nil, refresh, 200)
	if _, err := db.Exec("UPDATE refresh_tokens SET expires_at=NOW()-interval '1 second' WHERE token=$1", login["refresh_token"]); err != nil {
		t.Fatal(err)
	}
	request("POST", "/api/refresh", nil, refresh, 401)
	if _, err := db.Exec("UPDATE refresh_tokens SET expires_at=NOW()+interval '1 day' WHERE token=$1", login["refresh_token"]); err != nil {
		t.Fatal(err)
	}

	request("POST", "/api/revoke", nil, refresh, 204)
	request("POST", "/api/refresh", nil, refresh, 401)
	request("DELETE", "/api/chirps/"+second["id"].(string), "", access, 204)
	request("GET", path, nil, "", 404)
	updated := request("PUT", "/api/users", map[string]string{"email": email, "password": "changed-password"}, access, 200)
	if updated["id"] != id || updated["is_chirpy_red"] != true {
		t.Fatal("update lost identity/membership")
	}
	request("POST", "/api/login", credentials, "", 401)
	request("POST", "/api/login", map[string]string{"email": email, "password": "changed-password"}, "", 200)
	// List parameters are tested against real rows and preserve array shape.
	for _, query := range []string{"?sort=asc&limit=1", "?sort=desc&author_id=" + id, "?offset=100000"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/chirps"+query, nil))
		if w.Code != 200 || !strings.HasPrefix(w.Body.String(), "[") {
			t.Fatalf("list response %d %s", w.Code, w.Body)
		}
	}
}
