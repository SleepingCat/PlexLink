package matcher

import (
	"context"
	"github.com/SleepingCat/PlexLink/internal/model"
	"testing"
)

type seasons map[int]model.Season

func (s seasons) GetSeason(_ context.Context, _ int, n int) (model.Season, error) { return s[n], nil }
func TestAmbiguousStaysUnresolved(t *testing.T) {
	e := model.Evidence{Titles: []model.WeightedTitle{{Title: "The Killing", Weight: 3}}}
	got, _, err := TV(context.Background(), seasons{}, e, []model.TVCandidate{{ID: 1, Name: "The Killing", FirstAirDate: "2007-01-01"}, {ID: 2, Name: "The Killing", FirstAirDate: "2011-01-01"}}, 80, 15)
	if err != nil || got.ID != 0 {
		t.Fatalf("ambiguous match=%+v err=%v", got, err)
	}
}
func TestAnimeAbsolutePolicy(t *testing.T) {
	files := []model.MediaFile{{Ref: model.EpisodeRef{Episode: 3, Absolute: true}}}
	show := model.TVShow{Seasons: []model.SeasonSummary{{SeasonNumber: 0, EpisodeCount: 1}, {SeasonNumber: 1, EpisodeCount: 8}}}
	if err := MapAnimeAbsolute(show, files); err != nil || files[0].Ref.Season != 1 {
		t.Fatalf("mapping %+v %v", files, err)
	}
	files[0].Ref.Episode = 3
	if err := MapAnimeAbsolute(model.TVShow{Seasons: []model.SeasonSummary{{SeasonNumber: 1, EpisodeCount: 2}}}, files); err == nil {
		t.Fatal("out-of-range accepted")
	}
}
