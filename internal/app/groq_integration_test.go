package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/ai/providers/groq"
	"github.com/SleepingCat/PlexLink/internal/config"
	"github.com/SleepingCat/PlexLink/internal/ensemble"
	"github.com/SleepingCat/PlexLink/internal/model"
)

type groqTVMetadata struct{ ensembleMetadata }

func (groqTVMetadata) GetTV(_ context.Context, id int) (model.TVShow, error) {
	return model.TVShow{ID: id, Name: "Counterpart", FirstAirDate: "2017-12-10"}, nil
}

func TestGroqHypothesisEntersExistingSecondPassAndTMDBVerification(t *testing.T) {
	var providerRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerRequests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "groq/compound-mini", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": `{"original_title":"Sling Blade","year":1996,"kind":"movie","confidence":0.95}`}}}})
	}))
	defer server.Close()
	consultant, err := groq.New(groq.Config{BaseURL: server.URL, APIKey: "key", Model: "groq/compound-mini", MinConfidence: .9}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	catalog := &orchestrationResolver{name: "tmdb", resolve: func(req ensemble.ResolveRequest) ensemble.ResolverResult {
		for _, title := range req.TitleHypotheses {
			if title == "Sling Blade" || title == "Sling Blade 1996" {
				return evidenceResult("tmdb", 8973,
					ensemble.Evidence{Family: ensemble.FamilyTitle, Type: ensemble.EvidenceTitleExactCanonical, Source: "tmdb", Points: ensemble.PointsTitleExactCanonical},
					ensemble.Evidence{Family: ensemble.FamilyTime, Type: ensemble.EvidenceYearReleaseDateExact, Source: "tmdb", Points: ensemble.PointsYearReleaseDateExact})
			}
		}
		return ensemble.ResolverResult{Name: "tmdb", Status: ensemble.ResolverAbstain}
	}}
	p := Processor{Metadata: ensembleMetadata{}, Resolvers: []ensemble.Resolver{catalog}, AI: consultant, AIProvider: "groq", AIModel: "groq/compound-mini", Config: config.Config{AI: config.AI{Enabled: true, WebSearch: "require", MinConfidence: .9}, Resolvers: config.Resolvers{Timeout: "1s"}, State: config.State{Directory: t.TempDir()}}}
	result := Result{}
	match, _, err := p.resolveEnsemble(context.Background(), model.KindMovie, "Ottochennoe.Lezvie.1996.RUS.HDRip", []model.MediaFile{{Name: "Ottochennoe.Lezvie.1996.RUS.HDRip.avi"}}, model.Evidence{Titles: []model.WeightedTitle{{Title: "Ottochennoe Lezvie"}}, Year: 1996}, true, &result)
	if err != nil || match.ID != 8973 || match.Score != 500 || providerRequests.Load() != 1 || result.AI.ProviderRequests != 1 || !result.Ensemble.SecondPassUsed || !result.Ensemble.FinalTMDBVerified {
		t.Fatalf("match=%+v provider=%d diagnostics=%+v ensemble=%+v err=%v", match, providerRequests.Load(), result.AI, result.Ensemble, err)
	}
}

func TestGroqTVPackUsesOneIdentityRequestAndExistingTVSecondPass(t *testing.T) {
	var providerRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerRequests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "groq/compound-mini", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": `{"original_title":"Counterpart","year":2017,"kind":"tv","confidence":0.95}`}}}})
	}))
	defer server.Close()
	consultant, err := groq.New(groq.Config{BaseURL: server.URL, APIKey: "key", Model: "groq/compound-mini", MinConfidence: .9}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	catalog := &orchestrationResolver{name: "tmdb", resolve: func(req ensemble.ResolveRequest) ensemble.ResolverResult {
		if len(req.TitleHypotheses) == 0 {
			return ensemble.ResolverResult{Name: "tmdb", Status: ensemble.ResolverAbstain}
		}
		return ensemble.ResolverResult{Name: "tmdb", Status: ensemble.ResolverOK, Candidates: []ensemble.Candidate{{Identity: ensemble.EntityIdentity{Kind: model.KindTV, TMDBID: 42, Title: "Counterpart", Year: 2017}, Evidence: []ensemble.Evidence{
			{Family: ensemble.FamilyTitle, Type: ensemble.EvidenceTitleExactCanonical, Source: "tmdb", Points: ensemble.PointsTitleExactCanonical},
			{Family: ensemble.FamilyTime, Type: ensemble.EvidenceYearReleaseDateExact, Source: "tmdb", Points: ensemble.PointsYearReleaseDateExact},
		}}}}
	}}
	files := make([]model.MediaFile, 12)
	for i := range files {
		files[i] = model.MediaFile{Name: fmt.Sprintf("Counterpart.S02E%02d.mkv", i+1)}
	}
	p := Processor{Metadata: groqTVMetadata{}, Resolvers: []ensemble.Resolver{catalog}, AI: consultant, AIProvider: "groq", AIModel: "groq/compound-mini", Config: config.Config{AI: config.AI{Enabled: true, WebSearch: "require", MinConfidence: .9}, Resolvers: config.Resolvers{Timeout: "1s"}, State: config.State{Directory: t.TempDir()}}}
	match, _, err := p.resolveEnsemble(context.Background(), model.KindTV, "Counterpart.S02.LostFilm", files, model.Evidence{Titles: []model.WeightedTitle{{Title: "Counterpart"}}, Year: 2017}, true, &Result{})
	if err != nil || match.ID != 42 || providerRequests.Load() != 1 || catalog.calls.Load() != 2 {
		t.Fatalf("match=%+v provider=%d catalog=%d err=%v", match, providerRequests.Load(), catalog.calls.Load(), err)
	}
}

func TestStrongFirstPassDoesNotCallGroq(t *testing.T) {
	var providerRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerRequests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	consultant, err := groq.New(groq.Config{BaseURL: server.URL, APIKey: "key", Model: "groq/compound-mini", MinConfidence: .9}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	resolver := &orchestrationResolver{name: "tmdb", resolve: func(ensemble.ResolveRequest) ensemble.ResolverResult {
		return evidenceResult("tmdb", 8973,
			ensemble.Evidence{Family: ensemble.FamilyTitle, Type: ensemble.EvidenceTitleExactCanonical, Source: "tmdb", Points: ensemble.PointsTitleExactCanonical},
			ensemble.Evidence{Family: ensemble.FamilyTime, Type: ensemble.EvidenceYearReleaseDateExact, Source: "tmdb", Points: ensemble.PointsYearReleaseDateExact})
	}}
	p := Processor{Metadata: ensembleMetadata{}, Resolvers: []ensemble.Resolver{resolver}, AI: consultant, Config: config.Config{AI: config.AI{Enabled: true, WebSearch: "require", MinConfidence: .9}, Resolvers: config.Resolvers{Timeout: "1s"}}}
	result := Result{}
	match, _, err := p.resolveEnsemble(context.Background(), model.KindMovie, "Sling Blade 1996", nil, model.Evidence{Titles: []model.WeightedTitle{{Title: "Sling Blade"}}, Year: 1996}, true, &result)
	if err != nil || match.ID != 8973 || providerRequests.Load() != 0 || result.AI.Used {
		t.Fatalf("match=%+v requests=%d diagnostics=%+v err=%v", match, providerRequests.Load(), result.AI, err)
	}
}

func TestGroqHTTP400IsNonFatalAndExposesSanitizedDiagnostics(t *testing.T) {
	var providerRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerRequests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"unsupported field; token secret-key rejected"}}`))
	}))
	defer server.Close()
	consultant, err := groq.New(groq.Config{BaseURL: server.URL, APIKey: "secret-key", Model: "groq/compound-mini", MinConfidence: .9}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	resolver := &orchestrationResolver{name: "tmdb", resolve: func(ensemble.ResolveRequest) ensemble.ResolverResult {
		return ensemble.ResolverResult{Name: "tmdb", Status: ensemble.ResolverAbstain}
	}}
	p := Processor{Metadata: ensembleMetadata{}, Resolvers: []ensemble.Resolver{resolver}, AI: consultant, AIProvider: "groq", AIModel: "groq/compound-mini", Config: config.Config{AI: config.AI{Enabled: true, WebSearch: "require", MinConfidence: .9}, Resolvers: config.Resolvers{Timeout: "1s"}, State: config.State{Directory: t.TempDir()}}}
	result := Result{}
	_, _, err = p.resolveEnsemble(context.Background(), model.KindMovie, "Unknown.1996", nil, model.Evidence{Titles: []model.WeightedTitle{{Title: "Unknown"}}, Year: 1996}, true, &result)
	if !errors.Is(err, ErrUnresolved) || providerRequests.Load() != 1 || result.AI.ProviderRequests != 1 || result.AI.HTTPStatus != 400 || result.AI.ProviderErrorCode != "400" || result.AI.ProviderError != "unsupported field; token [REDACTED] rejected" || !strings.Contains(result.AI.ProviderResponse, `"code":400`) || strings.Contains(result.AI.ProviderResponse, "secret-key") || result.AI.ProviderRequest == "" || strings.Contains(result.AI.ProviderRequest, "secret-key") || result.Ensemble.SecondPassUsed {
		t.Fatalf("requests=%d diagnostics=%+v ensemble=%+v err=%v", providerRequests.Load(), result.AI, result.Ensemble, err)
	}
}
