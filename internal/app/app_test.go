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

	"github.com/SleepingCat/PlexLink/internal/ai"
	"github.com/SleepingCat/PlexLink/internal/config"
	"github.com/SleepingCat/PlexLink/internal/linker"
	"github.com/SleepingCat/PlexLink/internal/model"
	"github.com/SleepingCat/PlexLink/internal/release"
)

type torrents struct {
	t model.Torrent
	f []model.TorrentFile
}

type counterpartMetadata struct {
	queries    []string
	firstError bool
}

type bojackMetadata struct{}

type movieMetadata struct {
	candidates       []model.MovieCandidate
	dates            map[int]model.MovieReleaseDates
	releaseDateCalls []int
}

type fakeAI struct {
	calls    int
	requests []ai.Request
	result   ai.Result
	err      error
}

func (f *fakeAI) Resolve(_ context.Context, req ai.Request) (ai.Result, error) {
	f.calls++
	f.requests = append(f.requests, req)
	return f.result, f.err
}
func (*fakeAI) Capabilities() ai.Capabilities {
	return ai.Capabilities{StructuredOutput: true, WebSearch: true}
}

type ottoMetadata struct{}

func (ottoMetadata) SearchMovie(_ context.Context, query string) ([]model.MovieCandidate, error) {
	if strings.Contains(query, "Sling Blade") {
		return []model.MovieCandidate{{ID: 8973, Title: "Sling Blade", ReleaseDate: "1996-08-30"}}, nil
	}
	return nil, nil
}
func (ottoMetadata) GetMovie(context.Context, int) (model.Movie, error) {
	return model.Movie{ID: 8973, Title: "Sling Blade", ReleaseDate: "1996-08-30"}, nil
}
func (ottoMetadata) GetMovieReleaseDates(context.Context, int) (model.MovieReleaseDates, error) {
	return model.MovieReleaseDates{}, nil
}
func (ottoMetadata) SearchTV(context.Context, string) ([]model.TVCandidate, error) { return nil, nil }
func (ottoMetadata) GetTV(context.Context, int) (model.TVShow, error)              { return model.TVShow{}, nil }
func (ottoMetadata) GetSeason(context.Context, int, int) (model.Season, error) {
	return model.Season{}, nil
}

func (m *movieMetadata) SearchMovie(context.Context, string) ([]model.MovieCandidate, error) {
	return m.candidates, nil
}
func (m *movieMetadata) GetMovie(context.Context, int) (model.Movie, error) {
	return model.Movie{}, nil
}
func (m *movieMetadata) GetMovieReleaseDates(_ context.Context, id int) (model.MovieReleaseDates, error) {
	m.releaseDateCalls = append(m.releaseDateCalls, id)
	return m.dates[id], nil
}
func (m *movieMetadata) SearchTV(context.Context, string) ([]model.TVCandidate, error) {
	return nil, nil
}
func (m *movieMetadata) GetTV(context.Context, int) (model.TVShow, error) { return model.TVShow{}, nil }
func (m *movieMetadata) GetSeason(context.Context, int, int) (model.Season, error) {
	return model.Season{}, nil
}

func (bojackMetadata) SearchTV(context.Context, string) ([]model.TVCandidate, error) {
	return []model.TVCandidate{{ID: 61222, Name: "BoJack Horseman", FirstAirDate: "2014-08-22"}}, nil
}
func (bojackMetadata) GetTV(context.Context, int) (model.TVShow, error) {
	return model.TVShow{ID: 61222, Name: "BoJack Horseman", FirstAirDate: "2014-08-22"}, nil
}
func (bojackMetadata) GetSeason(_ context.Context, _ int, season int) (model.Season, error) {
	if season == 0 {
		return model.Season{SeasonNumber: 0, Episodes: []model.Episode{{EpisodeNumber: 1, Name: "Sabrina's Christmas Wish"}, {EpisodeNumber: 2, Name: "BoJack Horseman Christmas Special"}}}, nil
	}
	episodes := make([]model.Episode, 12)
	for i := range episodes {
		episodes[i].EpisodeNumber = i + 1
	}
	return model.Season{SeasonNumber: season, Episodes: episodes}, nil
}
func (bojackMetadata) SearchMovie(context.Context, string) ([]model.MovieCandidate, error) {
	return nil, nil
}
func (bojackMetadata) GetMovie(context.Context, int) (model.Movie, error) { return model.Movie{}, nil }
func (bojackMetadata) GetMovieReleaseDates(context.Context, int) (model.MovieReleaseDates, error) {
	return model.MovieReleaseDates{}, nil
}

func (m *counterpartMetadata) SearchTV(_ context.Context, query string) ([]model.TVCandidate, error) {
	m.queries = append(m.queries, query)
	if len(m.queries) == 1 {
		if m.firstError {
			return nil, errors.New("temporary search failure")
		}
		return nil, nil
	}
	if query == "Counterpart" {
		return []model.TVCandidate{{ID: 63646, Name: "Counterpart", FirstAirDate: "2017-12-10"}, {ID: 63646, Name: "Counterpart", FirstAirDate: "2017-12-10"}}, nil
	}
	return nil, nil
}
func (*counterpartMetadata) GetTV(context.Context, int) (model.TVShow, error) {
	return model.TVShow{ID: 63646, Name: "Counterpart", FirstAirDate: "2017-12-10"}, nil
}
func (*counterpartMetadata) GetSeason(_ context.Context, _ int, season int) (model.Season, error) {
	episodes := make([]model.Episode, 10)
	for i := range episodes {
		episodes[i].EpisodeNumber = i + 1
	}
	return model.Season{SeasonNumber: season, Episodes: episodes}, nil
}
func (*counterpartMetadata) SearchMovie(context.Context, string) ([]model.MovieCandidate, error) {
	return nil, nil
}
func (*counterpartMetadata) GetMovie(context.Context, int) (model.Movie, error) {
	return model.Movie{}, nil
}
func (*counterpartMetadata) GetMovieReleaseDates(context.Context, int) (model.MovieReleaseDates, error) {
	return model.MovieReleaseDates{}, nil
}

func (x torrents) GetTorrent(context.Context, string) (model.Torrent, error)     { return x.t, nil }
func (x torrents) GetFiles(context.Context, string) ([]model.TorrentFile, error) { return x.f, nil }

type metadata struct{}

func (metadata) SearchTV(context.Context, string) ([]model.TVCandidate, error) {
	return []model.TVCandidate{{ID: 7, Name: "Show", FirstAirDate: "2020-01-01"}}, nil
}
func (metadata) GetTV(context.Context, int) (model.TVShow, error) {
	return model.TVShow{ID: 7, Name: "Show", FirstAirDate: "2020-01-01", Seasons: []model.SeasonSummary{{SeasonNumber: 1, EpisodeCount: 1}}}, nil
}
func (metadata) GetSeason(context.Context, int, int) (model.Season, error) {
	return model.Season{SeasonNumber: 1, Episodes: []model.Episode{{EpisodeNumber: 1}}}, nil
}
func (metadata) SearchMovie(context.Context, string) ([]model.MovieCandidate, error) { return nil, nil }
func (metadata) GetMovie(context.Context, int) (model.Movie, error)                  { return model.Movie{}, nil }
func (metadata) GetMovieReleaseDates(context.Context, int) (model.MovieReleaseDates, error) {
	return model.MovieReleaseDates{}, nil
}

func TestFullTVWorkflowDryRunThenLink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	state := filepath.Join(root, "state")
	_ = os.MkdirAll(filepath.Join(source, "Show"), 0755)
	src := filepath.Join(source, "Show", "Show.S01E01.mkv")
	_ = os.WriteFile(src, []byte("video"), 0644)
	cfg := config.Config{Paths: config.Paths{TVSource: source, MovieSource: filepath.Join(root, "movies"), AnimeSource: filepath.Join(root, "anime"), TVTarget: target, MovieTarget: filepath.Join(root, "mt"), AnimeTarget: filepath.Join(root, "at")}, Matching: config.Matching{MinScore: 80, MinMargin: 15}, State: config.State{Directory: state}}
	p := Processor{Torrents: torrents{model.Torrent{Name: "Show S01", ContentPath: filepath.Join(source, "Show"), SavePath: source, Progress: 1}, []model.TorrentFile{{Name: "Show/Show.S01E01.mkv", Priority: 1, Progress: 1}}}, Metadata: metadata{}, Config: cfg}
	r, err := p.Process(context.Background(), "hash", true, 0)
	if err != nil || len(r.Plan) != 1 {
		t.Fatalf("dry run %+v %v", r, err)
	}
	if r.PlexMatch == nil || r.PlexMatch.Action != linker.Planned || r.PlexMatch.Content != "Title: Show\nYear: 2020\nTmdbId: 7\n" {
		t.Fatalf("dry-run plexmatch=%+v", r.PlexMatch)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("dry-run mutated target")
	}
	r, err = p.Process(context.Background(), "hash", false, 0)
	if err != nil || len(r.Plan) != 1 {
		t.Fatalf("process %+v %v", r, err)
	}
	si, _ := os.Stat(src)
	di, _ := os.Stat(r.Plan[0].Target)
	if !os.SameFile(si, di) {
		t.Fatal("target is not source hardlink")
	}
	if r.PlexMatch == nil || r.PlexMatch.Action != linker.Created {
		t.Fatalf("plexmatch=%+v", r.PlexMatch)
	}
	matchContent, err := os.ReadFile(filepath.Join(target, "Show (2020)", ".plexmatch"))
	if err != nil || string(matchContent) != "Title: Show\nYear: 2020\nTmdbId: 7\n" {
		t.Fatalf("match file content=%q err=%v", matchContent, err)
	}
}

func TestConflictingPlexMatchPreventsHardlinks(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(source, "Show.S01E01.mkv")
	if err := os.WriteFile(src, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	matchDir := filepath.Join(target, "Show (2020)")
	if err := os.MkdirAll(matchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(matchDir, ".plexmatch"), []byte("TmdbId: 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Paths: config.Paths{TVSource: source, TVTarget: target, MovieSource: filepath.Join(root, "movies"), MovieTarget: filepath.Join(root, "mt"), AnimeSource: filepath.Join(root, "anime"), AnimeTarget: filepath.Join(root, "at")}, Matching: config.Matching{MinScore: 80, MinMargin: 15}, State: config.State{Directory: filepath.Join(root, "state")}}
	p := Processor{Torrents: torrents{model.Torrent{Name: "Show S01", ContentPath: source, SavePath: source, Progress: 1}, []model.TorrentFile{{Name: "Show.S01E01.mkv", Priority: 1, Progress: 1}}}, Metadata: metadata{}, Config: cfg}
	result, err := p.Process(context.Background(), "conflict", false, 0)
	if !errors.Is(err, ErrConflict) || result.PlexMatch == nil || result.PlexMatch.Action != linker.Conflict {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	mediaTarget := filepath.Join(matchDir, "Season 01", "Show (2020) - S01E01.mkv")
	if _, err := os.Stat(mediaTarget); !os.IsNotExist(err) {
		t.Fatalf("hardlink created despite plexmatch conflict: %v", err)
	}
}

func TestAIFallbackSearchesTMDBAndVerifiesMovieYear(t *testing.T) {
	root := t.TempDir()
	source, target := filepath.Join(root, "movies"), filepath.Join(root, "target")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "Ottochennoe.Lezvie.1996.RUS.HDRip.avi"
	if err := os.WriteFile(filepath.Join(source, name), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := &fakeAI{result: ai.Result{Status: ai.Resolved, MediaType: model.KindMovie, CanonicalTitle: "Sling Blade", SearchQueries: []string{"Sling Blade 1996"}, Confidence: .97}}
	cfg := config.Config{AI: config.AI{Enabled: true, WebSearch: "allow", MinConfidence: .9, Cache: true}, Paths: config.Paths{TVSource: filepath.Join(root, "tv"), MovieSource: source, AnimeSource: filepath.Join(root, "anime"), TVTarget: filepath.Join(root, "tt"), MovieTarget: target, AnimeTarget: filepath.Join(root, "at")}, Matching: config.Matching{MinScore: 80, MinMargin: 15}, State: config.State{Directory: filepath.Join(root, "state")}}
	p := Processor{Torrents: torrents{t: model.Torrent{Name: name, ContentPath: filepath.Join(source, name), SavePath: source, Progress: 1}, f: []model.TorrentFile{{Name: name, Priority: 1, Progress: 1}}}, Metadata: ottoMetadata{}, AI: resolver, AIProvider: "xai", AIModel: "test", Config: cfg}
	result, err := p.Process(context.Background(), "otto", true, 0)
	if err != nil || result.Match.ID != 8973 || !result.AI.Verified || resolver.calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, resolver.calls, err)
	}
	if len(resolver.requests) != 1 || resolver.requests[0].Files[0] != name || strings.Contains(resolver.requests[0].Files[0], root) {
		t.Fatalf("external evidence leaked absolute path: %+v", resolver.requests)
	}
	result, err = p.Process(context.Background(), "otto", true, 0)
	if err != nil || result.AI.CacheHit || resolver.calls != 2 {
		t.Fatalf("cache result=%+v calls=%d err=%v", result.AI, resolver.calls, err)
	}
	if _, statErr := os.Stat(filepath.Join(cfg.State.Directory, "ai-cache")); !os.IsNotExist(statErr) {
		t.Fatalf("AI dry-run mutated cache: %v", statErr)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatal("AI dry-run mutated target")
	}
}

func TestNoAILeavesLongTailUnresolved(t *testing.T) {
	resolver := &fakeAI{result: ai.Result{Status: ai.Resolved, MediaType: model.KindMovie, Confidence: 1}}
	p := Processor{Metadata: ottoMetadata{}, AI: resolver, Config: config.Config{AI: config.AI{Enabled: true}, Matching: config.Matching{MinScore: 80, MinMargin: 15}}}
	r := Result{}
	_, _, err := p.resolve(context.Background(), model.KindMovie, model.Evidence{Titles: []model.WeightedTitle{{Title: "unknown"}}}, 0, &r)
	if !errors.Is(err, ErrUnresolved) || resolver.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, resolver.calls)
	}
}

func TestDeterministicHighConfidenceDoesNotCallAI(t *testing.T) {
	resolver := &fakeAI{err: errors.New("must not be called")}
	p := Processor{Metadata: metadata{}, AI: resolver, Config: config.Config{AI: config.AI{Enabled: true}, Matching: config.Matching{MinScore: 80, MinMargin: 15}}}
	result := Result{}
	match, _, err := p.resolve(context.Background(), model.KindTV, model.Evidence{Titles: []model.WeightedTitle{{Title: "Show"}}, Episodes: []model.EpisodeRef{{Season: 1, Episode: 1}}}, 0, &result)
	if err != nil || match.ID != 7 || resolver.calls != 0 {
		t.Fatalf("match=%+v calls=%d err=%v", match, resolver.calls, err)
	}
}

func TestAIProviderErrorCreatesNoTarget(t *testing.T) {
	root := t.TempDir()
	source, target := filepath.Join(root, "movies"), filepath.Join(root, "target")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "Unknown.1996.avi"
	if err := os.WriteFile(filepath.Join(source, name), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	outputErr := &ai.ProviderOutputError{Err: fmt.Errorf("%w: output token limit reached", ai.ErrProviderOutput), ConfiguredModel: "openrouter/free", ActualModel: "reasoning/free-model", FinishReason: "length", CompletionTokens: 2048, ReasoningTokens: 1987}
	resolver := &fakeAI{err: ai.WithProviderRequests(outputErr, 1)}
	cfg := config.Config{AI: config.AI{Enabled: true, WebSearch: "allow", MinConfidence: .9}, Paths: config.Paths{TVSource: filepath.Join(root, "tv"), MovieSource: source, AnimeSource: filepath.Join(root, "anime"), MovieTarget: target}, Matching: config.Matching{MinScore: 80, MinMargin: 15}, State: config.State{Directory: filepath.Join(root, "state")}}
	p := Processor{Torrents: torrents{t: model.Torrent{Name: name, ContentPath: filepath.Join(source, name), SavePath: source, Progress: 1}, f: []model.TorrentFile{{Name: name, Priority: 1, Progress: 1}}}, Metadata: ottoMetadata{}, AI: resolver, Config: cfg}
	result, err := p.Process(context.Background(), "unknown", false, 0)
	if !errors.Is(err, ErrUnresolved) || result.AI.Error != "AI consultant unavailable" {
		t.Fatalf("err=%v diagnostics=%+v", err, result.AI)
	}
	if result.AI.ProviderRequests != 1 {
		t.Fatalf("failed provider request was not counted: %+v", result.AI)
	}
	if result.AI.Model != "openrouter/free" || result.AI.ActualModel != "reasoning/free-model" || result.AI.FinishReason != "length" || result.AI.CompletionTokens != 2048 || result.AI.ReasoningTokens != 1987 {
		t.Fatalf("provider output diagnostics were not retained: %+v", result.AI)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatal("provider error created target")
	}
}

func TestAIHTTPErrorPreservesSafeMetadata(t *testing.T) {
	providerErr := &ai.ProviderHTTPError{Provider: "openrouter", StatusCode: 429, ErrorCode: "rate_limit", RetryAfterSeconds: 12, Message: "request limited", SanitizedRequest: `{"model":"safe"}`, SanitizedResponse: `{"error":"safe"}`}
	p := Processor{AI: &fakeAI{err: ai.WithProviderRequests(providerErr, 1)}, AIProvider: "openrouter", AIModel: "openrouter/free", Config: config.Config{State: config.State{Directory: t.TempDir()}}}
	result := Result{}
	_, _, err := p.callAI(context.Background(), ai.Request{Task: ai.IdentifyMedia, Kind: model.KindMovie}, &result)
	if err == nil || result.AI.HTTPStatus != 429 || result.AI.ProviderErrorCode != "rate_limit" || result.AI.ProviderError != "request limited" || result.AI.ProviderRequest != `{"model":"safe"}` || result.AI.ProviderResponse != `{"error":"safe"}` || result.AI.RetryAfterSeconds != 12 || result.AI.Provider != "openrouter" {
		t.Fatalf("diagnostics=%+v err=%v", result.AI, err)
	}
	wire, marshalErr := json.Marshal(result)
	if marshalErr != nil || strings.Contains(string(wire), "Authorization") || strings.Contains(string(wire), "secret") {
		t.Fatalf("unsafe diagnostics=%s err=%v", wire, marshalErr)
	}
}

func TestAIEpisodeMappingMustExistInTMDB(t *testing.T) {
	resolver := &fakeAI{result: ai.Result{Status: ai.Resolved, MediaType: model.KindTV, Confidence: .99, EpisodeMappings: []ai.EpisodeMapping{{SourceFile: "BoJack.Horseman.S01E13.mkv", Season: 0, Episode: 1, Confidence: .99}}}}
	p := Processor{Metadata: bojackMetadata{}, AI: resolver, AIProvider: "xai", AIModel: "test", Config: config.Config{AI: config.AI{Enabled: true, MinConfidence: .9}, State: config.State{Directory: t.TempDir()}}}
	media := []model.MediaFile{{Name: "BoJack.Horseman.S01E13.mkv", Ref: model.EpisodeRef{Season: 1, Episode: 13}}}
	result := Result{}
	if err := p.resolveAIEpisodes(context.Background(), model.KindTV, "BoJack", 61222, media, model.Evidence{}, &result); err != nil {
		t.Fatal(err)
	}
	if media[0].Ref.Season != 0 || media[0].Ref.Episode != 1 || !result.AI.Verified {
		t.Fatalf("media=%+v diagnostics=%+v", media, result.AI)
	}

	resolver.result.EpisodeMappings[0].Episode = 999
	p.Config.State.Directory = t.TempDir()
	media[0].Ref = model.EpisodeRef{Season: 1, Episode: 13}
	if err := p.resolveAIEpisodes(context.Background(), model.KindTV, "BoJack", 61222, media, model.Evidence{}, &Result{}); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("nonexistent episode err=%v", err)
	}
}

func TestVForVendettaUsesAlternateTMDBReleaseYear(t *testing.T) {
	provider := &movieMetadata{candidates: []model.MovieCandidate{{ID: 752, Title: "V for Vendetta", ReleaseDate: "2006-03-17"}}, dates: map[int]model.MovieReleaseDates{752: {Results: []model.MovieReleaseCountry{{CountryCode: "US", ReleaseDates: []model.MovieReleaseDate{{Type: 1, ReleaseDate: "2005-12-11T00:00:00.000Z"}, {Type: 3, ReleaseDate: "2006-03-17T00:00:00.000Z"}}}}}}}
	p := Processor{Metadata: provider, Config: config.Config{Matching: config.Matching{MinScore: 80, MinMargin: 15}}}
	result := Result{}
	match, _, err := p.resolve(context.Background(), model.KindMovie, model.Evidence{Titles: []model.WeightedTitle{{Title: "V for Vendetta", Weight: 3}}, Year: 2005}, 0, &result)
	if err != nil || match.ID != 752 || match.Score != 90 {
		t.Fatalf("match=%+v candidates=%+v err=%v", match, result.Candidates, err)
	}
	if len(provider.releaseDateCalls) != 1 || provider.releaseDateCalls[0] != 752 {
		t.Fatalf("release date calls=%v", provider.releaseDateCalls)
	}
	if got := match.Breakdown; len(got) != 2 || got[1] != "year_release_date=30" {
		t.Fatalf("breakdown=%v", got)
	}
}

func TestExactPrimaryMovieYearSkipsReleaseDatesRequest(t *testing.T) {
	provider := &movieMetadata{candidates: []model.MovieCandidate{{ID: 1, Title: "Film", ReleaseDate: "2005-01-01"}}}
	p := Processor{Metadata: provider, Config: config.Config{Matching: config.Matching{MinScore: 80, MinMargin: 15}}}
	result := Result{}
	match, _, err := p.resolve(context.Background(), model.KindMovie, model.Evidence{Titles: []model.WeightedTitle{{Title: "Film", Weight: 3}}, Year: 2005}, 0, &result)
	if err != nil || match.ID != 1 {
		t.Fatalf("match=%+v err=%v", match, err)
	}
	if len(provider.releaseDateCalls) != 0 {
		t.Fatalf("unnecessary release date calls=%v", provider.releaseDateCalls)
	}
}

func TestMovieReleaseDateValidationIsLimitedToTopThree(t *testing.T) {
	provider := &movieMetadata{candidates: []model.MovieCandidate{{ID: 1, Title: "Film", ReleaseDate: "2006-01-01"}, {ID: 2, Title: "Film Two", ReleaseDate: "2006-01-01"}, {ID: 3, Title: "Film Three", ReleaseDate: "2006-01-01"}, {ID: 4, Title: "Film Four", ReleaseDate: "2006-01-01"}}}
	p := Processor{Metadata: provider}
	_, err := p.validateTopMovieReleaseYears(context.Background(), model.Evidence{Titles: []model.WeightedTitle{{Title: "Film", Weight: 3}}, Year: 2005}, provider.candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.releaseDateCalls) != 3 {
		t.Fatalf("release_dates calls=%v, want top 3 only", provider.releaseDateCalls)
	}
}

func TestAllStandardTMDBReleaseTypesCanConfirmYear(t *testing.T) {
	for releaseType := 1; releaseType <= 6; releaseType++ {
		dates := model.MovieReleaseDates{Results: []model.MovieReleaseCountry{{ReleaseDates: []model.MovieReleaseDate{{Type: releaseType, ReleaseDate: "2005-01-01T00:00:00Z"}}}}}
		if !hasReleaseYear(dates, 2005) {
			t.Errorf("release type %d did not confirm year", releaseType)
		}
	}
	invalid := model.MovieReleaseDates{Results: []model.MovieReleaseCountry{{ReleaseDates: []model.MovieReleaseDate{{Type: 7, ReleaseDate: "2005-01-01T00:00:00Z"}}}}}
	if hasReleaseYear(invalid, 2005) {
		t.Fatal("non-standard release type confirmed year")
	}
}

func TestBoJackNamedExtraEpisodeRemapsToSpecial(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	files := make([]model.TorrentFile, 13)
	for i := range files {
		name := fmt.Sprintf("BoJack.Horseman.S01E%02d.mkv", i+1)
		if i == 12 {
			name = "BoJack Horseman s1e13 - Sabrina's Christmas Wish.mkv"
		}
		files[i] = model.TorrentFile{Name: name, Priority: 1, Progress: 1}
		_ = os.MkdirAll(filepath.Dir(filepath.Join(source, name)), 0755)
		_ = os.WriteFile(filepath.Join(source, name), []byte("video"), 0644)
	}
	cfg := config.Config{Paths: config.Paths{TVSource: source, MovieSource: filepath.Join(root, "movies"), AnimeSource: filepath.Join(root, "anime"), TVTarget: target, MovieTarget: filepath.Join(root, "movie-target"), AnimeTarget: filepath.Join(root, "anime-target")}, Matching: config.Matching{MinScore: 80, MinMargin: 15}, State: config.State{Directory: filepath.Join(root, "state")}}
	p := Processor{Torrents: torrents{t: model.Torrent{Name: "BoJack Horseman (1080p WEB-DL)", ContentPath: filepath.Join(source, "BoJack Horseman"), SavePath: source, Progress: 1}, f: files}, Metadata: bojackMetadata{}, Config: cfg}
	result, err := p.Process(context.Background(), "bojack", true, 0)
	if err != nil {
		t.Fatalf("expected successful special remap, got result=%+v err=%v", result, err)
	}
	if result.Match.ID != 61222 || result.Match.Score < 80 {
		t.Fatalf("series identification did not succeed: %+v", result.Match)
	}
	if len(result.EpisodeValidation) != 13 {
		t.Fatalf("validations=%d, want 13", len(result.EpisodeValidation))
	}
	for i, validation := range result.EpisodeValidation {
		if validation.State != model.EpisodeValid {
			t.Errorf("episode %d state=%s", i+1, validation.State)
		}
	}
	last := result.EpisodeValidation[12]
	if !last.Remapped || last.ParsedSeason != 1 || last.ParsedEpisode != 13 || last.Season != 0 || last.Episode != 1 {
		t.Fatalf("unexpected special mapping: %+v", last)
	}
	if len(result.Plan) != 13 || len(result.Actions) != 13 {
		t.Fatalf("plan/actions lengths: %d/%d", len(result.Plan), len(result.Actions))
	}
	wantSuffix := filepath.Join("Season 00", "BoJack Horseman (2014) - S00E01.mkv")
	if !strings.HasSuffix(result.Plan[12].Target, wantSuffix) {
		t.Fatalf("special target=%q, want suffix %q", result.Plan[12].Target, wantSuffix)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatal("dry-run created target root")
	}
}

func TestMissingEpisodeWithoutTitleIsNotRemappedByPosition(t *testing.T) {
	p := Processor{Metadata: bojackMetadata{}}
	files := []model.MediaFile{{Name: "BoJack.Horseman.S01E13.mkv", Ref: model.EpisodeRef{Season: 1, Episode: 13}}}
	validations, valid := p.validateEpisodes(context.Background(), 61222, files)
	if valid || validations[0].State != model.EpisodeUnresolved || validations[0].Remapped {
		t.Fatalf("position-only special remap occurred: %+v", validations)
	}
}

func TestSpecialTitleMatchMustBeUniqueAndStrong(t *testing.T) {
	if episode, ok := findSpecialEpisode("Sabrinas Christmas Wish", []model.Episode{{EpisodeNumber: 1, Name: "Sabrina's Christmas Wish"}}); !ok || episode.EpisodeNumber != 1 {
		t.Fatalf("exact normalized special did not match: %+v %v", episode, ok)
	}
	if _, ok := findSpecialEpisode("Christmas", []model.Episode{{EpisodeNumber: 1, Name: "Sabrina's Christmas Wish"}}); ok {
		t.Fatal("weak special title matched")
	}
	if _, ok := findSpecialEpisode("Holiday", []model.Episode{{EpisodeNumber: 1, Name: "Holiday"}, {EpisodeNumber: 2, Name: "Holiday"}}); ok {
		t.Fatal("ambiguous special title matched")
	}
}

func TestCounterpartUsesFileTitleAfterEmptyNoisyQuery(t *testing.T) {
	testCounterpartFallback(t, false)
}

func TestFailedFirstQueryDoesNotPreventSuccessfulFallback(t *testing.T) {
	testCounterpartFallback(t, true)
}

func testCounterpartFallback(t *testing.T, firstError bool) {
	t.Helper()
	files := make([]model.TorrentFile, 10)
	for i := range files {
		files[i] = model.TorrentFile{Name: fmt.Sprintf("Counterpart.S02E%02d.mp4", i+1), Priority: 1, Progress: 1}
	}
	evidence, _ := release.Parse(model.Torrent{Name: "Counterpart 2 - LostFilm.TV [MP4]", ContentPath: "Counterpart 2 - LostFilm.TV [MP4]"}, files, model.KindTV)
	provider := &counterpartMetadata{firstError: firstError}
	p := Processor{Metadata: provider, Config: config.Config{Matching: config.Matching{MinScore: 80, MinMargin: 15}}}
	result := Result{}
	match, _, err := p.resolve(context.Background(), model.KindTV, evidence, 0, &result)
	if err != nil {
		t.Fatalf("resolve failed: %v; queries=%v; evidence=%+v", err, provider.queries, evidence)
	}
	if match.ID != 63646 || match.Name != "Counterpart" || match.Year != 2017 {
		t.Fatalf("wrong match: %+v", match)
	}
	if len(provider.queries) < 2 || provider.queries[1] != "Counterpart" {
		t.Fatalf("file-derived fallback query not used: %v", provider.queries)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("TMDB candidates were not deduplicated: %+v", result.Candidates)
	}
}
