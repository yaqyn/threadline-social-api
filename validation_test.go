package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"valid", `{"email":"a@example.com"}`, 200},
		{"null", `null`, 400},
		{"trailing", `{} {}`, 400},
		{"malformed", `{`, 400},
		{"wrong type", `[]`, 400},
		{"unknown field", `{"email":"a@example.com","admin":true}`, 400},
		{"oversized", `{"email":"` + strings.Repeat("a", maxBodyBytes) + `"}`, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
			var payload struct {
				Email string `json:"email"`
			}
			got := decodeJSON(w, r, &payload)
			if got != (tc.want == 200) || w.Code != tc.want {
				t.Fatalf("valid=%v status=%d", got, w.Code)
			}
		})
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"email":"a@example.com"}`))
	var payload struct {
		Email string `json:"email"`
	}
	if decodeJSON(w, r, &payload) || w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("accepted missing content type: %d", w.Code)
	}
}

func TestQueryValidation(t *testing.T) {
	for _, query := range []string{"?limit=%ZZ", "?offset=%", "?limit=1;offset=100", "?limit=5&limit=10", "?sort=asc&sort=desc", "?page=1&page=2", "?page=2&offset=1", "?limt=5"} {
		if _, err := parseChirpQuery(httptest.NewRequest("GET", "/"+query, nil)); err == nil {
			t.Fatalf("accepted malformed query %s", query)
		}
	}
	for _, query := range []string{"?sort=wrong", "?limit=0", "?limit=101", "?offset=-1", "?page=0", "?author_id=wrong"} {
		if _, err := parseChirpQuery(httptest.NewRequest("GET", "/"+query, nil)); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
	p, err := parseChirpQuery(httptest.NewRequest("GET", "/?sort=desc&limit=5&offset=10", nil))
	if err != nil || !p.Descending || p.PageLimit != 5 || p.PageOffset != 10 {
		t.Fatalf("params=%+v err=%v", p, err)
	}
	p, err = parseChirpQuery(httptest.NewRequest("GET", "/?limit=5&page=3", nil))
	if err != nil || p.PageLimit != 5 || p.PageOffset != 10 {
		t.Fatalf("page params=%+v err=%v", p, err)
	}
	for _, alias := range []string{"newest", "oldest"} {
		p, err := parseChirpQuery(httptest.NewRequest("GET", "/?sort="+alias, nil))
		if err != nil || p.Descending != (alias == "newest") {
			t.Fatalf("sort alias %s: %+v %v", alias, p, err)
		}
	}
}

func TestPaginationLinksPreserveFiltersAndUseOffsets(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/chirps?author_id=00000000-0000-0000-0000-000000000001&limit=5&page=3", nil)
	got := paginationLink(r, 15, "next")
	if strings.Contains(got, "page=") || !strings.Contains(got, "offset=15") || !strings.Contains(got, "author_id=") || !strings.Contains(got, `rel="next"`) {
		t.Fatal(got)
	}
}

func TestCleanChirp(t *testing.T) {
	for _, separator := range []string{"\t", "\n", "\r\n", "\u00a0", "\u2003"} {
		if got := cleanChirp("hello" + separator + "FORNAX" + separator); got != "hello"+separator+"****"+separator {
			t.Errorf("whitespace cleaning: %q", got)
		}
	}
	for input, want := range map[string]string{"KERFUFFLE sharbert fornax": "**** **** ****", "Sharbert!": "Sharbert!", "a  kerfuffle": "a  ****", "plain text": "plain text"} {
		if got := cleanChirp(input); got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	}
}

func TestCreateChirpTrimsSurroundingWhitespaceBeforeSaving(t *testing.T) {
	body := normalizeChirp("\n\t hello FORNAX \u00a0")
	if body != "hello ****" {
		t.Fatalf("unexpected normalization: %q", body)
	}
}

func TestCreateChirpNormalizesLineEndings(t *testing.T) {
	if got := normalizeChirp("first\r\nsecond\rthird"); got != "first\nsecond\nthird" {
		t.Fatalf("unexpected line endings: %q", got)
	}
}

func TestCreateChirpRemovesTrailingLineWhitespace(t *testing.T) {
	if got := normalizeChirp("first  \nsecond\t\n third "); got != "first\nsecond\n third" {
		t.Fatalf("unexpected trailing whitespace: %q", got)
	}
}

func TestChirpControlValidationKeepsFormattingWhitespace(t *testing.T) {
	for _, value := range []string{"before\x00after", "before\x1bafter", "before\u007fafter"} {
		if !hasUnsafeControls(value) {
			t.Fatalf("accepted control in %q", value)
		}
	}
	if hasUnsafeControls("one\ntwo\tindented\r\n") {
		t.Fatal("rejected formatting whitespace")
	}
}

func TestValidEmail(t *testing.T) {
	for _, value := range []string{"", "invalid", "Name <a@example.com>"} {
		if validEmail(value) {
			t.Fatalf("accepted %q", value)
		}
	}
	if !validEmail("a@example.com") {
		t.Fatal("rejected email")
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got := normalizeEmail("  Student@Example.COM\t"); got != "student@example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestInvalidLoginInputIsRejectedWithoutDatabaseAccess(t *testing.T) {
	cfg := &apiConfig{}
	for _, body := range []string{
		`{"email":"invalid","password":"password"}`,
		`{"email":"a@example.com","password":""}`,
		`{"email":"a@example.com","password":"` + strings.Repeat("a", 1025) + `"}`,
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/api/login", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		cfg.routes().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("got %d: %s", response.Code, response.Body)
		}
		if !strings.Contains(response.Body.String(), "Incorrect email or password") {
			t.Fatal(response.Body.String())
		}
	}
}
