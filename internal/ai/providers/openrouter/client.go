package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SleepingCat/PlexLink/internal/ai"
)

const (
	maxResponseBytes = 2 << 20
	maxErrorBytes    = 1024
	maxAttempts      = 3
)

type Config struct {
	BaseURL         string
	APIKey          string
	Model           string
	MaxOutputTokens int
}

type Client struct {
	config Config
	http   *http.Client
}

func New(config Config, client *http.Client) (*Client, error) {
	if strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.APIKey) == "" || strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("OpenRouter base URL, API key, and model are required")
	}
	if config.MaxOutputTokens < 0 {
		return nil, errors.New("OpenRouter max output tokens cannot be negative")
	}
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	return &Client{config: config, http: client}, nil
}

func (c *Client) Capabilities() ai.Capabilities {
	return ai.Capabilities{StructuredOutput: true}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type request struct {
	Model          string         `json:"model"`
	Messages       []message      `json:"messages"`
	MaxTokens      int            `json:"max_tokens,omitempty"`
	ResponseFormat map[string]any `json:"response_format"`
	Provider       provider       `json:"provider"`
}

type provider struct {
	RequireParameters bool `json:"require_parameters"`
}

type response struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string  `json:"finish_reason"`
		Message      message `json:"message"`
	} `json:"choices"`
}

type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("OpenRouter API status %d", e.StatusCode)
	}
	return fmt.Sprintf("OpenRouter API status %d: %s", e.StatusCode, e.Body)
}

func (c *Client) Resolve(ctx context.Context, req ai.Request) (ai.Result, error) {
	if req.WebSearch == ai.WebRequire {
		return ai.Result{}, fmt.Errorf("%w: OpenRouter web search", ai.ErrUnsupportedCapability)
	}
	evidence, err := json.Marshal(req)
	if err != nil {
		return ai.Result{}, fmt.Errorf("encode AI request: %w", err)
	}
	body := request{
		Model:     c.config.Model,
		Messages:  []message{{Role: "system", Content: ai.SystemPrompt(req.Task, req.WebSearch)}, {Role: "user", Content: string(evidence)}},
		MaxTokens: c.config.MaxOutputTokens,
		ResponseFormat: map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name": "plexlink_media_resolution", "strict": true, "schema": ai.ResultSchema(),
		}},
		Provider: provider{RequireParameters: true},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ai.Result{}, fmt.Errorf("encode OpenRouter request: %w", err)
	}

	wire, attempts, err := c.call(ctx, payload)
	if err != nil {
		return ai.Result{}, ai.WithProviderRequests(err, attempts)
	}
	if len(wire.Choices) == 0 {
		return ai.Result{}, ai.WithProviderRequests(fmt.Errorf("%w: OpenRouter returned no choices", ai.ErrProviderOutput), attempts)
	}
	choice := wire.Choices[0]
	if choice.FinishReason == "length" {
		return ai.Result{}, ai.WithProviderRequests(fmt.Errorf("%w: OpenRouter output token limit reached", ai.ErrProviderOutput), attempts)
	}
	if strings.TrimSpace(choice.Message.Content) == "" {
		return ai.Result{}, ai.WithProviderRequests(fmt.Errorf("%w: OpenRouter returned empty message content", ai.ErrProviderOutput), attempts)
	}
	var result ai.Result
	if err := json.Unmarshal([]byte(choice.Message.Content), &result); err != nil {
		return ai.Result{}, ai.WithProviderRequests(fmt.Errorf("%w: decode OpenRouter structured output: %v", ai.ErrProviderOutput, err), attempts)
	}
	webUsed := false
	result.WebSearchUsed = &webUsed
	result.ProviderRequests = attempts
	result.ActualModel = wire.Model
	if err := ai.Validate(req, result); err != nil {
		return ai.Result{}, ai.WithProviderRequests(err, attempts)
	}
	return result, nil
}

func (c *Client) call(ctx context.Context, payload []byte) (response, int, error) {
	attempts := 0
	for attempt := 0; attempt < maxAttempts; attempt++ {
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.config.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
		if err != nil {
			return response{}, attempts, fmt.Errorf("create OpenRouter request: %w", err)
		}
		r.Header.Set("Authorization", "Bearer "+c.config.APIKey)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")
		attempts++
		resp, err := c.http.Do(r)
		if err != nil {
			return response{}, attempts, fmt.Errorf("OpenRouter request: %w", err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		if readErr != nil {
			return response{}, attempts, fmt.Errorf("read OpenRouter response: %w", readErr)
		}
		if transient(resp.StatusCode) && attempt+1 < maxAttempts {
			if err := wait(ctx, retryDelay(resp, attempt)); err != nil {
				return response{}, attempts, err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return response{}, attempts, &HTTPError{StatusCode: resp.StatusCode, Body: c.safeError(raw)}
		}
		var wire response
		if err := json.Unmarshal(raw, &wire); err != nil {
			return response{}, attempts, fmt.Errorf("%w: decode OpenRouter response: %v", ai.ErrProviderOutput, err)
		}
		return wire, attempts, nil
	}
	return response{}, attempts, errors.New("OpenRouter request exhausted retries")
}

func (c *Client) safeError(raw []byte) string {
	message := strings.TrimSpace(string(raw))
	message = strings.ReplaceAll(message, c.config.APIKey, "[REDACTED]")
	if len(message) > maxErrorBytes {
		message = message[:maxErrorBytes]
	}
	return message
}

func transient(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func retryDelay(resp *http.Response, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Duration(1<<attempt) * 200 * time.Millisecond
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
