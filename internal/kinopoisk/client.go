package kinopoisk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	defaultBaseURL = "https://api.poiskkino.dev"
	maxBodyBytes   = 1 << 20
)

type Client struct {
	baseURL     string
	apiKey      string
	http        *http.Client
	resultLimit int
}

func NewClient(baseURL, apiKey string, httpClient *http.Client) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBaseURL
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: httpClient, resultLimit: 10}
}

type SearchResponse struct {
	Docs  []Movie `json:"docs"`
	Total int     `json:"total"`
	Limit int     `json:"limit"`
	Page  int     `json:"page"`
	Pages int     `json:"pages"`
}

type TokenInfo struct {
	RequestsLimit     int    `json:"requestsLimit"`
	RequestsUsed      int    `json:"requestsUsed"`
	RequestsRemaining int    `json:"requestsRemaining"`
	TTL               int    `json:"ttl"`
	ResetAt           string `json:"resetAt"`
}

type Movie struct {
	ID              int         `json:"id"`
	Name            string      `json:"name"`
	AlternativeName string      `json:"alternativeName"`
	EnName          string      `json:"enName"`
	Names           []MovieName `json:"names"`
	Year            int         `json:"year"`
	Type            string      `json:"type"`
	ExternalID      ExternalID  `json:"externalId"`
}

type MovieName struct {
	Name     string `json:"name"`
	Language string `json:"language"`
	Type     string `json:"type"`
}

type ExternalID struct {
	TMDB flexibleInt `json:"tmdb"`
	IMDb string      `json:"imdb"`
}

type flexibleInt int

func (i *flexibleInt) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || string(data) == `""` {
		*i = 0
		return nil
	}
	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		*i = flexibleInt(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("expected integer or numeric string")
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("expected numeric string")
	}
	*i = flexibleInt(n)
	return nil
}

type HTTPError struct{ StatusCode int }

func (e *HTTPError) Error() string { return fmt.Sprintf("kinopoisk returned HTTP %d", e.StatusCode) }

func (c *Client) Search(ctx context.Context, query string) (SearchResponse, error) {
	endpoint, err := url.Parse(c.baseURL + "/v1.5/movie/search")
	if err != nil {
		return SearchResponse{}, fmt.Errorf("build search URL: %w", err)
	}
	params := endpoint.Query()
	params.Set("query", query)
	params.Set("limit", strconv.Itoa(c.resultLimit))
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return SearchResponse{}, fmt.Errorf("build search request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-KEY", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return SearchResponse{}, fmt.Errorf("perform search request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return SearchResponse{}, &HTTPError{StatusCode: resp.StatusCode}
	}
	var envelope struct {
		Docs  json.RawMessage `json:"docs"`
		Total *int            `json:"total"`
		Limit *int            `json:"limit"`
		Page  *int            `json:"page"`
		Pages *int            `json:"pages"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes))
	if err := decoder.Decode(&envelope); err != nil {
		return SearchResponse{}, fmt.Errorf("decode search response: %w", err)
	}
	if len(envelope.Docs) == 0 || string(envelope.Docs) == "null" {
		return SearchResponse{}, fmt.Errorf("decode search response: missing docs array")
	}
	if envelope.Total == nil || envelope.Limit == nil || envelope.Page == nil || envelope.Pages == nil || *envelope.Total < 0 || *envelope.Limit < 0 || *envelope.Limit > 10 || *envelope.Page < 1 || *envelope.Pages < 0 {
		return SearchResponse{}, fmt.Errorf("decode search response: invalid pagination envelope")
	}
	result := SearchResponse{Total: *envelope.Total, Limit: *envelope.Limit, Page: *envelope.Page, Pages: *envelope.Pages}
	if err := json.Unmarshal(envelope.Docs, &result.Docs); err != nil {
		return SearchResponse{}, fmt.Errorf("decode search response: invalid docs array: %w", err)
	}
	return result, nil
}

func (c *Client) Token(ctx context.Context) (TokenInfo, error) {
	endpoint, err := url.Parse(c.baseURL + "/v1.5/token")
	if err != nil {
		return TokenInfo{}, fmt.Errorf("build token URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return TokenInfo{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-KEY", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return TokenInfo{}, fmt.Errorf("perform token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return TokenInfo{}, &HTTPError{StatusCode: resp.StatusCode}
	}
	var wire struct {
		RequestsLimit     *int    `json:"requestsLimit"`
		RequestsUsed      *int    `json:"requestsUsed"`
		RequestsRemaining *int    `json:"requestsRemaining"`
		TTL               *int    `json:"ttl"`
		ResetAt           *string `json:"resetAt"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&wire); err != nil {
		return TokenInfo{}, fmt.Errorf("decode token response: %w", err)
	}
	if wire.RequestsLimit == nil || wire.RequestsUsed == nil || wire.RequestsRemaining == nil || wire.TTL == nil || wire.ResetAt == nil {
		return TokenInfo{}, fmt.Errorf("decode token response: incomplete token status")
	}
	return TokenInfo{RequestsLimit: *wire.RequestsLimit, RequestsUsed: *wire.RequestsUsed, RequestsRemaining: *wire.RequestsRemaining, TTL: *wire.TTL, ResetAt: *wire.ResetAt}, nil
}
