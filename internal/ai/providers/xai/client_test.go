package xai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/ai"
	"github.com/SleepingCat/PlexLink/internal/model"
)

func TestResponsesRequestUsesOnlyWebSearchAndStrictSchema(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("request %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "web_search_call"}, map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": `{"status":"resolved","media_type":"movie","canonical_title":"Sling Blade","localized_titles":[],"year":1996,"season":0,"search_queries":["Sling Blade 1996"],"selected_tmdb_id":null,"episode_mappings":[],"confidence":0.97,"evidence_summary":["year"]}`}}}}})
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL + "/v1", APIKey: "secret", Model: "configured-model", ReasoningEffort: "low", MaxOutputTokens: 1200}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Resolve(context.Background(), ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie, WebSearch: ai.WebRequire})
	if err != nil || result.CanonicalTitle != "Sling Blade" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if got["model"] != "configured-model" || got["store"] != false {
		t.Fatalf("request=%+v", got)
	}
	tools := got["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["type"] != "web_search" {
		t.Fatalf("tools=%+v", tools)
	}
	format := got["text"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["strict"] != true {
		t.Fatalf("format=%+v", format)
	}
}

func TestRequiredWebSearchMustBeObserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{}"}]}]}`))
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL, APIKey: "secret", Model: "model"}, server.Client())
	if _, err := client.Resolve(context.Background(), ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie, WebSearch: ai.WebRequire}); err == nil {
		t.Fatal("missing web search accepted")
	} else if ai.ProviderRequestsFromError(err) != 1 {
		t.Fatalf("provider requests=%d, want 1", ai.ProviderRequestsFromError(err))
	}
}

func TestHTTPFailureRetainsProviderRequestCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL, APIKey: "secret", Model: "model"}, server.Client())
	_, err := client.Resolve(context.Background(), ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie})
	if err == nil || ai.ProviderRequestsFromError(err) != 1 {
		t.Fatalf("err=%v requests=%d", err, ai.ProviderRequestsFromError(err))
	}
}
