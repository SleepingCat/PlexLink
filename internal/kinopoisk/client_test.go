package kinopoisk

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefaultBaseURLHasNoAPIVersion(t *testing.T) {
	client := NewClient("", "secret", http.DefaultClient)
	if client.baseURL != "https://api.poiskkino.dev" || client.resultLimit != 10 {
		t.Fatalf("baseURL=%q limit=%d", client.baseURL, client.resultLimit)
	}
}

func TestTokenUsesCurrentEndpointAndParsesQuota(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1.5/token" || req.Header.Get("X-API-KEY") != "secret" {
			t.Errorf("request path=%q key=%q", req.URL.Path, req.Header.Get("X-API-KEY"))
		}
		fmt.Fprint(w, `{"requestsLimit":200,"requestsUsed":12,"requestsRemaining":188,"ttl":3600,"resetAt":"2026-08-22T00:00:00.000Z"}`)
	}))
	defer server.Close()
	info, err := NewClient(server.URL, "secret", server.Client()).Token(context.Background())
	if err != nil || info.RequestsLimit != 200 || info.RequestsUsed != 12 || info.RequestsRemaining != 188 || info.TTL != 3600 || info.ResetAt == "" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
}

func TestTokenRejectsHTTPAndMalformedSchema(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", http.StatusUnauthorized, `{}`},
		{"malformed", http.StatusOK, `{"requestsLimit":200}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			if _, err := NewClient(server.URL, "secret", server.Client()).Token(context.Background()); err == nil {
				t.Fatal("invalid token response accepted")
			}
		})
	}
}

func TestSearchCachesSuccessfulQueryWithinProcess(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		fmt.Fprint(w, `{"docs":[{"id":301,"name":"Матрица","year":1999,"type":"movie","externalId":{"tmdb":603}}],"total":1,"limit":10,"page":1,"pages":1}`)
	}))
	defer server.Close()
	client := NewClient(server.URL, "secret", server.Client())
	for _, query := range []string{"Матрица", "  матрица "} {
		response, err := client.Search(context.Background(), query)
		if err != nil || len(response.Docs) != 1 || response.Docs[0].ID != 301 {
			t.Fatalf("response=%+v err=%v", response, err)
		}
	}
	if requests != 1 {
		t.Fatalf("HTTP requests=%d, want 1", requests)
	}
}

func TestSearchLatchesDailyQuotaWithoutMoreHTTPRequests(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"statusCode":403,"error":"Forbidden","message":"Вы израсходовали ваш суточный лимит по запросам."}`)
	}))
	defer server.Close()
	client := NewClient(server.URL, "secret", server.Client())
	for _, query := range []string{"Матрица", "Иваново детство"} {
		_, err := client.Search(context.Background(), query)
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || !httpErr.DailyQuota || httpErr.StatusCode != http.StatusForbidden {
			t.Fatalf("query=%q err=%+v", query, err)
		}
	}
	if requests != 1 {
		t.Fatalf("HTTP requests=%d, want 1", requests)
	}
}
