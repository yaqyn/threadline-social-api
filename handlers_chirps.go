package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/yaqyn/threadline-social-api/internal/auth"
	"github.com/yaqyn/threadline-social-api/internal/database"
)

func (cfg *apiConfig) handlerCreateChirp(w http.ResponseWriter, r *http.Request) {
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Invalid or missing token")
		return
	}
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Invalid or missing token")
		return
	}
	var params struct {
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &params) {
		return
	}
	if strings.TrimSpace(params.Body) == "" {
		respondWithError(w, http.StatusBadRequest, "Chirp body is required")
		return
	}
	if hasUnsafeControls(params.Body) {
		respondWithError(w, http.StatusBadRequest, "Chirp contains unsupported control characters")
		return
	}
	params.Body = normalizeChirp(params.Body)
	if utf8.RuneCountInString(params.Body) > 140 {
		respondWithError(w, http.StatusBadRequest, "Chirp is too long")
		return
	}
	chirp, err := cfg.dbQueries.CreateChirp(r.Context(), database.CreateChirpParams{
		Body: params.Body, UserID: userID,
	})
	if err != nil {
		var pgErr *pq.Error
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			respondWithError(w, http.StatusBadRequest, "User does not exist")
			return
		}
		log.Printf("Failed to create chirp: %v", err)
		respondWithError(w, http.StatusInternalServerError, "Failed to create chirp")
		return
	}
	respondWithJSON(w, http.StatusCreated, Chirp{
		ID: chirp.ID, CreatedAt: chirp.CreatedAt, UpdatedAt: chirp.UpdatedAt,
		Body: chirp.Body, UserID: chirp.UserID,
	})
}

func (cfg *apiConfig) handlerGetChirps(w http.ResponseWriter, r *http.Request) {
	params, err := parseChirpQuery(r)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	requestedLimit := params.PageLimit
	params.PageLimit++
	rows, err := cfg.dbQueries.GetChirpsPage(r.Context(), params)
	if err != nil {
		log.Printf("Failed to get chirps: %v", err)
		respondWithError(w, http.StatusInternalServerError, "Failed to get chirps")
		return
	}
	hasMore := len(rows) > int(requestedLimit)
	if hasMore {
		rows = rows[:requestedLimit]
	}
	chirps := make([]Chirp, 0, len(rows))
	for _, row := range rows {
		chirps = append(chirps, Chirp{
			ID: row.ID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			Body: row.Body, UserID: row.UserID,
		})
	}
	w.Header().Set("X-Page-Limit", strconv.Itoa(int(requestedLimit)))
	w.Header().Set("X-Page-Offset", strconv.Itoa(int(params.PageOffset)))
	w.Header().Set("X-Result-Count", strconv.Itoa(len(chirps)))
	w.Header().Set("X-Has-More", strconv.FormatBool(hasMore))
	if hasMore {
		w.Header().Set("X-Next-Offset", strconv.Itoa(int(params.PageOffset+requestedLimit)))
	}
	if params.PageOffset > 0 {
		previous := max(int32(0), params.PageOffset-requestedLimit)
		w.Header().Set("X-Previous-Offset", strconv.Itoa(int(previous)))
	}
	links := []string{}
	if hasMore {
		links = append(links, paginationLink(r, params.PageOffset+requestedLimit, "next"))
	}
	if params.PageOffset > 0 {
		links = append(links, paginationLink(r, max(int32(0), params.PageOffset-requestedLimit), "prev"))
	}
	if len(links) > 0 {
		w.Header().Set("Link", strings.Join(links, ", "))
	}
	respondWithJSON(w, http.StatusOK, chirps)
}

func paginationLink(r *http.Request, offset int32, relation string) string {
	values := r.URL.Query()
	values.Del("page")
	values.Set("offset", strconv.Itoa(int(offset)))
	return "<" + r.URL.Path + "?" + values.Encode() + ">; rel=\"" + relation + "\""
}

func (cfg *apiConfig) handlerGetChirp(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("chirpID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid chirp ID")
		return
	}
	chirp, err := cfg.dbQueries.GetChirp(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		respondWithError(w, http.StatusNotFound, "Chirp not found")
		return
	}
	if err != nil {
		log.Printf("Failed to get chirp: %v", err)
		respondWithError(w, http.StatusInternalServerError, "Failed to get chirp")
		return
	}
	respondWithJSON(w, http.StatusOK, Chirp{
		ID: chirp.ID, CreatedAt: chirp.CreatedAt, UpdatedAt: chirp.UpdatedAt,
		Body: chirp.Body, UserID: chirp.UserID,
	})
}

func cleanChirp(body string) string {
	var cleaned strings.Builder
	writeWord := func(word string) {
		switch strings.ToLower(word) {
		case "kerfuffle", "sharbert", "fornax":
			cleaned.WriteString("****")
		default:
			cleaned.WriteString(word)
		}
	}
	start := 0
	for index, char := range body {
		if unicode.IsSpace(char) {
			writeWord(body[start:index])
			cleaned.WriteRune(char)
			start = index + utf8.RuneLen(char)
		}
	}
	writeWord(body[start:])
	return cleaned.String()
}

func normalizeChirp(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	lines := strings.Split(strings.TrimSpace(body), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " \t")
	}
	return cleanChirp(strings.Join(lines, "\n"))
}

func (cfg *apiConfig) handlerDeleteChirp(w http.ResponseWriter, r *http.Request) {
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Invalid or missing token")
		return
	}
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Invalid or missing token")
		return
	}
	chirpID, err := uuid.Parse(r.PathValue("chirpID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid chirp ID")
		return
	}
	chirp, err := cfg.dbQueries.GetChirp(r.Context(), chirpID)
	if errors.Is(err, sql.ErrNoRows) {
		respondWithError(w, http.StatusNotFound, "Chirp not found")
		return
	}
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get chirp")
		return
	}
	if chirp.UserID != userID {
		respondWithError(w, http.StatusForbidden, "You can only delete your own chirps")
		return
	}
	deleted, err := cfg.dbQueries.DeleteChirp(r.Context(), database.DeleteChirpParams{
		ID: chirpID, UserID: userID,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to delete chirp")
		return
	}
	if deleted == 0 {
		respondWithError(w, http.StatusNotFound, "Chirp not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
