package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/yaqyn/threadline-social-api/internal/database"
)

func decodeJSON(w http.ResponseWriter, r *http.Request, payload any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		respondWithError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	decoder := json.NewDecoder(r.Body)
	// Decode the document first so null is never mistaken for an empty object.
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		jsonError(w, err)
		return false
	}
	if string(raw) == "null" {
		respondWithError(w, 400, "JSON object is required")
		return false
	}
	payloadDecoder := json.NewDecoder(bytes.NewReader(raw))
	payloadDecoder.DisallowUnknownFields()
	if err := payloadDecoder.Decode(payload); err != nil {
		jsonError(w, err)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		jsonError(w, err)
		return false
	}
	return true
}

func jsonError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		respondWithError(w, 413, "Request body is too large")
		return
	}
	respondWithError(w, 400, "Invalid JSON")
}

func validEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && len(value) <= 254
}
func normalizeEmail(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func hasUnsafeControls(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) && char != '\n' && char != '\r' && char != '\t' {
			return true
		}
	}
	return false
}

func parseChirpQuery(r *http.Request) (database.GetChirpsPageParams, error) {
	p := database.GetChirpsPageParams{PageLimit: 100}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return p, errors.New("Invalid query encoding")
	}
	allowed := map[string]bool{"author_id": true, "q": true, "sort": true, "limit": true, "offset": true, "page": true, "format": true}
	for name := range query {
		if !allowed[name] {
			return p, errors.New("Unknown query parameter: " + name)
		}
	}
	for _, name := range []string{"author_id", "q", "sort", "limit", "offset", "page", "format"} {
		if len(query[name]) > 1 {
			return p, errors.New("Duplicate " + name)
		}
	}
	if format := query.Get("format"); format != "" && format != "json" && format != "ndjson" {
		return p, errors.New("Format must be json or ndjson")
	}
	p.Search = strings.TrimSpace(query.Get("q"))
	if utf8.RuneCountInString(p.Search) > 140 || hasUnsafeControls(p.Search) {
		return p, errors.New("Search must contain 140 characters or fewer")
	}
	if value := query.Get("author_id"); value != "" {
		id, err := uuid.Parse(value)
		if err != nil {
			return p, errors.New("Invalid author ID")
		}
		p.AuthorID = uuid.NullUUID{UUID: id, Valid: true}
	}
	switch query.Get("sort") {
	case "", "asc", "oldest":
	case "desc", "newest":
		p.Descending = true
	default:
		return p, errors.New("Sort must be asc, desc, oldest, or newest")
	}
	for _, item := range []struct {
		name     string
		target   *int32
		min, max int64
	}{
		{"limit", &p.PageLimit, 1, 100}, {"offset", &p.PageOffset, 0, 2147483647},
	} {
		if value := query.Get(item.name); value != "" {
			n, err := strconv.ParseInt(value, 10, 32)
			if err != nil || n < item.min || n > item.max {
				return p, errors.New("Invalid " + item.name)
			}
			*item.target = int32(n)
		}
	}
	if value := query.Get("page"); value != "" {
		if query.Get("offset") != "" {
			return p, errors.New("Page and offset cannot be combined")
		}
		page, err := strconv.ParseInt(value, 10, 32)
		offset := (page - 1) * int64(p.PageLimit)
		if err != nil || page < 1 || offset > 2147483647 {
			return p, errors.New("Invalid page")
		}
		p.PageOffset = int32(offset)
	}
	return p, nil
}
