package groq

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SleepingCat/PlexLink/internal/ai"
	"github.com/SleepingCat/PlexLink/internal/model"
)

func TestIdentityRequestUsesCompoundWebSearchAndParsesHypothesis(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("path=%q headers=%v", r.URL.Path, r.Header)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		writeResponse(w, "groq/compound-mini", `{"original_title":"Sling Blade","year":1996,"kind":"movie","confidence":0.95}`)
	}))
	defer server.Close()
	client := newTestClient(t, server, server.Client())
	result, err := client.Resolve(context.Background(), ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie, TorrentName: "Ottochennoe.Lezvie.1996.RUS.HDRip", WebSearch: ai.WebRequire})
	if err != nil {
		t.Fatal(err)
	}
	if result.CanonicalTitle != "Sling Blade" || result.Year != 1996 || result.ProviderRequests != 1 || result.ActualModel != "groq/compound-mini" || result.WebSearchUsed == nil || !*result.WebSearchUsed {
		t.Fatalf("result=%+v", result)
	}
	tools := got["compound_custom"].(map[string]any)["tools"].(map[string]any)["enabled_tools"].([]any)
	messages := got["messages"].([]any)
	prompt := messages[0].(map[string]any)["content"].(string)
	last := messages[len(messages)-1].(map[string]any)
	if len(got) != 3 || got["model"] != "groq/compound-mini" || len(messages) < 2 || messages[0].(map[string]any)["role"] != "system" || last["role"] != "user" || len(tools) != 1 || tools[0] != "web_search" {
		t.Fatalf("wire=%+v", got)
	}
	for _, forbidden := range []string{"temperature", "max_completion_tokens", "citation_options", "response_format", "reasoning_effort", "reasoning_format"} {
		if got[forbidden] != nil {
			t.Fatalf("unexpected %s in request", forbidden)
		}
	}
	if strings.Contains(prompt, "Ottochennoe.Lezvie.1996.RUS.HDRip") || strings.Contains(prompt, "Sling Blade") || !strings.Contains(prompt, "MUST use web search") || !strings.Contains(prompt, "untrusted data") {
		t.Fatalf("prompt=%q", prompt)
	}
	if user := last["content"].(string); !strings.Contains(user, "<release_name>Ottochennoe.Lezvie.1996.RUS.HDRip</release_name>") {
		t.Fatalf("user message=%q", user)
	}
	if result.ProviderRequest == "" || strings.Contains(result.ProviderRequest, "test-key") {
		t.Fatalf("unsafe/missing sanitized request: %q", result.ProviderRequest)
	}
	if got := client.Capabilities(); got.StructuredOutput || !got.WebSearch || got.StructuredOutputWithWebSearch {
		t.Fatalf("capabilities=%+v", got)
	}
}

func TestEmptyWhitespaceAndInvalidOutputAreOneRequestFailures(t *testing.T) {
	tests := []struct{ name, content string }{{"empty", ""}, {"whitespace", "  \n\t"}, {"invalid JSON", "Here is the result: Sling Blade"}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				writeResponse(w, "groq/compound-mini", tt.content)
			}))
			defer server.Close()
			_, err := newTestClient(t, server, server.Client()).Resolve(context.Background(), identityRequest(model.KindMovie))
			if err == nil || calls.Load() != 1 || ai.ProviderRequestsFromError(err) != 1 {
				t.Fatalf("calls=%d requests=%d err=%v", calls.Load(), ai.ProviderRequestsFromError(err), err)
			}
		})
	}
}

func TestHTTPFailuresAreNeverRetriedAndPreserveDiagnostics(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"code":"provider_failure","message":"invalid compound field for test-key"}}`))
			}))
			defer server.Close()
			_, err := newTestClient(t, server, server.Client()).Resolve(context.Background(), identityRequest(model.KindMovie))
			diagnostic, ok := ai.ProviderHTTPDiagnostics(err)
			if !ok || diagnostic.Provider != "groq" || diagnostic.StatusCode != status || diagnostic.ErrorCode != "provider_failure" || diagnostic.Message != "invalid compound field for [REDACTED]" || !strings.Contains(diagnostic.SanitizedResponse, `"code":"provider_failure"`) || strings.Contains(diagnostic.SanitizedResponse, "test-key") || diagnostic.SanitizedRequest == "" || strings.Contains(diagnostic.SanitizedRequest, "test-key") || calls.Load() != 1 || ai.ProviderRequestsFromError(err) != 1 {
				t.Fatalf("calls=%d diagnostic=%+v err=%v", calls.Load(), diagnostic, err)
			}
		})
	}
}

func TestProviderTimeoutAndCallerCancellationRemainDistinct(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		writeResponse(w, "model", `{}`)
	}))
	defer server.Close()
	localHTTP := server.Client()
	localHTTP.Timeout = 5 * time.Millisecond
	client := newTestClient(t, server, localHTTP)
	_, err := client.Resolve(context.Background(), identityRequest(model.KindMovie))
	if err == nil || errors.Is(err, context.Canceled) || ai.ProviderRequestsFromError(err) != 1 {
		t.Fatalf("provider timeout err=%v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Resolve(ctx, identityRequest(model.KindMovie))
	if !errors.Is(err, context.Canceled) || ai.ProviderRequestsFromError(err) != 1 {
		t.Fatalf("caller cancellation err=%v", err)
	}
}

func TestNullAndLowConfidenceAreUnknownAndNotCacheable(t *testing.T) {
	for _, content := range []string{
		`{"original_title":null,"year":1996,"kind":"movie","confidence":0.25}`,
		`{"original_title":"Sling Blade","year":1996,"kind":"movie","confidence":0.89}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeResponse(w, "model", content) }))
		result, err := newTestClient(t, server, server.Client()).Resolve(context.Background(), identityRequest(model.KindMovie))
		server.Close()
		if err != nil || result.Status != ai.Unknown || ai.CacheableResult(identityRequest(model.KindMovie), result) {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
}

func TestTVIdentityAndIdentityOnlyScope(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeResponse(w, "groq/compound-mini", `{"original_title":"Counterpart","year":2017,"kind":"tv","confidence":0.95}`)
	}))
	defer server.Close()
	client := newTestClient(t, server, server.Client())
	result, err := client.Resolve(context.Background(), identityRequest(model.KindTV))
	if err != nil || result.CanonicalTitle != "Counterpart" || result.MediaType != model.KindTV {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, task := range []ai.Task{ai.SelectCandidate, ai.MapEpisodes} {
		_, err := client.Resolve(context.Background(), ai.Request{Task: task, Kind: model.KindTV, WebSearch: ai.WebRequire})
		if !errors.Is(err, ai.ErrUnsupportedCapability) {
			t.Fatalf("task=%s err=%v", task, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d, want one identity request and no episode requests", calls.Load())
	}
}

func identityRequest(kind model.Kind) ai.Request {
	return ai.Request{Task: ai.IdentifyMedia, Kind: kind, TorrentName: "release", WebSearch: ai.WebRequire}
}

func newTestClient(t *testing.T, server *httptest.Server, httpClient *http.Client) *Client {
	t.Helper()
	client, err := New(Config{BaseURL: server.URL + "/openai/v1", APIKey: "test-key", Model: "groq/compound-mini", MinConfidence: .9}, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeResponse(w http.ResponseWriter, modelName, content string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"model": modelName, "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content}}}})
}
