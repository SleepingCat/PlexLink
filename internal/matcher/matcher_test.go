package matcher

import (
	"context"
	"github.com/SleepingCat/PlexLink/internal/model"
	"strconv"
	"strings"
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

func TestBreakdownContributionsSumToScore(t *testing.T) {
	evidence := model.Evidence{Titles: []model.WeightedTitle{{Title: "Counterpart", Weight: 3}}, Year: 2017, Episodes: []model.EpisodeRef{{Season: 2, Episode: 1}, {Season: 2, Episode: 2}, {Season: 2, Episode: 3}}}
	_, tvCandidates, err := TV(context.Background(), seasons{2: {SeasonNumber: 2, Episodes: []model.Episode{{EpisodeNumber: 1}, {EpisodeNumber: 3}}}}, evidence, []model.TVCandidate{{ID: 1, Name: "Counterpart", FirstAirDate: "2017-01-01"}}, 80, 15)
	if err != nil {
		t.Fatal(err)
	}
	assertBreakdownSum(t, tvCandidates[0])
	_, movieCandidates := Movie(model.Evidence{Titles: evidence.Titles, Year: 2017}, []model.MovieCandidate{{ID: 2, Title: "Counterpart", ReleaseDate: "2017-01-01"}}, 80, 15)
	assertBreakdownSum(t, movieCandidates[0])
}

func assertBreakdownSum(t *testing.T, match model.Match) {
	t.Helper()
	sum := 0
	for _, item := range match.Breakdown {
		parts := strings.Split(item, "=")
		if len(parts) != 2 {
			t.Fatalf("non-numeric breakdown item %q", item)
		}
		value, err := strconv.Atoi(parts[1])
		if err != nil {
			t.Fatalf("non-numeric breakdown item %q: %v", item, err)
		}
		sum += value
	}
	if sum != match.Score {
		t.Fatalf("breakdown sum=%d, score=%d, breakdown=%v", sum, match.Score, match.Breakdown)
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
