package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SleepingCat/PlexLink/internal/httpx"
	"github.com/SleepingCat/PlexLink/internal/model"
)

type Client struct {
	base, token, language string
	http                  *http.Client
}

func New(base, token, language string, h *http.Client) *Client {
	if h == nil {
		h = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{strings.TrimRight(base, "/"), token, language, h}
}
func (c *Client) Ping(ctx context.Context) error {
	var response struct {
		Images struct {
			BaseURL string `json:"base_url"`
		} `json:"images"`
	}
	if err := c.get(ctx, "/configuration", &response); err != nil {
		return err
	}
	if response.Images.BaseURL == "" {
		return fmt.Errorf("TMDB configuration response is incomplete")
	}
	return nil
}
func (c *Client) get(ctx context.Context, path string, v any) error {
	resp, err := httpx.Do(ctx, c.http, func() (*http.Request, error) {
		r, e := http.NewRequest(http.MethodGet, c.base+path, nil)
		if e == nil {
			r.Header.Set("Authorization", "Bearer "+c.token)
			r.Header.Set("Accept", "application/json")
		}
		return r, e
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("TMDB API status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v); err != nil {
		return fmt.Errorf("decode TMDB response: %w", err)
	}
	return nil
}
func (c *Client) SearchTV(ctx context.Context, q string) ([]model.TVCandidate, error) {
	var r struct {
		Results []model.TVCandidate `json:"results"`
	}
	err := c.get(ctx, "/search/tv?query="+url.QueryEscape(q)+"&language="+url.QueryEscape(c.language), &r)
	return r.Results, err
}
func (c *Client) SearchMovie(ctx context.Context, q string) ([]model.MovieCandidate, error) {
	var r struct {
		Results []model.MovieCandidate `json:"results"`
	}
	err := c.get(ctx, "/search/movie?query="+url.QueryEscape(q)+"&language="+url.QueryEscape(c.language), &r)
	return r.Results, err
}
func (c *Client) GetTV(ctx context.Context, id int) (model.TVShow, error) {
	var r model.TVShow
	err := c.get(ctx, "/tv/"+strconv.Itoa(id)+"?language="+url.QueryEscape(c.language), &r)
	return r, err
}
func (c *Client) GetSeason(ctx context.Context, id, season int) (model.Season, error) {
	var r model.Season
	err := c.get(ctx, fmt.Sprintf("/tv/%d/season/%d?language=%s", id, season, url.QueryEscape(c.language)), &r)
	return r, err
}
func (c *Client) GetMovie(ctx context.Context, id int) (model.Movie, error) {
	var r model.Movie
	err := c.get(ctx, "/movie/"+strconv.Itoa(id)+"?language="+url.QueryEscape(c.language), &r)
	return r, err
}
