package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/config"
	"github.com/SleepingCat/PlexLink/internal/model"
)

type torrents struct {
	t model.Torrent
	f []model.TorrentFile
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
