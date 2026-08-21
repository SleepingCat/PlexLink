package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/ai"
	"github.com/SleepingCat/PlexLink/internal/model"
)

const validMovie = `{"status":"resolved","media_type":"movie","canonical_title":"Sling Blade","localized_titles":[],"year":1996,"season":0,"search_queries":["Sling Blade 1996"],"selected_tmdb_id":null,"episode_mappings":[],"confidence":0.97,"evidence_summary":["year"]}`

func TestRequestWireFormatAndResponse(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("path=%q auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		writeResponse(t, w, "backend/free-model", "stop", validMovie)
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL + "/api/v1", APIKey: "test-key", Model: "openrouter/free", MaxOutputTokens: 2048}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Resolve(context.Background(), ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie, TorrentName: "untrusted", WebSearch: ai.WebNever})
	if err != nil {
		t.Fatal(err)
	}
	if result.CanonicalTitle != "Sling Blade" || result.ActualModel != "backend/free-model" || result.ProviderRequests != 1 || result.WebSearchUsed == nil || *result.WebSearchUsed {
		t.Fatalf("result=%+v", result)
	}
	if got["model"] != "openrouter/free" || got["max_tokens"] != float64(2048) || got["max_output_tokens"] != nil {
		t.Fatalf("token/model mapping=%+v", got)
	}
	format := got["response_format"].(map[string]any)
	schema := format["json_schema"].(map[string]any)
	provider := got["provider"].(map[string]any)
	messages := got["messages"].([]any)
	if format["type"] != "json_schema" || schema["strict"] != true || schema["schema"] == nil || provider["require_parameters"] != true || len(messages) != 2 {
		t.Fatalf("structured request=%+v", got)
	}
	for _, forbidden := range []string{"input", "tools", "generation_config", "reasoning", "store"} {
		if got[forbidden] != nil {
			t.Fatalf("provider-specific field %q leaked", forbidden)
		}
	}
}

func TestWebSearchCapabilities(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeResponse(t, w, "model", "stop", validMovie)
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL, APIKey: "key", Model: "model"}, server.Client())
	if got := client.Capabilities(); !got.StructuredOutput || got.WebSearch || got.StructuredOutputWithWebSearch {
		t.Fatalf("capabilities=%+v", got)
	}
	for _, policy := range []ai.WebSearchPolicy{ai.WebNever, ai.WebAllow} {
		if _, err := client.Resolve(context.Background(), ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie, WebSearch: policy}); err != nil {
			t.Fatalf("policy %s: %v", policy, err)
		}
	}
	_, err := client.Resolve(context.Background(), ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie, WebSearch: ai.WebRequire})
	if !errors.Is(err, ai.ErrUnsupportedCapability) || calls.Load() != 2 || ai.ProviderRequestsFromError(err) != 0 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}

func TestInvalidProviderOutputs(t *testing.T) {
	tests := []struct {
		name, response string
		req            ai.Request
	}{
		{"invalid JSON", `{"model":"m","choices":[{"finish_reason":"stop","message":{"content":"{"}}]}`, ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie}},
		{"missing choices", `{"model":"m","choices":[]}`, ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie}},
		{"length", `{"model":"m","choices":[{"finish_reason":"length","message":{"content":""}}]}`, ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie}},
		{"empty content", `{"model":"m","choices":[{"finish_reason":"stop","message":{"content":""}}]}`, ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie}},
		{"out of list ID", `{"model":"m","choices":[{"finish_reason":"stop","message":{"content":"{\"status\":\"resolved\",\"media_type\":\"movie\",\"canonical_title\":\"X\",\"localized_titles\":[],\"year\":1996,\"season\":0,\"search_queries\":[],\"selected_tmdb_id\":9,\"episode_mappings\":[],\"confidence\":0.9,\"evidence_summary\":[]}"}}]}`, ai.Request{Task: ai.SelectCandidate, Kind: model.KindMovie, Candidates: []ai.Candidate{{ID: 7}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tt.response)) }))
			defer server.Close()
			client, _ := New(Config{BaseURL: server.URL, APIKey: "key", Model: "model"}, server.Client())
			_, err := client.Resolve(context.Background(), tt.req)
			if err == nil || ai.ProviderRequestsFromError(err) != 1 {
				t.Fatalf("err=%v requests=%d", err, ai.ProviderRequestsFromError(err))
			}
		})
	}
}

func TestHTTPErrorRetryCountingAndRedaction(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "bad secret-key", status) }))
			defer server.Close()
			client, _ := New(Config{BaseURL: server.URL, APIKey: "secret-key", Model: "model"}, server.Client())
			_, err := client.Resolve(context.Background(), ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie})
			var providerErr *HTTPError
			if !errors.As(err, &providerErr) || providerErr.StatusCode != status || strings.Contains(err.Error(), "secret-key") || ai.ProviderRequestsFromError(err) != 1 {
				t.Fatalf("err=%v", err)
			}
		})
	}

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call := calls.Add(1)
		if call < 3 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "busy", http.StatusTooManyRequests)
			return
		}
		writeResponse(t, w, "model", "stop", validMovie)
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL, APIKey: "key", Model: "model"}, server.Client())
	result, err := client.Resolve(context.Background(), ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie})
	if err != nil || result.ProviderRequests != 3 || calls.Load() != 3 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls.Load(), err)
	}

	var unavailableCalls atomic.Int32
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		unavailableCalls.Add(1)
		w.Header().Set("Retry-After", "0")
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer unavailable.Close()
	client, _ = New(Config{BaseURL: unavailable.URL, APIKey: "key", Model: "model"}, unavailable.Client())
	_, err = client.Resolve(context.Background(), ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie})
	var providerErr *HTTPError
	if !errors.As(err, &providerErr) || providerErr.StatusCode != http.StatusServiceUnavailable || unavailableCalls.Load() != 3 || ai.ProviderRequestsFromError(err) != 3 {
		t.Fatalf("calls=%d err=%v", unavailableCalls.Load(), err)
	}
}

func TestContextCancellationCountsStartedRequest(t *testing.T) {
	started := make(chan struct{})
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	client, _ := New(Config{BaseURL: "https://openrouter.invalid/api/v1", APIKey: "key", Model: "model"}, httpClient)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	_, err := client.Resolve(ctx, ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie})
	if !errors.Is(err, context.Canceled) || ai.ProviderRequestsFromError(err) != 1 {
		t.Fatalf("err=%v requests=%d", err, ai.ProviderRequestsFromError(err))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func writeResponse(t *testing.T, w http.ResponseWriter, actualModel, finishReason, content string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"model": actualModel, "choices": []any{map[string]any{"finish_reason": finishReason, "message": map[string]any{"role": "assistant", "content": content}}}}); err != nil {
		t.Error(err)
	}
}
