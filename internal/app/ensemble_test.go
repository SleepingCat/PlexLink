package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/ai"
	"github.com/SleepingCat/PlexLink/internal/config"
	"github.com/SleepingCat/PlexLink/internal/ensemble"
	"github.com/SleepingCat/PlexLink/internal/model"
	"github.com/SleepingCat/PlexLink/internal/state"
)

type ensembleMetadata struct{}

type aiGateMetadata struct {
	title         string
	originalTitle string
	year          int
}

func (aiGateMetadata) SearchMovie(context.Context, string) ([]model.MovieCandidate, error) {
	return nil, nil
}
func (m aiGateMetadata) GetMovie(_ context.Context, id int) (model.Movie, error) {
	title, releaseYear := m.title, m.year
	if title == "" {
		title = "Death Proof"
	}
	if releaseYear == 0 {
		releaseYear = 2007
	}
	originalTitle := m.originalTitle
	if originalTitle == "" {
		originalTitle = title
	}
	return model.Movie{ID: id, Title: title, OriginalTitle: originalTitle, ReleaseDate: fmt.Sprintf("%04d-05-22", releaseYear)}, nil
}
func (aiGateMetadata) GetMovieReleaseDates(context.Context, int) (model.MovieReleaseDates, error) {
	return model.MovieReleaseDates{}, nil
}
func (aiGateMetadata) SearchTV(context.Context, string) ([]model.TVCandidate, error) { return nil, nil }
func (aiGateMetadata) GetTV(context.Context, int) (model.TVShow, error) {
	return model.TVShow{}, errors.New("not found")
}
func (aiGateMetadata) GetSeason(context.Context, int, int) (model.Season, error) {
	return model.Season{}, errors.New("not found")
}

type finalFailMetadata struct {
	aiGateMetadata
	calls atomic.Int32
}

func (m *finalFailMetadata) GetMovie(ctx context.Context, id int) (model.Movie, error) {
	if m.calls.Add(1) > 1 {
		return model.Movie{}, errors.New("TMDB unavailable")
	}
	return m.aiGateMetadata.GetMovie(ctx, id)
}

func (ensembleMetadata) SearchMovie(_ context.Context, query string) ([]model.MovieCandidate, error) {
	if strings.Contains(strings.ToLower(query), "sling blade") {
		return []model.MovieCandidate{{ID: 8973, Title: "Sling Blade", OriginalTitle: "Sling Blade", ReleaseDate: "1996-08-30"}}, nil
	}
	return nil, nil
}

type unavailableMetadata struct{ ensembleMetadata }

func (unavailableMetadata) GetMovie(context.Context, int) (model.Movie, error) {
	return model.Movie{}, errors.New("tmdb unavailable")
}

func (unavailableMetadata) GetTV(context.Context, int) (model.TVShow, error) {
	return model.TVShow{}, errors.New("tmdb unavailable")
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

func TestIMDbBridgeNormalizesWithoutAddingMatchEvidence(t *testing.T) {
	candidate := ensemble.Candidate{Identity: ensemble.EntityIdentity{Kind: model.KindMovie, IMDbID: "tt0100000", Title: "Sling Blade", Year: 1996}, Evidence: []ensemble.Evidence{{Family: ensemble.FamilyTitle, Type: ensemble.EvidenceTitleExactAKA, Source: "tvmaze", Points: ensemble.PointsTitleExactAKA}}}
	mapped, opErr := (tmdbNormalizer{metadata: ensembleMetadata{}}).Normalize(context.Background(), candidate)
	if opErr != nil || len(mapped) != 1 || mapped[0].Identity.TMDBID != 8973 {
		t.Fatalf("mapped=%+v err=%+v", mapped, opErr)
	}
	for _, item := range mapped[0].Evidence {
		if item.Family == ensemble.FamilyExternalIdentity {
			t.Fatalf("normalization bridge became match evidence: %+v", mapped[0].Evidence)
		}
	}
	decision := ensemble.Aggregate([]ensemble.ResolverResult{{Name: "tvmaze", Status: ensemble.ResolverOK, Candidates: mapped}})
	if got := decision.Candidates[0]; got.FamilyScores[ensemble.FamilyExternalIdentity] != 0 || got.FamilyCount != 1 || got.IdentityAnchors != 0 {
		t.Fatalf("candidate=%+v", got)
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
	if result.Ensemble.FirstPass == nil || result.Ensemble.FirstPass.Type != ensemble.DecisionNoEvidence {
		t.Fatalf("first pass=%+v", result.Ensemble.FirstPass)
	}
	if len(aiResolver.requests) != 1 || aiResolver.requests[0].Task != ai.IdentifyMedia || len(aiResolver.requests[0].Candidates) != 0 {
		t.Fatalf("AI request=%+v", aiResolver.requests)
	}
	if !containsString(aiResolver.requests[0].Parsed.Titles, "Отточенное лезвие") {
		t.Fatalf("reverse-transliteration hypothesis was not sent to AI: %+v", aiResolver.requests[0].Parsed.Titles)
	}
}

func TestEnsembleRequestAddsReverseTransliterationWithoutReplacingOriginal(t *testing.T) {
	req := ensembleRequest(model.KindMovie, "Ottochennoe.Lezvie.1996", nil, model.Evidence{Titles: []model.WeightedTitle{{Title: "Ottochennoe Lezvie", Weight: 3}}, Year: 1996}, nil)
	if req.Title != "Ottochennoe Lezvie" || !containsString(req.TitleHypotheses, "Отточенное лезвие") {
		t.Fatalf("request=%+v", req)
	}
}

func TestEnsembleRequeryPrioritizesNewAIHypotheses(t *testing.T) {
	req := ensembleRequest(model.KindMovie, "Ottochennoe.Lezvie.1996", nil, model.Evidence{Titles: []model.WeightedTitle{{Title: "Ottochennoe Lezvie", Weight: 3}}, Year: 1996}, []string{"Sling Blade 1996", "Sling Blade"})
	if len(req.TitleHypotheses) < 3 || req.TitleHypotheses[0] != "Sling Blade 1996" || req.TitleHypotheses[1] != "Sling Blade" || req.TitleHypotheses[2] != "Отточенное лезвие" {
		t.Fatalf("hypotheses=%q", req.TitleHypotheses)
	}
}

func TestAIAssistedGateAcceptsVerifiedCandidateWithoutInflatingScore(t *testing.T) {
	webUsed := true
	catalog := aiGateCatalog("Доказательство смерти", 2007)
	aiResolver := &fakeAI{result: ai.Result{Status: ai.Resolved, MediaType: model.KindMovie, CanonicalTitle: "Death Proof", Year: 2007, Confidence: .95, WebSearchUsed: &webUsed}}
	p := Processor{Metadata: aiGateMetadata{title: "Доказательство смерти", originalTitle: "Death Proof", year: 2007}, Resolvers: []ensemble.Resolver{catalog}, AI: aiResolver, AIProvider: "groq", AIModel: "groq/compound-mini", Config: config.Config{AI: config.AI{Enabled: true, MinConfidence: .9, WebSearch: "require"}, Resolvers: config.Resolvers{Timeout: "1s"}, State: config.State{Directory: t.TempDir()}}}
	result := Result{}
	match, _, err := p.resolveEnsemble(context.Background(), model.KindMovie, "Dokazatelstvo_smerti", nil, model.Evidence{Titles: []model.WeightedTitle{{Title: "Dokazatelstvo smerti"}}}, true, &result)
	if err != nil || match.ID != 1991 || match.Score != ensemble.PointsTitleExactCanonical || result.Ensemble.FinalDecision == nil || result.Ensemble.FinalDecision.Type != ensemble.DecisionAIAssisted || !result.Ensemble.AIAssistedGate.Accepted || !result.Ensemble.FinalTMDBVerified {
		t.Fatalf("match=%+v diagnostics=%+v err=%v", match, result.Ensemble, err)
	}
	if result.Ensemble.DeterministicFinal == nil || result.Ensemble.DeterministicFinal.Type != ensemble.DecisionAmbiguous || result.Ensemble.DeterministicFinal.Candidates[0].TotalScore != ensemble.PointsTitleExactCanonical {
		t.Fatalf("deterministic score/decision was altered: %+v", result.Ensemble.DeterministicFinal)
	}
}

func TestAIAssistedGateRejectsTMDBDisagreementAndSourceYearConflict(t *testing.T) {
	webUsed := true
	tests := []struct {
		name       string
		catalog    *orchestrationResolver
		sourceYear int
	}{
		{name: "TMDB title disagrees", catalog: aiGateCatalog("Different Movie", 2007)},
		{name: "source year conflicts", catalog: aiGateCatalog("Death Proof", 2005), sourceYear: 1998},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aiResolver := &fakeAI{result: ai.Result{Status: ai.Resolved, MediaType: model.KindMovie, CanonicalTitle: "Death Proof", Year: 2005, Confidence: .98, WebSearchUsed: &webUsed}}
			if tt.name == "TMDB title disagrees" {
				aiResolver.result.Year = 2007
			}
			metadata := aiGateMetadata{}
			if tt.name == "TMDB title disagrees" {
				metadata = aiGateMetadata{title: "Другой фильм", originalTitle: "Different Movie", year: 2007}
			} else {
				metadata = aiGateMetadata{title: "Death Proof", year: 2005}
			}
			p := Processor{Metadata: metadata, Resolvers: []ensemble.Resolver{tt.catalog}, AI: aiResolver, AIProvider: "groq", AIModel: "groq/compound-mini", Config: config.Config{AI: config.AI{Enabled: true, MinConfidence: .9, WebSearch: "require"}, Resolvers: config.Resolvers{Timeout: "1s"}, State: config.State{Directory: t.TempDir()}}}
			result := Result{}
			_, _, err := p.resolveEnsemble(context.Background(), model.KindMovie, "source", nil, model.Evidence{Titles: []model.WeightedTitle{{Title: "source"}}, Year: tt.sourceYear}, true, &result)
			if !errors.Is(err, ErrUnresolved) || result.Ensemble.AIAssistedGate.Accepted {
				t.Fatalf("diagnostics=%+v err=%v", result.Ensemble, err)
			}
		})
	}
}

func TestAIAssistedGateRequiresWebBackedHypothesisAndFinalTMDBVerification(t *testing.T) {
	webNotUsed := false
	failingMetadata := &finalFailMetadata{}
	for _, tt := range []struct {
		name      string
		web       *bool
		metadata  MetadataProvider
		wantError error
	}{
		{name: "not web backed", web: &webNotUsed, metadata: aiGateMetadata{}, wantError: ErrUnresolved},
		{name: "final TMDB failure", web: func() *bool { value := true; return &value }(), metadata: failingMetadata, wantError: ErrMetadata},
	} {
		t.Run(tt.name, func(t *testing.T) {
			catalog := aiGateCatalog("Death Proof", 2007)
			aiResolver := &fakeAI{result: ai.Result{Status: ai.Resolved, MediaType: model.KindMovie, CanonicalTitle: "Death Proof", Year: 2007, Confidence: .99, WebSearchUsed: tt.web}}
			p := Processor{Metadata: tt.metadata, Resolvers: []ensemble.Resolver{catalog}, AI: aiResolver, AIProvider: "groq", AIModel: "groq/compound-mini", Config: config.Config{AI: config.AI{Enabled: true, MinConfidence: .9, WebSearch: "require"}, Resolvers: config.Resolvers{Timeout: "1s"}, State: config.State{Directory: t.TempDir()}}}
			result := Result{}
			_, _, err := p.resolveEnsemble(context.Background(), model.KindMovie, "source", nil, model.Evidence{}, true, &result)
			if !errors.Is(err, tt.wantError) || result.Ensemble.AIAssistedGate.Accepted || result.Ensemble.FinalTMDBVerified {
				t.Fatalf("diagnostics=%+v err=%v", result.Ensemble, err)
			}
		})
	}
}

func TestAIAssistedGateRejectsLowConfidenceBeforeSecondPass(t *testing.T) {
	webUsed := true
	catalog := aiGateCatalog("Death Proof", 2007)
	aiResolver := &fakeAI{result: ai.Result{Status: ai.Resolved, MediaType: model.KindMovie, CanonicalTitle: "Death Proof", Year: 2007, Confidence: .89, WebSearchUsed: &webUsed}}
	p := Processor{Metadata: aiGateMetadata{}, Resolvers: []ensemble.Resolver{catalog}, AI: aiResolver, AIProvider: "groq", AIModel: "groq/compound-mini", Config: config.Config{AI: config.AI{Enabled: true, MinConfidence: .9, WebSearch: "require"}, Resolvers: config.Resolvers{Timeout: "1s"}, State: config.State{Directory: t.TempDir()}}}
	result := Result{}
	_, _, err := p.resolveEnsemble(context.Background(), model.KindMovie, "source", nil, model.Evidence{}, true, &result)
	if !errors.Is(err, ErrUnresolved) || result.Ensemble.SecondPassUsed || result.Ensemble.AIAssistedGate.Evaluated || catalog.calls.Load() != 1 {
		t.Fatalf("catalog=%d diagnostics=%+v err=%v", catalog.calls.Load(), result.Ensemble, err)
	}
}

func aiGateCatalog(title string, year int) *orchestrationResolver {
	return &orchestrationResolver{name: "tmdb", resolve: func(req ensemble.ResolveRequest) ensemble.ResolverResult {
		if len(req.TitleHypotheses) == 0 {
			return ensemble.ResolverResult{Name: "tmdb", Status: ensemble.ResolverAbstain}
		}
		return ensemble.ResolverResult{Name: "tmdb", Status: ensemble.ResolverOK, Candidates: []ensemble.Candidate{{Identity: ensemble.EntityIdentity{Kind: model.KindMovie, TMDBID: 1991, Title: title, Year: year}, Evidence: []ensemble.Evidence{{Family: ensemble.FamilyTitle, Type: ensemble.EvidenceTitleExactCanonical, Source: "tmdb", Points: ensemble.PointsTitleExactCanonical}}}}}
	}}
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

func TestTMDBOutageRequiresCompatibleVerifiedStateForCanonicalIdentity(t *testing.T) {
	resolver := &orchestrationResolver{name: "tmdb", resolve: func(ensemble.ResolveRequest) ensemble.ResolverResult {
		return evidenceResult("tmdb", 8973,
			ensemble.Evidence{Family: ensemble.FamilyTitle, Type: ensemble.EvidenceTitleExactCanonical, Source: "tmdb", Points: ensemble.PointsTitleExactCanonical},
			ensemble.Evidence{Family: ensemble.FamilyTime, Type: ensemble.EvidenceYearReleaseDateExact, Source: "tmdb", Points: ensemble.PointsYearReleaseDateExact})
	}}
	p := Processor{Metadata: unavailableMetadata{}, Resolvers: []ensemble.Resolver{resolver}, Config: config.Config{Resolvers: config.Resolvers{Timeout: "1s"}}}
	_, _, err := p.resolveEnsemble(context.Background(), model.KindMovie, "Sling Blade", nil, model.Evidence{Titles: []model.WeightedTitle{{Title: "Sling Blade"}}, Year: 1996}, false, &Result{})
	if !errors.Is(err, ErrUnresolved) {
		t.Fatalf("fresh unverified identity err=%v", err)
	}

	match, _, err := p.resolveCached(context.Background(), model.KindMovie, state.VerifiedResolution{TMDBID: 8973, Kind: "movie", Title: "Sling Blade", Year: 1996})
	if err != nil || match.ID != 8973 || match.Name != "Sling Blade" {
		t.Fatalf("cached match=%+v err=%v", match, err)
	}
}
