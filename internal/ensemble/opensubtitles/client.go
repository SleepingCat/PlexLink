package opensubtitles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/SleepingCat/PlexLink/internal/ensemble"
)

const maxResponseBytes = 4 << 20

type Client struct {
	baseURL   string
	apiKey    string
	userAgent string
	http      *http.Client
}

func NewClient(baseURL, apiKey, userAgent string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if strings.TrimSpace(userAgent) == "" {
		userAgent = "PlexLink"
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, userAgent: userAgent, http: httpClient}
}

type SearchResult struct {
	MovieHashMatch bool
	Feature        Feature
}

type Feature struct {
	Type         string
	Title        string
	Year         int
	TMDBID       int
	IMDbID       string
	ParentTMDBID int
	ParentIMDbID string
	ParentTitle  string
	Season       int
	Episode      int
}

type searchResponse struct {
	Data []struct {
		Attributes struct {
			MovieHashMatch bool `json:"moviehash_match"`
			FeatureDetails struct {
				FeatureType  string          `json:"feature_type"`
				Title        string          `json:"title"`
				Year         int             `json:"year"`
				TMDBID       json.RawMessage `json:"tmdb_id"`
				IMDbID       json.RawMessage `json:"imdb_id"`
				ParentTMDBID json.RawMessage `json:"parent_tmdb_id"`
				ParentIMDbID json.RawMessage `json:"parent_imdb_id"`
				ParentTitle  string          `json:"parent_title"`
				Season       int             `json:"season_number"`
				Episode      int             `json:"episode_number"`
			} `json:"feature_details"`
		} `json:"attributes"`
	} `json:"data"`
}

func (c *Client) Search(ctx context.Context, hash string, size int64) ([]SearchResult, *ensemble.OperationalError) {
	if c.baseURL == "" || c.apiKey == "" {
		return nil, opError(ensemble.ErrorConfiguration, 0, "OpenSubtitles client is not configured", false)
	}
	u, err := url.Parse(c.baseURL + "/subtitles")
	if err != nil {
		return nil, opError(ensemble.ErrorConfiguration, 0, "invalid OpenSubtitles base URL", false)
	}
	q := u.Query()
	q.Set("moviehash", hash)
	q.Set("moviebytesize", strconv.FormatInt(size, 10))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, opError(ensemble.ErrorConfiguration, 0, "cannot create OpenSubtitles request", false)
	}
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return nil, opError(ensemble.ErrorCanceled, 0, "OpenSubtitles request canceled", false)
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, opError(ensemble.ErrorTimeout, 0, "OpenSubtitles request timed out", true)
		}
		return nil, opError(ensemble.ErrorProvider, 0, "OpenSubtitles request failed", true)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, opError(ensemble.ErrorAuthentication, resp.StatusCode, "OpenSubtitles authentication failed", false)
		case http.StatusTooManyRequests:
			return nil, opError(ensemble.ErrorRateLimited, resp.StatusCode, "OpenSubtitles rate limit exceeded", true)
		default:
			return nil, opError(ensemble.ErrorProvider, resp.StatusCode, "OpenSubtitles provider error", resp.StatusCode >= 500)
		}
	}

	var payload searchResponse
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes))
	if err := dec.Decode(&payload); err != nil {
		return nil, opError(ensemble.ErrorInvalidResponse, resp.StatusCode, "invalid OpenSubtitles response", false)
	}
	results := make([]SearchResult, 0, len(payload.Data))
	for _, datum := range payload.Data {
		f := datum.Attributes.FeatureDetails
		results = append(results, SearchResult{MovieHashMatch: datum.Attributes.MovieHashMatch, Feature: Feature{
			Type: f.FeatureType, Title: f.Title, Year: f.Year, TMDBID: jsonInt(f.TMDBID), IMDbID: imdbID(f.IMDbID),
			ParentTMDBID: jsonInt(f.ParentTMDBID), ParentIMDbID: imdbID(f.ParentIMDbID), ParentTitle: f.ParentTitle,
			Season: f.Season, Episode: f.Episode,
		}})
	}
	return results, nil
}

func opError(kind ensemble.OperationalErrorKind, status int, message string, retryable bool) *ensemble.OperationalError {
	return &ensemble.OperationalError{Kind: kind, StatusCode: status, Message: message, Retryable: retryable}
}

func jsonInt(raw json.RawMessage) int {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		n, _ = strconv.Atoi(s)
	}
	return n
}

func imdbID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var n int64
		if json.Unmarshal(raw, &n) != nil {
			return ""
		}
		s = strconv.FormatInt(n, 10)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(s), "tt") {
		return "tt" + strings.TrimPrefix(strings.ToLower(s), "tt")
	}
	return fmt.Sprintf("tt%s", s)
}
