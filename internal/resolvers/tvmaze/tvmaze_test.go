package tvmaze

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/ensemble"
	"github.com/SleepingCat/PlexLink/internal/model"
)

func TestResolverSupportsSeriesOnly(t *testing.T) {
	r := NewResolver(nil)
	if r.Supports(model.KindMovie) || !r.Supports(model.KindTV) || !r.Supports(model.KindAnime) {
		t.Fatal("unexpected kind support")
	}
	result := r.Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "Alien"})
	if result.Status != ensemble.ResolverAbstain {
		t.Fatalf("movie status = %s", result.Status)
	}
}

func TestResolverPreservesAmbiguousResultsAndEmitsEvidence(t *testing.T) {
	var mu sync.Mutex
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.RequestURI())
		mu.Unlock()
		switch r.URL.Path {
		case "/search/shows":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
                    {"score":0.99,"show":{"id":1,"name":"The Office","premiered":"2005-03-24","externals":{"imdb":"tt0386676","thetvdb":73244}}},
                    {"score":0.91,"show":{"id":2,"name":"The Office","premiered":"2001-07-09","externals":{"imdb":"tt0290978"}}}
                ]`))
		case "/shows/1/akas":
			_, _ = w.Write([]byte(`[{"name":"Офис","country":{"name":"Russian Federation","code":"RU"}}]`))
		case "/shows/2/akas":
			_, _ = w.Write([]byte(`[]`))
		case "/shows/1/seasons", "/shows/2/seasons":
			_, _ = w.Write([]byte(`[{"id":11,"number":2,"episodeOrder":22}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := NewResolver(New(server.URL, server.Client()))
	result := r.Resolve(context.Background(), ensemble.ResolveRequest{
		Kind: model.KindTV, Title: "The Office", TitleHypotheses: []string{"The Office", "  the office "}, ParsedEvidence: model.Evidence{Titles: []model.WeightedTitle{{Title: "Офис"}}}, Year: 2005, Season: 2,
	})
	if result.Status != ensemble.ResolverOK || len(result.Candidates) != 2 {
		t.Fatalf("result = %+v", result)
	}
	first := result.Candidates[0]
	if first.Identity.ProviderID != "tvmaze:1" || first.Identity.IMDbID != "tt0386676" || first.Identity.Year != 2005 {
		t.Fatalf("identity = %+v", first.Identity)
	}
	for _, typ := range []ensemble.EvidenceType{ensemble.EvidenceTitleExactCanonical, ensemble.EvidenceTitleExactAKA, ensemble.EvidenceYearPrimaryExact, ensemble.EvidenceSeasonExists} {
		if !hasEvidence(first, typ) {
			t.Errorf("missing evidence %s: %+v", typ, first.Evidence)
		}
	}
	searches := 0
	for _, request := range requests {
		if request == "/search/shows?q=The+Office" || request == "/search/shows?q=%D0%9E%D1%84%D0%B8%D1%81" {
			searches++
		}
	}
	if searches != 2 {
		t.Fatalf("search requests = %v", requests)
	}
}

func TestResolverPreservesCandidatesWhenLaterProviderCallFails(t *testing.T) {
	searches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/shows":
			searches++
			if searches == 1 {
				_, _ = w.Write([]byte(`[{"show":{"id":7,"name":"Dark","premiered":"2017-12-01","externals":{}}}]`))
				return
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/shows/7/akas":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	result := NewResolver(New(server.URL, server.Client())).Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindTV, Title: "Dark", TitleHypotheses: []string{"Dark Netflix"}})
	if result.Status != ensemble.ResolverOK || len(result.Candidates) != 1 || len(result.Warnings) == 0 || result.Error != nil {
		t.Fatalf("result = %+v", result)
	}
}

func TestResolverDeduplicatesCandidatesAcrossQueries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search/shows" {
			_, _ = w.Write([]byte(`[{"show":{"id":7,"name":"Dark","premiered":"2017-12-01","externals":{}}}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	result := NewResolver(New(server.URL, server.Client())).Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindTV, Title: "Dark", TitleHypotheses: []string{"DARK", "Dark series"}})
	if len(result.Candidates) != 1 {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
}

func TestClientTypedEpisodeAndAlternateMethods(t *testing.T) {
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.RequestURI()] = true
		switch r.URL.Path {
		case "/shows/10/episodes":
			_, _ = w.Write([]byte(`[{"id":100,"name":"Special","season":0,"number":null,"type":"significant_special","airdate":"2020-01-01"}]`))
		case "/shows/10/alternatelists":
			_, _ = w.Write([]byte(`[{"id":5,"dvdRelease":true,"country":{"name":"United States","code":"US"}}]`))
		case "/alternatelists/5/alternateepisodes":
			_, _ = w.Write([]byte(`[{"id":6,"name":"Pilot","season":1,"number":1,"_embedded":{"episodes":[{"id":100,"name":"Pilot","season":1,"number":1}]}}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := New(server.URL, server.Client())
	episodes, err := c.EpisodesWithSpecials(context.Background(), 10)
	if err != nil || len(episodes) != 1 || episodes[0].Number != nil {
		t.Fatalf("episodes = %+v, %v", episodes, err)
	}
	lists, err := c.AlternateLists(context.Background(), 10)
	if err != nil || len(lists) != 1 || !lists[0].DVD {
		t.Fatalf("lists = %+v, %v", lists, err)
	}
	alternate, err := c.AlternateEpisodes(context.Background(), 5)
	if err != nil || len(alternate) != 1 || len(alternate[0].Embedded.Episodes) != 1 || alternate[0].Embedded.Episodes[0].ID != 100 {
		t.Fatalf("alternate = %+v, %v", alternate, err)
	}
	for _, path := range []string{"/shows/10/episodes?specials=1", "/shows/10/alternatelists", "/alternatelists/5/alternateepisodes?embed=episodes"} {
		if !seen[path] {
			t.Errorf("missing request %s; got %v", path, seen)
		}
	}
}

func TestResolverAbstainsForNotFoundAndEmpty(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = w.Write([]byte(`[]`))
				}
			}))
			defer server.Close()
			result := NewResolver(New(server.URL, server.Client())).Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindTV, Title: "Missing"})
			if result.Status != ensemble.ResolverAbstain {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestResolverReturnsOperationalErrorFor5xxAndInvalidJSON(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		code int
		kind ensemble.OperationalErrorKind
	}{{"provider", `{}`, 500, ensemble.ErrorProvider}, {"schema", `{`, 200, ensemble.ErrorInvalidResponse}} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			result := NewResolver(New(server.URL, server.Client())).Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindTV, Title: "Show"})
			if result.Status != ensemble.ResolverError || result.Error == nil || result.Error.Kind != test.kind || len(result.Candidates) != 0 {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestResolverContextCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	result := NewResolver(New(server.URL, server.Client())).Resolve(ctx, ensemble.ResolveRequest{Kind: model.KindAnime, Title: "Show"})
	if result.Status != ensemble.ResolverError || result.Error == nil || result.Error.Kind != ensemble.ErrorCanceled {
		t.Fatalf("result = %+v", result)
	}
}

func TestLookupAndShowMethods(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains([]string{"/lookup/shows", "/shows/9"}, r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"id":9,"name":"Show","externals":{"imdb":"tt1"}}`))
	}))
	defer server.Close()
	c := New(server.URL, server.Client())
	show, err := c.LookupShowByIMDb(context.Background(), "tt1")
	if err != nil || show.ID != 9 {
		t.Fatalf("lookup = %+v, %v", show, err)
	}
	show, err = c.Show(context.Background(), 9)
	if err != nil || show.ID != 9 {
		t.Fatalf("show = %+v, %v", show, err)
	}
	_, err = c.Show(context.Background(), 8)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found error = %v", err)
	}
}

func hasEvidence(candidate ensemble.Candidate, typ ensemble.EvidenceType) bool {
	return slices.ContainsFunc(candidate.Evidence, func(item ensemble.Evidence) bool { return item.Type == typ })
}
