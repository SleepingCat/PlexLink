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
	maxAttempts      = 2
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
		return nil, errors.New("OpenRouter base URL, API key, and model are required")
	}
	if config.MaxOutputTokens < 0 {
		return nil, errors.New("OpenRouter max output tokens cannot be negative")
	}
	if config.ReasoningEffort == "" {
		config.ReasoningEffort = "minimal"
	}
	switch config.ReasoningEffort {
	case "none", "minimal", "low", "medium", "high":
	default:
		return nil, errors.New("OpenRouter reasoning effort must be none, minimal, low, medium, or high")
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
	Reasoning      reasoning      `json:"reasoning"`
}

type provider struct {
	RequireParameters bool `json:"require_parameters"`
}

type reasoning struct {
	Effort string `json:"effort"`
}

type response struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string  `json:"finish_reason"`
		Message      message `json:"message"`
	} `json:"choices"`
	Usage struct {
		CompletionTokens       int `json:"completion_tokens"`
		ReasoningTokens        int `json:"reasoning_tokens"`
		CompletionTokenDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
}

type HTTPError = ai.ProviderHTTPError

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
			"name": "plexlink_media_resolution", "strict": true, "schema": ai.CompactResultSchema(req.Task),
		}},
		Provider:  provider{RequireParameters: true},
		Reasoning: reasoning{Effort: c.config.ReasoningEffort},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ai.Result{}, fmt.Errorf("encode OpenRouter request: %w", err)
	}

	requests := 0
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		wire, err := c.call(ctx, payload)
		requests++
		if err != nil {
			return ai.Result{}, ai.WithProviderRequests(err, requests)
		}
		result, retryable, err := c.decode(req, wire)
		if err == nil {
			result.ProviderRequests = requests
			return result, nil
		}
		lastErr = err
		if !retryable {
			break
		}
	}
	return ai.Result{}, ai.WithProviderRequests(lastErr, requests)
}

func (c *Client) decode(req ai.Request, wire response) (ai.Result, bool, error) {
	if len(wire.Choices) == 0 {
		return ai.Result{}, true, fmt.Errorf("%w: OpenRouter returned no choices", ai.ErrProviderOutput)
	}
	choice := wire.Choices[0]
	if choice.FinishReason == "length" {
		reasoningTokens := wire.Usage.CompletionTokenDetails.ReasoningTokens
		if reasoningTokens == 0 {
			reasoningTokens = wire.Usage.ReasoningTokens
		}
		return ai.Result{}, true, &ai.ProviderOutputError{Err: fmt.Errorf("%w: OpenRouter output token limit reached", ai.ErrProviderOutput), ConfiguredModel: c.config.Model, ActualModel: wire.Model, FinishReason: choice.FinishReason, CompletionTokens: wire.Usage.CompletionTokens, ReasoningTokens: reasoningTokens}
	}
	if strings.TrimSpace(choice.Message.Content) == "" {
		return ai.Result{}, true, fmt.Errorf("%w: OpenRouter returned empty message content", ai.ErrProviderOutput)
	}
	var result ai.Result
	if err := json.Unmarshal([]byte(choice.Message.Content), &result); err != nil {
		return ai.Result{}, true, fmt.Errorf("%w: decode OpenRouter structured output: %v", ai.ErrProviderOutput, err)
	}
	result.MediaType = req.Kind
	webUsed := false
	result.WebSearchUsed = &webUsed
	result.ActualModel = wire.Model
	if err := ai.Validate(req, result); err != nil {
		return ai.Result{}, true, err
	}
	return result, false, nil
}

func (c *Client) call(ctx context.Context, payload []byte) (response, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.config.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return response{}, fmt.Errorf("create OpenRouter request: %w", err)
	}
	r.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(r)
	if err != nil {
		return response{}, fmt.Errorf("OpenRouter request: %w", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	resp.Body.Close()
	if readErr != nil {
		return response{}, fmt.Errorf("read OpenRouter response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retryAfter, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return response{}, &HTTPError{Provider: "openrouter", StatusCode: resp.StatusCode, ErrorCode: providerErrorCode(raw), RetryAfterSeconds: retryAfter, Message: c.safeError(raw)}
	}
	var wire response
	if err := json.Unmarshal(raw, &wire); err != nil {
		return response{}, fmt.Errorf("%w: decode OpenRouter response: %v", ai.ErrProviderOutput, err)
	}
	return wire, nil
}

func providerErrorCode(raw []byte) string {
	var envelope struct {
		Error struct {
			Code any `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Error.Code == nil {
		return ""
	}
	return fmt.Sprint(envelope.Error.Code)
}

func (c *Client) safeError(raw []byte) string {
	message := strings.TrimSpace(string(raw))
	message = strings.ReplaceAll(message, c.config.APIKey, "[REDACTED]")
	if len(message) > maxErrorBytes {
		message = message[:maxErrorBytes]
	}
	return message
}
