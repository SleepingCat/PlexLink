package xai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/SleepingCat/PlexLink/internal/ai"
	"github.com/SleepingCat/PlexLink/internal/httpx"
)

type Config struct {
	BaseURL         string
	APIKey          string
	Model           string
	ReasoningEffort string
	MaxOutputTokens int
}

type Client struct {
	config Config
	http   *http.Client
}

func New(config Config, client *http.Client) (*Client, error) {
	if strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.APIKey) == "" || strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("xAI base URL, API key, and model are required")
	}
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	return &Client{config: config, http: client}, nil
}

func (c *Client) Capabilities() ai.Capabilities {
	return ai.Capabilities{StructuredOutput: true, WebSearch: true}
}

type input struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type request struct {
	Model           string              `json:"model"`
	Input           []input             `json:"input"`
	Tools           []map[string]string `json:"tools,omitempty"`
	ToolChoice      string              `json:"tool_choice,omitempty"`
	Text            map[string]any      `json:"text"`
	Reasoning       map[string]string   `json:"reasoning,omitempty"`
	MaxOutputTokens int                 `json:"max_output_tokens,omitempty"`
	Store           bool                `json:"store"`
}
type response struct {
	Status string `json:"status"`
	Output []struct {
		Type    string                        `json:"type"`
		Content []struct{ Type, Text string } `json:"content"`
	} `json:"output"`
}

func (c *Client) Resolve(ctx context.Context, req ai.Request) (ai.Result, error) {
	user, err := json.Marshal(req)
	if err != nil {
		return ai.Result{}, fmt.Errorf("encode AI request: %w", err)
	}
	body := request{Model: c.config.Model, Input: []input{{"system", ai.SystemPrompt(req.Task, req.WebSearch)}, {"user", string(user)}}, MaxOutputTokens: c.config.MaxOutputTokens, Store: false,
		Text: map[string]any{"format": map[string]any{"type": "json_schema", "name": "plexlink_media_resolution", "schema": ai.ResultSchema(), "strict": true}},
	}
	if c.config.ReasoningEffort != "" {
		body.Reasoning = map[string]string{"effort": c.config.ReasoningEffort}
	}
	if req.WebSearch != ai.WebNever {
		body.Tools = []map[string]string{{"type": "web_search"}}
		if req.WebSearch == ai.WebRequire {
			body.ToolChoice = "required"
		}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ai.Result{}, fmt.Errorf("encode xAI request: %w", err)
	}
	resp, err := httpx.Do(ctx, c.http, func() (*http.Request, error) {
		r, e := http.NewRequest(http.MethodPost, strings.TrimRight(c.config.BaseURL, "/")+"/responses", bytes.NewReader(payload))
		if e == nil {
			r.Header.Set("Authorization", "Bearer "+c.config.APIKey)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json")
		}
		return r, e
	})
	if err != nil {
		return ai.Result{}, fmt.Errorf("xAI request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return ai.Result{}, fmt.Errorf("xAI API status %d", resp.StatusCode)
	}
	var wire response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&wire); err != nil {
		return ai.Result{}, fmt.Errorf("decode xAI response: %w", err)
	}
	if wire.Status != "completed" {
		return ai.Result{}, fmt.Errorf("xAI response status %q", wire.Status)
	}
	webUsed := false
	var output string
	for _, item := range wire.Output {
		if item.Type == "web_search_call" {
			webUsed = true
		}
		if item.Type == "message" {
			for _, content := range item.Content {
				if content.Type == "output_text" {
					output = content.Text
				}
			}
		}
	}
	if req.WebSearch == ai.WebRequire && !webUsed {
		return ai.Result{}, fmt.Errorf("%w: required web search was not used", ai.ErrInvalidResult)
	}
	if output == "" {
		return ai.Result{}, fmt.Errorf("%w: xAI returned no output_text", ai.ErrInvalidResult)
	}
	var result ai.Result
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return ai.Result{}, fmt.Errorf("%w: decode structured output: %v", ai.ErrInvalidResult, err)
	}
	result.WebSearchUsed = &webUsed
	if err := ai.Validate(req, result); err != nil {
		return ai.Result{}, err
	}
	return result, nil
}
