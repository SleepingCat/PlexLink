package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/ai"
	"github.com/SleepingCat/PlexLink/internal/config"
	"github.com/SleepingCat/PlexLink/internal/ensemble"
	"github.com/SleepingCat/PlexLink/internal/model"
)

type ensembleMetadata struct{}

func (ensembleMetadata) SearchMovie(_ context.Context, query string) ([]model.MovieCandidate, error) {
	if strings.Contains(strings.ToLower(query), "sling blade") {
		return []model.MovieCandidate{{ID: 8973, Title: "Sling Blade", OriginalTitle: "Sling Blade", ReleaseDate: "1996-08-30"}}, nil
	}
	return nil, nil
}
func (ensembleMetadata) GetMovie(_ context.Context, id int) (model.Movie, error) {
	if id == 8973 {
		return model.Movie{ID: id, Title: "Sling Blade", OriginalTitle: "Sling Blade", ReleaseDate: "1996-08-30"}, nil
	}
	return model.Movie{}, errors.New("not found")
}
func (ensembleMetadata) GetMovieReleaseDates(context.Context, int) (model.MovieReleaseDates, error) {
	return model.MovieReleaseDates{}, nil
}
func (ensembleMetadata) SearchTV(context.Context, string) ([]model.TVCandidate, error) {
	return nil, nil
}
func (ensembleMetadata) GetTV(context.Context, int) (model.TVShow, error) {
	return model.TVShow{}, errors.New("not found")
}
func (ensembleMetadata) GetSeason(context.Context, int, int) (model.Season, error) {
	return model.Season{}, errors.New("not found")
}
func (ensembleMetadata) FindByIMDb(_ context.Context, id string) (model.ExternalFindResult, error) {
	if id == "tt0100000" {
		return model.ExternalFindResult{MovieResults: []model.MovieCandidate{{ID: 8973, Title: "Sling Blade", ReleaseDate: "1996-08-30"}}}, nil
	}
	return model.ExternalFindResult{}, nil
}

type orchestrationResolver struct {
	name    string
	calls   atomic.Int32
	resolve func(ensemble.ResolveRequest) ensemble.ResolverResult
}

func (r *orchestrationResolver) Name() string           { return r.name }
func (*orchestrationResolver) Supports(model.Kind) bool { return true }
func (r *orchestrationResolver) Resolve(_ context.Context, req ensemble.ResolveRequest) ensemble.ResolverResult {
	r.calls.Add(1)
	return r.resolve(req)
}

func evidenceResult(name string, id int, items ...ensemble.Evidence) ensemble.ResolverResult {
	return ensemble.ResolverResult{Name: name, Status: ensemble.ResolverOK, Candidates: []ensemble.Candidate{{Identity: ensemble.EntityIdentity{Kind: model.KindMovie, TMDBID: id, Title: "Sling Blade", Year: 1996}, Evidence: items}}}
}

func TestEnsembleWinnerBypassesAI(t *testing.T) {
	resolver := &orchestrationResolver{name: "tmdb", resolve: func(ensemble.ResolveRequest) ensemble.ResolverResult {
		return evidenceResult("tmdb", 8973,
			ensemble.Evidence{Family: ensemble.FamilyTitle, Type: ensemble.EvidenceTitleExactCanonical, Source: "tmdb", Points: ensemble.PointsTitleExactCanonical},
			ensemble.Evidence{Family: ensemble.FamilyTime, Type: ensemble.EvidenceYearReleaseDateExact, Source: "tmdb", Points: ensemble.PointsYearReleaseDateExact})
	}}
	aiResolver := &fakeAI{result: ai.Result{Status: ai.Resolved, MediaType: model.KindMovie, Confidence: .99}}
	p := Processor{Metadata: ensembleMetadata{}, Resolvers: []ensemble.Resolver{resolver}, AI: aiResolver, Config: config.Config{AI: config.AI{Enabled: true, MinConfidence: .9}, Resolvers: config.Resolvers{Timeout: "1s"}}}
	result := Result{}
	match, _, err := p.resolveEnsemble(context.Background(), model.KindMovie, "Sling Blade", nil, model.Evidence{Titles: []model.WeightedTitle{{Title: "Sling Blade"}}, Year: 1996}, true, &result)
	if err != nil || match.ID != 8973 || aiResolver.calls != 0 || !result.Ensemble.FinalTMDBVerified {
		t.Fatalf("match=%+v ai=%d result=%+v err=%v", match, aiResolver.calls, result, err)
	}
}

func TestIMDbBridgeIsScoredOnlyAfterTMDBNormalization(t *testing.T) {
	candidate := ensemble.Candidate{Identity: ensemble.EntityIdentity{Kind: model.KindMovie, IMDbID: "tt0100000", Title: "Sling Blade", Year: 1996}, Evidence: []ensemble.Evidence{{Family: ensemble.FamilyTitle, Type: ensemble.EvidenceTitleExactAKA, Source: "tvmaze", Points: ensemble.PointsTitleExactAKA}}}
	mapped, opErr := (tmdbNormalizer{metadata: ensembleMetadata{}}).Normalize(context.Background(), candidate)
	if opErr != nil || len(mapped) != 1 || mapped[0].Identity.TMDBID != 8973 {
		t.Fatalf("mapped=%+v err=%+v", mapped, opErr)
	}
	found := false
	for _, item := range mapped[0].Evidence {
		if item.Type == ensemble.EvidenceExternalIMDbMapsSameTMDB && item.Points == ensemble.PointsExternalIMDbMapsSameTMDB {
			found = true
		}
	}
	if !found {
		t.Fatalf("evidence=%+v", mapped[0].Evidence)
	}
}

func TestAIConsultantRunsOnceAndOneCatalogRequery(t *testing.T) {
	catalog := &orchestrationResolver{name: "tmdb", resolve: func(req ensemble.ResolveRequest) ensemble.ResolverResult {
		for _, hypothesis := range req.TitleHypotheses {
			if hypothesis == "Sling Blade" {
				return evidenceResult("tmdb", 8973,
					ensemble.Evidence{Family: ensemble.FamilyTitle, Type: ensemble.EvidenceTitleExactCanonical, Source: "tmdb", Points: ensemble.PointsTitleExactCanonical},
					ensemble.Evidence{Family: ensemble.FamilyTime, Type: ensemble.EvidenceYearReleaseDateExact, Source: "tmdb", Points: ensemble.PointsYearReleaseDateExact})
			}
		}
		return ensemble.ResolverResult{Name: "tmdb", Status: ensemble.ResolverAbstain}
	}}
	fingerprint := &orchestrationResolver{name: "opensubtitles", resolve: func(ensemble.ResolveRequest) ensemble.ResolverResult {
		return ensemble.ResolverResult{Name: "opensubtitles", Status: ensemble.ResolverAbstain}
	}}
	aiResolver := &fakeAI{result: ai.Result{Status: ai.Resolved, MediaType: model.KindMovie, CanonicalTitle: "Sling Blade", Year: 1996, Confidence: .99}}
	p := Processor{Metadata: ensembleMetadata{}, Resolvers: []ensemble.Resolver{catalog, fingerprint}, AI: aiResolver, AIProvider: "fake", AIModel: "fake", Config: config.Config{AI: config.AI{Enabled: true, MinConfidence: .9, WebSearch: "never"}, Resolvers: config.Resolvers{Timeout: "1s"}, State: config.State{Directory: t.TempDir()}}}
	result := Result{}
	match, _, err := p.resolveEnsemble(context.Background(), model.KindMovie, "Ottochennoe Lezvie", []model.MediaFile{{Name: "Ottochennoe.Lezvie.1996.mkv"}}, model.Evidence{Titles: []model.WeightedTitle{{Title: "Ottochennoe Lezvie"}}, Year: 1996}, true, &result)
	if err != nil || match.ID != 8973 || aiResolver.calls != 1 || catalog.calls.Load() != 2 || fingerprint.calls.Load() != 1 || !result.Ensemble.SecondPassUsed {
		t.Fatalf("match=%+v ai=%d catalog=%d fingerprint=%d result=%+v err=%v", match, aiResolver.calls, catalog.calls.Load(), fingerprint.calls.Load(), result.Ensemble, err)
	}
	if result.Ensemble.FinalDecision.Candidates[0].TotalScore != 500 {
		t.Fatalf("AI confidence leaked into score: %+v", result.Ensemble.FinalDecision.Candidates[0])
	}
}

func TestPostAIInsufficientEvidenceStaysUnresolved(t *testing.T) {
	resolver := &orchestrationResolver{name: "tmdb", resolve: func(ensemble.ResolveRequest) ensemble.ResolverResult {
		return ensemble.ResolverResult{Name: "tmdb", Status: ensemble.ResolverAbstain}
	}}
	aiResolver := &fakeAI{result: ai.Result{Status: ai.Resolved, MediaType: model.KindMovie, CanonicalTitle: "Guess", Confidence: .99}}
	p := Processor{Metadata: ensembleMetadata{}, Resolvers: []ensemble.Resolver{resolver}, AI: aiResolver, AIProvider: "fake", AIModel: "fake", Config: config.Config{AI: config.AI{Enabled: true, MinConfidence: .9}, Resolvers: config.Resolvers{Timeout: "1s"}, State: config.State{Directory: t.TempDir()}}}
	_, _, err := p.resolveEnsemble(context.Background(), model.KindMovie, "Unknown", nil, model.Evidence{Titles: []model.WeightedTitle{{Title: "Unknown"}}}, true, &Result{})
	if !errors.Is(err, ErrUnresolved) || aiResolver.calls != 1 || resolver.calls.Load() != 2 {
		t.Fatalf("err=%v ai=%d resolver=%d", err, aiResolver.calls, resolver.calls.Load())
	}
}

func TestAIOutageIsSafeUnresolved(t *testing.T) {
	resolver := &orchestrationResolver{name: "tmdb", resolve: func(ensemble.ResolveRequest) ensemble.ResolverResult {
		return ensemble.ResolverResult{Name: "tmdb", Status: ensemble.ResolverAbstain}
	}}
	aiResolver := &fakeAI{err: errors.New("provider raw failure")}
	p := Processor{Metadata: ensembleMetadata{}, Resolvers: []ensemble.Resolver{resolver}, AI: aiResolver, AIProvider: "fake", AIModel: "fake", Config: config.Config{AI: config.AI{Enabled: true, MinConfidence: .9}, Resolvers: config.Resolvers{Timeout: "1s"}, State: config.State{Directory: t.TempDir()}}}
	result := Result{}
	_, _, err := p.resolveEnsemble(context.Background(), model.KindMovie, "Unknown", nil, model.Evidence{Titles: []model.WeightedTitle{{Title: "Unknown"}}}, true, &result)
	if !errors.Is(err, ErrUnresolved) || result.AI.Error != "AI consultant unavailable" {
		t.Fatalf("err=%v diagnostics=%+v", err, result.AI)
	}
}

func TestVerifiedStateBypassesEnsembleAndAI(t *testing.T) {
	root := t.TempDir()
	source, target, stateDir := filepath.Join(root, "movies"), filepath.Join(root, "plex"), filepath.Join(root, "state")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "Sling.Blade.1996.mkv"
	if err := os.WriteFile(filepath.Join(source, name), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := &orchestrationResolver{name: "tmdb", resolve: func(ensemble.ResolveRequest) ensemble.ResolverResult {
		return evidenceResult("tmdb", 8973,
			ensemble.Evidence{Family: ensemble.FamilyTitle, Type: ensemble.EvidenceTitleExactCanonical, Source: "tmdb", Points: ensemble.PointsTitleExactCanonical},
			ensemble.Evidence{Family: ensemble.FamilyTime, Type: ensemble.EvidenceYearReleaseDateExact, Source: "tmdb", Points: ensemble.PointsYearReleaseDateExact})
	}}
	aiResolver := &fakeAI{result: ai.Result{Status: ai.Unknown, MediaType: model.KindMovie}}
	cfg := config.Config{Paths: config.Paths{MovieSource: source, MovieTarget: target, TVSource: filepath.Join(root, "tv"), TVTarget: filepath.Join(root, "ptv"), AnimeSource: filepath.Join(root, "anime"), AnimeTarget: filepath.Join(root, "panime")}, State: config.State{Directory: stateDir}, Resolvers: config.Resolvers{Timeout: "1s"}, AI: config.AI{Enabled: true, MinConfidence: .9}}
	torrentClient := torrents{t: model.Torrent{Name: "Sling Blade 1996", ContentPath: filepath.Join(source, name), SavePath: source, Progress: 1}, f: []model.TorrentFile{{Name: name, Priority: 1, Progress: 1}}}
	p := Processor{Torrents: torrentClient, Metadata: ensembleMetadata{}, Resolvers: []ensemble.Resolver{resolver}, AI: aiResolver, Config: cfg}
	if _, err := p.Process(context.Background(), "cache-hash", false, 0); err != nil {
		t.Fatal(err)
	}
	firstCalls := resolver.calls.Load()
	second, err := p.Process(context.Background(), "cache-hash", false, 0)
	if err != nil || !second.Ensemble.CachedResolution || resolver.calls.Load() != firstCalls || aiResolver.calls != 0 {
		t.Fatalf("cached=%v resolver=%d/%d ai=%d err=%v", second.Ensemble.CachedResolution, resolver.calls.Load(), firstCalls, aiResolver.calls, err)
	}
}
