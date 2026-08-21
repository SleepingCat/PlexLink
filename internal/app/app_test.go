package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/config"
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
