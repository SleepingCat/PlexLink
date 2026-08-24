package qbt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/SleepingCat/PlexLink/internal/httpx"
	"github.com/SleepingCat/PlexLink/internal/model"
)

type Client struct {
	base, user, password string
	http                 *http.Client
	loggedIn             bool
}

func New(base, user, password string, h *http.Client) (*Client, error) {
	if h == nil {
		jar, _ := cookiejar.New(nil)
		h = &http.Client{Timeout: 15 * time.Second, Jar: jar}
	}
	if h.Jar == nil {
		jar, _ := cookiejar.New(nil)
		h.Jar = jar
	}
	return &Client{base: strings.TrimRight(base, "/"), user: user, password: password, http: h}, nil
}
func (c *Client) Login(ctx context.Context) error {
	form := url.Values{"username": {c.user}, "password": {c.password}}.Encode()
	resp, err := httpx.Do(ctx, c.http, func() (*http.Request, error) {
		r, err := http.NewRequest(http.MethodPost, c.base+"/api/v2/auth/login", strings.NewReader(form))
		if err == nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		return r, err
	})
	if err != nil {
		return fmt.Errorf("qBittorrent login: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != 200 || strings.TrimSpace(string(b)) != "Ok." {
		return fmt.Errorf("qBittorrent login failed: status %d", resp.StatusCode)
	}
	c.loggedIn = true
	return nil
}
func (c *Client) ensure(ctx context.Context) error {
	if c.loggedIn {
		return nil
	}
	return c.Login(ctx)
}
func (c *Client) GetTorrent(ctx context.Context, hash string) (model.Torrent, error) {
	if err := c.ensure(ctx); err != nil {
		return model.Torrent{}, err
	}
	var rows []struct {
		Hash        string  `json:"hash"`
		Name        string  `json:"name"`
		ContentPath string  `json:"content_path"`
		SavePath    string  `json:"save_path"`
		Category    string  `json:"category"`
		Tags        string  `json:"tags"`
		State       string  `json:"state"`
		Progress    float64 `json:"progress"`
	}
	if err := c.getJSON(ctx, "/api/v2/torrents/info?hashes="+url.QueryEscape(hash), &rows); err != nil {
		return model.Torrent{}, err
	}
	if len(rows) == 0 {
		return model.Torrent{}, fmt.Errorf("torrent %s not found", hash)
	}
	r := rows[0]
	return model.Torrent{Hash: r.Hash, Name: r.Name, ContentPath: r.ContentPath, SavePath: r.SavePath, Category: r.Category, Tags: r.Tags, Progress: r.Progress, State: r.State}, nil
}
func (c *Client) GetFiles(ctx context.Context, hash string) ([]model.TorrentFile, error) {
	if err := c.ensure(ctx); err != nil {
		return nil, err
	}
	var rows []struct {
		Name     string  `json:"name"`
		Size     int64   `json:"size"`
		Priority int     `json:"priority"`
		Progress float64 `json:"progress"`
	}
	if err := c.getJSON(ctx, "/api/v2/torrents/files?hash="+url.QueryEscape(hash), &rows); err != nil {
		return nil, err
	}
	out := make([]model.TorrentFile, len(rows))
	for i, r := range rows {
		out[i] = model.TorrentFile{Name: r.Name, Size: r.Size, Priority: r.Priority, Progress: r.Progress}
	}
	return out, nil
}

// ShutdownIfIdle requests application shutdown only when qBittorrent reports
// that no incomplete downloads remain. It never changes individual torrents.
func (c *Client) ShutdownIfIdle(ctx context.Context) (bool, error) {
	if err := c.ensure(ctx); err != nil {
		return false, err
	}
	var torrents []struct {
		Hash       string   `json:"hash"`
		Progress   *float64 `json:"progress"`
		AmountLeft *int64   `json:"amount_left"`
		State      string   `json:"state"`
	}
	if err := c.getJSON(ctx, "/api/v2/torrents/info", &torrents); err != nil {
		return false, fmt.Errorf("check incomplete downloads: %w", err)
	}
	for _, torrent := range torrents {
		if torrent.Progress == nil || torrent.AmountLeft == nil || strings.TrimSpace(torrent.State) == "" {
			return false, fmt.Errorf("check incomplete downloads: torrent %q has incomplete status data", torrent.Hash)
		}
		if *torrent.Progress < 0 || *torrent.Progress > 1 || *torrent.AmountLeft < 0 {
			return false, fmt.Errorf("check incomplete downloads: torrent %q has invalid status data", torrent.Hash)
		}
		if *torrent.Progress < 1 || *torrent.AmountLeft > 0 || shutdownBlockedByState(torrent.State) {
			return false, nil
		}
	}
	resp, err := httpx.Do(ctx, c.http, func() (*http.Request, error) {
		return http.NewRequest(http.MethodPost, c.base+"/api/v2/app/shutdown", nil)
	})
	if err != nil {
		return false, fmt.Errorf("request qBittorrent shutdown: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("request qBittorrent shutdown: status %d", resp.StatusCode)
	}
	return true, nil
}

func shutdownBlockedByState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "downloading", "forceddl", "stalleddl":
		return true
	default:
		return false
	}
}

func (c *Client) getJSON(ctx context.Context, path string, v any) error {
	resp, err := httpx.Do(ctx, c.http, func() (*http.Request, error) { return http.NewRequest(http.MethodGet, c.base+path, nil) })
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("qBittorrent API status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v); err != nil {
		return fmt.Errorf("decode qBittorrent response: %w", err)
	}
	return nil
}
