package kinopoisk

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/ensemble"
	"github.com/SleepingCat/PlexLink/internal/model"
)

func TestResolverSearchHeaderUnicodeBoundedAndDedupe(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("X-API-KEY"); got != "secret-key" {
			t.Errorf("API key = %q", got)
		}
		mu.Lock()
		queries = append(queries, req.URL.Query().Get("query"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"docs":[{"id":42,"name":"Забавные Игры","alternativeName":"Funny Games","names":[{"name":"Забавные Игры","language":"ru"}],"year":1997,"type":"movie","externalId":{"tmdb":10234,"imdb":"tt0119167"}}]}`)
	}))
	defer server.Close()
	r := NewResolver(NewClient(server.URL, "secret-key", server.Client()))
	result := r.Resolve(context.Background(), ensemble.ResolveRequest{
		Kind: model.KindMovie, Title: "Забавные Игры", TitleHypotheses: []string{"Забавные Игры", "Funny Games", "Third", "Fourth", "Fifth"}, Year: 1997,
	})
	if result.Status != ensemble.ResolverOK || len(result.Candidates) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if len(queries) != maxQueries {
		t.Fatalf("queries = %#v", queries)
	}
	if queries[0] != "Забавные Игры" {
		t.Fatalf("unicode query = %q", queries[0])
	}
	candidate := result.Candidates[0]
	if candidate.Identity.TMDBID != 10234 || candidate.Identity.IMDbID != "tt0119167" {
		t.Fatalf("identity = %+v", candidate.Identity)
	}
	assertEvidence(t, candidate, ensemble.EvidenceTitleExactLocalized)
	assertEvidence(t, candidate, ensemble.EvidenceExternalTMDBExact)
	assertEvidence(t, candidate, ensemble.EvidenceYearPrimaryExact)
}

func TestResolverMediaKindFiltering(t *testing.T) {
	body := `{"docs":[
		{"id":1,"name":"M","type":"movie"}, {"id":2,"name":"C","type":"cartoon"},
		{"id":3,"name":"T","type":"tv-series"}, {"id":4,"name":"AS","type":"animated-series"},
		{"id":5,"name":"A","type":"anime"}]}`
	for _, test := range []struct {
		kind model.Kind
		want []string
	}{
		{model.KindMovie, []string{"1", "2"}}, {model.KindTV, []string{"3", "4"}}, {model.KindAnime, []string{"4", "5"}},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			r, closeServer := testResolver(t, http.StatusOK, body)
			defer closeServer()
			got := r.Resolve(context.Background(), ensemble.ResolveRequest{Kind: test.kind, Title: "query"})
			if got.Status != ensemble.ResolverOK || len(got.Candidates) != len(test.want) {
				t.Fatalf("result = %+v", got)
			}
			for i := range test.want {
				if got.Candidates[i].Identity.ProviderID != test.want[i] {
					t.Fatalf("candidate IDs = %+v", got.Candidates)
				}
			}
		})
	}
}

func TestResolverAlternativeNameIsAKA(t *testing.T) {
	r, closeServer := testResolver(t, http.StatusOK, `{"docs":[{"id":1,"name":"Острое лезвие","alternativeName":"Sling Blade","type":"movie"}]}`)
	defer closeServer()
	result := r.Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "Sling Blade"})
	assertEvidence(t, result.Candidates[0], ensemble.EvidenceTitleExactAKA)
}

func TestResolverEmptyResponseAbstains(t *testing.T) {
	r, closeServer := testResolver(t, http.StatusOK, `{"docs":[]}`)
	defer closeServer()
	result := r.Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "none"})
	if result.Status != ensemble.ResolverAbstain || result.Error != nil {
		t.Fatalf("result = %+v", result)
	}
}

func TestResolverPartialFailureKeepsUsefulResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("query") == "bad" {
			http.Error(w, "provider secret response", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"docs":[{"id":1,"name":"good","type":"movie"}]}`)
	}))
	defer server.Close()
	result := NewResolver(NewClient(server.URL, "key", server.Client())).Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "bad", TitleHypotheses: []string{"good"}})
	if result.Status != ensemble.ResolverOK || len(result.Candidates) != 1 || len(result.Warnings) != 1 || result.Error != nil {
		t.Fatalf("result = %+v", result)
	}
}

func TestResolverHTTPFailuresAreSafe(t *testing.T) {
	for _, test := range []struct {
		status int
		kind   ensemble.OperationalErrorKind
		retry  bool
	}{
		{401, ensemble.ErrorAuthentication, false}, {403, ensemble.ErrorAuthentication, false},
		{429, ensemble.ErrorRateLimited, true}, {500, ensemble.ErrorProvider, true},
	} {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			r, closeServer := testResolver(t, test.status, `api-key secret-key raw-body`)
			defer closeServer()
			result := r.Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "query"})
			if result.Status != ensemble.ResolverError || result.Error == nil || result.Error.Kind != test.kind || result.Error.Retryable != test.retry {
				t.Fatalf("result = %+v", result)
			}
			formatted := fmt.Sprintf("%+v", result)
			if strings.Contains(formatted, "secret-key") || strings.Contains(formatted, "raw-body") {
				t.Fatalf("credential/body leaked: %s", formatted)
			}
		})
	}
}

func TestResolverInvalidResponseAndCancellation(t *testing.T) {
	t.Run("invalid", func(t *testing.T) {
		r, closeServer := testResolver(t, http.StatusOK, `{not-json`)
		defer closeServer()
		result := r.Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "query"})
		if result.Error == nil || result.Error.Kind != ensemble.ErrorInvalidResponse {
			t.Fatalf("result = %+v", result)
		}
	})
	t.Run("missing docs", func(t *testing.T) {
		r, closeServer := testResolver(t, http.StatusOK, `{}`)
		defer closeServer()
		result := r.Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "query"})
		if result.Error == nil || result.Error.Kind != ensemble.ErrorInvalidResponse {
			t.Fatalf("result = %+v", result)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := NewResolver(NewClient("http://unused.invalid", "key", http.DefaultClient))
		result := r.Resolve(ctx, ensemble.ResolveRequest{Kind: model.KindMovie, Title: "query"})
		if result.Error == nil || result.Error.Kind != ensemble.ErrorCanceled {
			t.Fatalf("result = %+v", result)
		}
	})
}

func TestResolverMissingCredentialIsConfigurationError(t *testing.T) {
	result := NewResolver(NewClient("", "", nil)).Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "query"})
	if result.Error == nil || result.Error.Kind != ensemble.ErrorConfiguration {
		t.Fatalf("result = %+v", result)
	}
}

func testResolver(t *testing.T, status int, body string) (*Resolver, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status); _, _ = fmt.Fprint(w, body) }))
	return NewResolver(NewClient(server.URL, "key", server.Client())), server.Close
}

func assertEvidence(t *testing.T, candidate ensemble.Candidate, typ ensemble.EvidenceType) {
	t.Helper()
	for _, evidence := range candidate.Evidence {
		if evidence.Type == typ {
			return
		}
	}
	t.Fatalf("missing evidence %s in %+v", typ, candidate.Evidence)
}
