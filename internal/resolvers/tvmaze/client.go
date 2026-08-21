package tvmaze

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
	"time"
)

const DefaultBaseURL = "https://api.tvmaze.com"

var ErrNotFound = errors.New("tvmaze resource not found")

type HTTPError struct {
	StatusCode int
}

func (e *HTTPError) Error() string { return fmt.Sprintf("TVMaze API status %d", e.StatusCode) }

type Client struct {
	base string
	http *http.Client
}

func New(base string, h *http.Client) *Client {
	if strings.TrimSpace(base) == "" {
		base = DefaultBaseURL
	}
	if h == nil {
		h = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{base: strings.TrimRight(base, "/"), http: h}
}

type SearchResult struct {
	Score float64 `json:"score"`
	Show  Show    `json:"show"`
}

type Show struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Language  string    `json:"language"`
	Premiered string    `json:"premiered"`
	Externals Externals `json:"externals"`
}

type Externals struct {
	TVRage  *int    `json:"tvrage"`
	TheTVDB *int    `json:"thetvdb"`
	IMDb    *string `json:"imdb"`
}

type AKA struct {
	Name    string   `json:"name"`
	Country *Country `json:"country"`
}

type Country struct {
	Name string `json:"name"`
	Code string `json:"code"`
}

type Episode struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Season   int    `json:"season"`
	Number   *int   `json:"number"`
	Type     string `json:"type"`
	Airdate  string `json:"airdate"`
	Airstamp string `json:"airstamp"`
	Runtime  *int   `json:"runtime"`
}

type Season struct {
	ID           int    `json:"id"`
	Number       int    `json:"number"`
	Name         string `json:"name"`
	EpisodeOrder *int   `json:"episodeOrder"`
	PremiereDate string `json:"premiereDate"`
	EndDate      string `json:"endDate"`
}

type AlternateList struct {
	ID               int      `json:"id"`
	URL              string   `json:"url"`
	DVD              bool     `json:"dvdRelease"`
	Country          *Country `json:"country"`
	Language         *string  `json:"language"`
	Network          *Network `json:"network"`
	WebChannel       *Network `json:"webChannel"`
	Streaming        bool     `json:"streamingPremiere"`
	Broadcast        bool     `json:"broadcastPremiere"`
	CountryPremiere  bool     `json:"countryPremiere"`
	LanguagePremiere bool     `json:"languagePremiere"`
}

type Network struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type AlternateEpisode struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Season   *int   `json:"season"`
	Number   *int   `json:"number"`
	Airdate  string `json:"airdate"`
	Airstamp string `json:"airstamp"`
	Runtime  *int   `json:"runtime"`
	Embedded struct {
		Episodes []Episode `json:"episodes"`
	} `json:"_embedded"`
}

func (c *Client) SearchShows(ctx context.Context, query string) ([]SearchResult, error) {
	var result []SearchResult
	err := c.get(ctx, "/search/shows?q="+url.QueryEscape(query), &result)
	return result, err
}

func (c *Client) LookupShowByIMDb(ctx context.Context, imdbID string) (Show, error) {
	var result Show
	err := c.get(ctx, "/lookup/shows?imdb="+url.QueryEscape(imdbID), &result)
	return result, err
}

func (c *Client) Show(ctx context.Context, id int) (Show, error) {
	var result Show
	err := c.get(ctx, "/shows/"+strconv.Itoa(id), &result)
	return result, err
}

func (c *Client) AKAs(ctx context.Context, showID int) ([]AKA, error) {
	var result []AKA
	err := c.get(ctx, fmt.Sprintf("/shows/%d/akas", showID), &result)
	return result, err
}

func (c *Client) EpisodesWithSpecials(ctx context.Context, showID int) ([]Episode, error) {
	var result []Episode
	err := c.get(ctx, fmt.Sprintf("/shows/%d/episodes?specials=1", showID), &result)
	return result, err
}

func (c *Client) Seasons(ctx context.Context, showID int) ([]Season, error) {
	var result []Season
	err := c.get(ctx, fmt.Sprintf("/shows/%d/seasons", showID), &result)
	return result, err
}

func (c *Client) AlternateLists(ctx context.Context, showID int) ([]AlternateList, error) {
	var result []AlternateList
	err := c.get(ctx, fmt.Sprintf("/shows/%d/alternatelists", showID), &result)
	return result, err
}

func (c *Client) AlternateEpisodes(ctx context.Context, listID int) ([]AlternateEpisode, error) {
	var result []AlternateEpisode
	err := c.get(ctx, fmt.Sprintf("/alternatelists/%d/alternateepisodes?embed=episodes", listID), &result)
	return result, err
}

func (c *Client) get(ctx context.Context, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return fmt.Errorf("build TVMaze request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call TVMaze: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return &HTTPError{StatusCode: resp.StatusCode}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(target); err != nil {
		return fmt.Errorf("decode TVMaze response: %w", err)
	}
	return nil
}
