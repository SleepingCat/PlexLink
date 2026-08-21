package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/config"
	"github.com/SleepingCat/PlexLink/internal/ensemble"
	"github.com/SleepingCat/PlexLink/internal/linker"
	"github.com/SleepingCat/PlexLink/internal/model"
)

type freshEpisodeMetadata struct{ episodeCount int }

func (m freshEpisodeMetadata) SearchTV(context.Context, string) ([]model.TVCandidate, error) {
	return []model.TVCandidate{{ID: 7, Name: "Fresh Show", FirstAirDate: "2024-01-01"}}, nil
}
func (m freshEpisodeMetadata) GetTV(context.Context, int) (model.TVShow, error) {
	return model.TVShow{ID: 7, Name: "Fresh Show", FirstAirDate: "2024-01-01"}, nil
}
func (m freshEpisodeMetadata) GetSeason(_ context.Context, _ int, season int) (model.Season, error) {
	if season != 2 {
		return model.Season{}, errors.New("not found")
	}
	episodes := make([]model.Episode, m.episodeCount)
	for i := range episodes {
		episodes[i].EpisodeNumber = i + 1
	}
	return model.Season{SeasonNumber: 2, Episodes: episodes}, nil
}
func (freshEpisodeMetadata) SearchMovie(context.Context, string) ([]model.MovieCandidate, error) {
	return nil, nil
}
func (freshEpisodeMetadata) GetMovie(context.Context, int) (model.Movie, error) {
	return model.Movie{}, nil
}
func (freshEpisodeMetadata) GetMovieReleaseDates(context.Context, int) (model.MovieReleaseDates, error) {
	return model.MovieReleaseDates{}, nil
}

func freshPackProcessor(t *testing.T, count int, extra string) (Processor, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	files := make([]model.TorrentFile, 0, count+1)
	for i := 1; i <= count; i++ {
		name := fmt.Sprintf("Fresh.Show.S02E%02d.mkv", i)
		if err := os.WriteFile(filepath.Join(source, name), []byte(fmt.Sprintf("episode-%d", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, model.TorrentFile{Name: name, Priority: 1, Progress: 1})
	}
	if extra != "" {
		if err := os.WriteFile(filepath.Join(source, extra), []byte("extra"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, model.TorrentFile{Name: extra, Priority: 1, Progress: 1})
	}
	cfg := config.Config{Paths: config.Paths{TVSource: source, TVTarget: filepath.Join(root, "target"), MovieSource: filepath.Join(root, "movies"), MovieTarget: filepath.Join(root, "pmovies"), AnimeSource: filepath.Join(root, "anime"), AnimeTarget: filepath.Join(root, "panime")}, State: config.State{Directory: filepath.Join(root, "state")}}
	p := Processor{Torrents: torrents{t: model.Torrent{Name: "Fresh Show S02", ContentPath: source, SavePath: source, Progress: 1}, f: files}, Metadata: freshEpisodeMetadata{episodeCount: 11}, Config: cfg}
	return p, root
}

func TestFreshEpisodeBecomesProvisionalAndAllFilesArePlanned(t *testing.T) {
	p, root := freshPackProcessor(t, 12, "")
	result, err := p.Process(context.Background(), "fresh", true, 7)
	if err != nil || len(result.Plan) != 12 || result.MappingStatus != model.MappingResolvedWithWarnings {
		t.Fatalf("plan=%d status=%s err=%v mappings=%+v", len(result.Plan), result.MappingStatus, err, result.EpisodeValidation)
	}
	last := result.EpisodeValidation[11]
	if last.State != model.EpisodeProvisional || last.ContextScore != ensemble.FamilyCap(ensemble.FamilyContext) || last.Season != 2 || last.Episode != 12 {
		t.Fatalf("last=%+v", last)
	}
	if _, err := os.Stat(filepath.Join(root, "target")); !os.IsNotExist(err) {
		t.Fatal("dry-run mutated target filesystem")
	}
}

func TestUnknownFileDoesNotBlockResolvedSiblings(t *testing.T) {
	p, _ := freshPackProcessor(t, 11, "Fresh.Show.Bonus.mkv")
	result, err := p.Process(context.Background(), "partial", true, 7)
	if err != nil || len(result.Plan) != 11 || result.MappingStatus != model.MappingPartial {
		t.Fatalf("plan=%d status=%s err=%v mappings=%+v", len(result.Plan), result.MappingStatus, err, result.EpisodeValidation)
	}
	if result.EpisodeValidation[11].State != model.EpisodeUnresolved {
		t.Fatalf("mapping=%+v", result.EpisodeValidation[11])
	}
}

func TestIgnoredSampleDoesNotBlockPack(t *testing.T) {
	p, _ := freshPackProcessor(t, 11, "Fresh.Show.Sample.mkv")
	result, err := p.Process(context.Background(), "ignored", true, 7)
	if err != nil || len(result.Plan) != 11 || result.MappingStatus != model.MappingResolved {
		t.Fatalf("plan=%d status=%s err=%v", len(result.Plan), result.MappingStatus, err)
	}
	if result.EpisodeValidation[11].State != model.EpisodeIgnored {
		t.Fatalf("mapping=%+v", result.EpisodeValidation[11])
	}
}

type conflictingFingerprint struct{}

func (conflictingFingerprint) Name() string             { return "opensubtitles" }
func (conflictingFingerprint) Supports(model.Kind) bool { return true }
func (conflictingFingerprint) Resolve(context.Context, ensemble.ResolveRequest) ensemble.ResolverResult {
	return ensemble.ResolverResult{Name: "opensubtitles", Status: ensemble.ResolverOK, Candidates: []ensemble.Candidate{{Identity: ensemble.EntityIdentity{Kind: model.KindTV, TMDBID: 99}, Evidence: []ensemble.Evidence{{Family: ensemble.FamilyFileIdentity, Type: ensemble.EvidenceOpenSubtitlesHashExact, Source: "opensubtitles", Points: ensemble.PointsOpenSubtitlesHashExact}}}}}
}

func TestFingerprintConflictBlocksOnlyProvisionalFile(t *testing.T) {
	p, _ := freshPackProcessor(t, 12, "")
	p.Resolvers = []ensemble.Resolver{conflictingFingerprint{}}
	result, err := p.Process(context.Background(), "conflict-file", true, 7)
	if err != nil || len(result.Plan) != 11 || result.MappingStatus != model.MappingPartial {
		t.Fatalf("plan=%d status=%s err=%v", len(result.Plan), result.MappingStatus, err)
	}
	last := result.EpisodeValidation[11]
	if last.State != model.EpisodeUnresolved || last.Reason != "file identity conflicts with the accepted show" {
		t.Fatalf("last=%+v", last)
	}
}

func TestTargetCollisionDoesNotBlockOtherSafeTargets(t *testing.T) {
	p, _ := freshPackProcessor(t, 2, "")
	p.Metadata = freshEpisodeMetadata{episodeCount: 2}
	dry, err := p.Process(context.Background(), "collision", true, 7)
	if err != nil || len(dry.Plan) != 2 {
		t.Fatalf("dry=%+v err=%v", dry, err)
	}
	if err := os.MkdirAll(filepath.Dir(dry.Plan[0].Target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dry.Plan[0].Target, []byte("different source"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := p.Process(context.Background(), "collision", false, 7)
	if !errors.Is(err, ErrConflict) || result.MappingStatus != model.MappingConflict {
		t.Fatalf("status=%s err=%v", result.MappingStatus, err)
	}
	if _, statErr := os.Stat(dry.Plan[1].Target); statErr != nil {
		t.Fatalf("safe sibling was not linked: %v", statErr)
	}
	if content, readErr := os.ReadFile(dry.Plan[0].Target); readErr != nil || string(content) != "different source" {
		t.Fatal("conflicting target was overwritten")
	}
}

func TestRepeatedProvisionalProcessingIsIdempotent(t *testing.T) {
	p, _ := freshPackProcessor(t, 12, "")
	first, err := p.Process(context.Background(), "repeat", false, 7)
	if err != nil || len(first.Actions) != 12 {
		t.Fatalf("first actions=%v err=%v", first.Actions, err)
	}
	second, err := p.Process(context.Background(), "repeat", false, 0)
	if err != nil || len(second.Actions) != 12 {
		t.Fatalf("second actions=%v err=%v", second.Actions, err)
	}
	for _, action := range second.Actions {
		if action != linker.Noop {
			t.Fatalf("action=%s, want NOOP", action)
		}
	}
}

func TestInspectDiagnosticsAreDeterministicAndExplainDecisionAndMappings(t *testing.T) {
	p, _ := freshPackProcessor(t, 12, "")
	result, err := p.Process(context.Background(), "diagnostics", true, 7)
	if err != nil {
		t.Fatal(err)
	}
	result.Ensemble = EnsembleDiagnostics{
		Used: true,
		ResolverResults: []ensemble.ResolverResult{
			{
				Name:   "tmdb",
				Status: ensemble.ResolverOK,
				Candidates: []ensemble.Candidate{
					{
						Identity: ensemble.EntityIdentity{Kind: model.KindTV, TMDBID: 7, Title: "Fresh Show", Year: 2024},
						Evidence: []ensemble.Evidence{
							{Family: ensemble.FamilyTitle, Type: ensemble.EvidenceTitleExactCanonical, Source: "tmdb", Points: ensemble.PointsTitleExactCanonical},
							{Family: ensemble.FamilyTime, Type: ensemble.EvidenceYearReleaseDateExact, Source: "tmdb", Points: ensemble.PointsYearReleaseDateExact},
						},
					},
				},
			},
		},
	}
	decision := ensemble.Aggregate(result.Ensemble.ResolverResults)
	result.Ensemble.FirstPass, result.Ensemble.FinalDecision = &decision, &decision
	result.Ensemble.FinalTMDBVerified = true

	first, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("inspect JSON is not deterministic")
	}
	for _, field := range []string{"resolver_results", "candidates", "evidence", "family_subtotals", "total_score", "margin", "hard_conflicts", "episode_validation", "PROVISIONAL", "RESOLVED_WITH_WARNINGS"} {
		if !strings.Contains(string(first), field) {
			t.Fatalf("inspect JSON does not expose %q: %s", field, first)
		}
	}
}
