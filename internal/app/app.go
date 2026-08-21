package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SleepingCat/PlexLink/internal/config"
	"github.com/SleepingCat/PlexLink/internal/linker"
	"github.com/SleepingCat/PlexLink/internal/matcher"
	"github.com/SleepingCat/PlexLink/internal/model"
	"github.com/SleepingCat/PlexLink/internal/pathutil"
	"github.com/SleepingCat/PlexLink/internal/plexpath"
	"github.com/SleepingCat/PlexLink/internal/release"
	"github.com/SleepingCat/PlexLink/internal/state"
)

var (
	ErrIgnored        = errors.New("ignored")
	ErrUnresolved     = errors.New("unresolved")
	ErrAnimeNumbering = errors.New("unresolved anime numbering")
	ErrConflict       = errors.New("target conflict")
	ErrTorrent        = errors.New("qBittorrent error")
	ErrMetadata       = errors.New("TMDB error")
)

type TorrentClient interface {
	GetTorrent(context.Context, string) (model.Torrent, error)
	GetFiles(context.Context, string) ([]model.TorrentFile, error)
}
type MetadataProvider interface {
	SearchTV(context.Context, string) ([]model.TVCandidate, error)
	GetTV(context.Context, int) (model.TVShow, error)
	GetSeason(context.Context, int, int) (model.Season, error)
	SearchMovie(context.Context, string) ([]model.MovieCandidate, error)
	GetMovie(context.Context, int) (model.Movie, error)
}
type Processor struct {
	Torrents TorrentClient
	Metadata MetadataProvider
	Config   config.Config
}
type Result struct {
	Torrent    model.Torrent    `json:"torrent"`
	Kind       model.Kind       `json:"kind"`
	Evidence   model.Evidence   `json:"evidence"`
	Candidates []model.Match    `json:"candidates"`
	Match      model.Match      `json:"match"`
	Plan       []model.LinkPlan `json:"plan"`
	Actions    []linker.Action  `json:"actions"`
}

func (p *Processor) Process(ctx context.Context, hash string, dry bool, manualID int) (Result, error) {
	explicitID := manualID
	if manualID == 0 {
		var err error
		manualID, err = state.Resolution(p.Config.State.Directory, hash)
		if err != nil {
			return Result{}, err
		}
	}
	t, err := p.Torrents.GetTorrent(ctx, hash)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrTorrent, err)
	}
	r := Result{Torrent: t}
	if t.Progress < 1 {
		return r, fmt.Errorf("%w: torrent is not complete", ErrIgnored)
	}
	kind, sourceRoot, targetRoot, ok := p.kind(t.ContentPath)
	if !ok {
		return r, fmt.Errorf("%w: torrent is outside configured source roots", ErrIgnored)
	}
	r.Kind = kind
	files, err := p.Torrents.GetFiles(ctx, hash)
	if err != nil {
		return r, fmt.Errorf("%w: %v", ErrTorrent, err)
	}
	e, media := release.Parse(t, files, kind)
	r.Evidence = e
	if len(media) == 0 {
		return r, fmt.Errorf("%w: no supported completed media files", ErrIgnored)
	}
	for i := range media {
		media[i].Source = filepath.Join(t.SavePath, filepath.FromSlash(media[i].Name))
	}
	match, show, err := p.resolve(ctx, kind, e, manualID, &r)
	if err != nil {
		if errors.Is(err, ErrUnresolved) || errors.Is(err, ErrAnimeNumbering) {
			if !dry {
				_ = p.saveUnresolved(hash, r)
			}
		}
		return r, err
	}
	r.Match = match
	if kind == model.KindAnime {
		if err := matcher.MapAnimeAbsolute(show, media); err != nil {
			if !dry {
				_ = p.saveUnresolved(hash, r)
			}
			return r, ErrAnimeNumbering
		}
	}
	for _, f := range media {
		if kind != model.KindMovie && (f.Ref.Season < 0 || f.Ref.Episode == 0) {
			return r, fmt.Errorf("%w: missing episode numbering for %s", ErrUnresolved, f.Name)
		}
		target, err := plexpath.Build(targetRoot, kind, match, f)
		if err != nil {
			return r, err
		}
		r.Plan = append(r.Plan, model.LinkPlan{Source: f.Source, Target: target})
	}
	for _, plan := range r.Plan {
		action, err := linker.Link(sourceRoot, targetRoot, plan.Source, plan.Target, dry)
		if err != nil {
			return r, err
		}
		r.Actions = append(r.Actions, action)
		if action == linker.Conflict {
			return r, ErrConflict
		}
	}
	if explicitID > 0 && !dry {
		if err := state.SaveResolution(p.Config.State.Directory, hash, explicitID); err != nil {
			return r, fmt.Errorf("save manual resolution: %w", err)
		}
	}
	return r, nil
}
func (p *Processor) resolve(ctx context.Context, kind model.Kind, e model.Evidence, id int, r *Result) (model.Match, model.TVShow, error) {
	query := ""
	if len(e.Titles) > 0 {
		query = e.Titles[0].Title
	}
	if query == "" {
		return model.Match{}, model.TVShow{}, ErrUnresolved
	}
	if kind == model.KindMovie {
		if id > 0 {
			m, err := p.Metadata.GetMovie(ctx, id)
			if err != nil {
				return model.Match{}, model.TVShow{}, fmt.Errorf("%w: %v", ErrMetadata, err)
			}
			return model.Match{ID: m.ID, Name: m.Title, Year: year(m.ReleaseDate), Score: 999}, model.TVShow{}, nil
		}
		cs, err := p.Metadata.SearchMovie(ctx, query)
		if err != nil {
			return model.Match{}, model.TVShow{}, fmt.Errorf("%w: %v", ErrMetadata, err)
		}
		m, all := matcher.Movie(e, cs, p.Config.Matching.MinScore, p.Config.Matching.MinMargin)
		r.Candidates = all
		if m.ID == 0 {
			return m, model.TVShow{}, ErrUnresolved
		}
		return m, model.TVShow{}, nil
	}
	if id > 0 {
		s, err := p.Metadata.GetTV(ctx, id)
		if err != nil {
			return model.Match{}, s, fmt.Errorf("%w: %v", ErrMetadata, err)
		}
		return model.Match{ID: s.ID, Name: s.Name, Year: year(s.FirstAirDate), Score: 999}, s, nil
	}
	cs, err := p.Metadata.SearchTV(ctx, query)
	if err != nil {
		return model.Match{}, model.TVShow{}, fmt.Errorf("%w: %v", ErrMetadata, err)
	}
	m, all, err := matcher.TV(ctx, p.Metadata, e, cs, p.Config.Matching.MinScore, p.Config.Matching.MinMargin)
	r.Candidates = all
	if err != nil {
		return m, model.TVShow{}, err
	}
	if m.ID == 0 {
		return m, model.TVShow{}, ErrUnresolved
	}
	s, err := p.Metadata.GetTV(ctx, m.ID)
	if err != nil {
		return m, s, fmt.Errorf("%w: %v", ErrMetadata, err)
	}
	return m, s, nil
}
func (p *Processor) kind(path string) (model.Kind, string, string, bool) {
	types := []model.Kind{model.KindTV, model.KindMovie, model.KindAnime}
	sort.SliceStable(types, func(i, j int) bool {
		a, _ := p.Config.Paths.Roots(types[i])
		b, _ := p.Config.Paths.Roots(types[j])
		return len(a) > len(b)
	})
	for _, k := range types {
		s, t := p.Config.Paths.Roots(k)
		if pathutil.Contains(s, path) {
			return k, s, t, true
		}
	}
	return "", "", "", false
}
func (p *Processor) saveUnresolved(hash string, r Result) error {
	dir := filepath.Join(p.Config.State.Directory, "unresolved")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, strings.ToLower(hash)+".json"), b, 0o600)
}
func year(s string) int {
	var y int
	if len(s) >= 4 {
		fmt.Sscanf(s[:4], "%d", &y)
	}
	return y
}
