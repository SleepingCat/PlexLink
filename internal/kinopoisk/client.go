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
	defaultBaseURL = "https://api.kinopoisk.dev/v1.4"
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
	Docs []Movie `json:"docs"`
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
	endpoint, err := url.Parse(c.baseURL + "/movie/search")
	if err != nil {
		return SearchResponse{}, fmt.Errorf("build search URL: %w", err)
	}
	params := endpoint.Query()
	params.Set("query", query)
	params.Set("limit", strconv.Itoa(c.resultLimit))
	params.Set("page", "1")
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
	var result SearchResponse
	var envelope struct {
		Docs json.RawMessage `json:"docs"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes))
	if err := decoder.Decode(&envelope); err != nil {
		return SearchResponse{}, fmt.Errorf("decode search response: %w", err)
	}
	if len(envelope.Docs) == 0 || string(envelope.Docs) == "null" {
		return SearchResponse{}, fmt.Errorf("decode search response: missing docs array")
	}
	if err := json.Unmarshal(envelope.Docs, &result.Docs); err != nil {
		return SearchResponse{}, fmt.Errorf("decode search response: invalid docs array: %w", err)
	}
	return result, nil
}
