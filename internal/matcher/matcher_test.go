package matcher

import (
	"context"
	"errors"
	"github.com/SleepingCat/PlexLink/internal/model"
	"strconv"
	"strings"
	"testing"
)

type seasons map[int]model.Season

func (s seasons) GetSeason(_ context.Context, _ int, n int) (model.Season, error) { return s[n], nil }

type missingSeasons struct{}

func (missingSeasons) GetSeason(context.Context, int, int) (model.Season, error) {
	return model.Season{}, errors.New("season not found")
}
func TestAmbiguousStaysUnresolved(t *testing.T) {
	e := model.Evidence{Titles: []model.WeightedTitle{{Title: "The Killing", Weight: 3}}}
	got, _, err := TV(context.Background(), seasons{}, e, []model.TVCandidate{{ID: 1, Name: "The Killing", FirstAirDate: "2007-01-01"}, {ID: 2, Name: "The Killing", FirstAirDate: "2011-01-01"}}, 80, 15)
	if err != nil || got.ID != 0 {
		t.Fatalf("ambiguous match=%+v err=%v", got, err)
	}
}

func TestNormalizedExactTitleScore(t *testing.T) {
	tests := []struct{ evidence, candidate string }{
		{"The Devils Hour", "The Devil's Hour"},
		{"The Devils Hour", "The Devil’s Hour"},
		{"Marvels Daredevil", "Marvel's Daredevil"},
		{"Counterpart", "Counterpart"},
	}
	for _, tc := range tests {
		t.Run(tc.candidate, func(t *testing.T) {
			evidence := model.Evidence{Titles: []model.WeightedTitle{{Title: tc.evidence, Weight: 3}}}
			if got := scoreTitles(evidence, tc.candidate); got != 60 {
				t.Fatalf("scoreTitles(%q, %q) = %d, want 60", tc.evidence, tc.candidate, got)
			}
		})
	}
}

func TestBreakdownContributionsSumToScore(t *testing.T) {
	evidence := model.Evidence{Titles: []model.WeightedTitle{{Title: "Counterpart", Weight: 3}}, Year: 2017, Episodes: []model.EpisodeRef{{Season: 2, Episode: 1}, {Season: 2, Episode: 2}, {Season: 2, Episode: 3}}}
	_, tvCandidates, err := TV(context.Background(), seasons{2: {SeasonNumber: 2, Episodes: []model.Episode{{EpisodeNumber: 1}, {EpisodeNumber: 3}}}}, evidence, []model.TVCandidate{{ID: 1, Name: "Counterpart", FirstAirDate: "2017-01-01"}}, 80, 15)
	if err != nil {
		t.Fatal(err)
	}
	assertBreakdownSum(t, tvCandidates[0])
	_, movieCandidates := Movie(model.Evidence{Titles: evidence.Titles, Year: 2017}, []model.MovieCandidate{{ID: 2, Title: "Counterpart", ReleaseDate: "2017-01-01"}}, nil, 80, 15)
	assertBreakdownSum(t, movieCandidates[0])
}

func TestMissingRepresentativeEpisodeDoesNotPenalizeSeriesIdentity(t *testing.T) {
	evidence := model.Evidence{Titles: []model.WeightedTitle{{Title: "BoJack Horseman", Weight: 3}}, Episodes: []model.EpisodeRef{{Season: 1, Episode: 1}, {Season: 1, Episode: 7}, {Season: 1, Episode: 13}}}
	episodes := make([]model.Episode, 12)
	for i := range episodes {
		episodes[i].EpisodeNumber = i + 1
	}
	match, candidates, err := TV(context.Background(), seasons{1: {SeasonNumber: 1, Episodes: episodes}}, evidence, []model.TVCandidate{{ID: 61222, Name: "BoJack Horseman", FirstAirDate: "2014-08-22"}}, 80, 15)
	if err != nil || match.ID != 61222 {
		t.Fatalf("series match=%+v candidates=%+v err=%v", match, candidates, err)
	}
	if match.Score != 85 {
		t.Fatalf("score=%d breakdown=%v, want 85", match.Score, match.Breakdown)
	}
	assertBreakdownSum(t, match)
}

func TestMovieYearScoringSources(t *testing.T) {
	evidence := model.Evidence{Titles: []model.WeightedTitle{{Title: "Film", Weight: 3}}, Year: 2005}
	tests := []struct {
		name      string
		candidate model.MovieCandidate
		validated bool
		wantScore int
		wantYear  string
	}{{"primary", model.MovieCandidate{ID: 1, Title: "Film", ReleaseDate: "2005-01-01"}, false, 90, "year_primary=30"}, {"release date", model.MovieCandidate{ID: 2, Title: "Film", ReleaseDate: "2006-01-01"}, true, 90, "year_release_date=30"}, {"nearby unverified", model.MovieCandidate{ID: 3, Title: "Film", ReleaseDate: "2006-01-01"}, false, 65, "year_nearby_unverified=5"}, {"two years", model.MovieCandidate{ID: 4, Title: "Film", ReleaseDate: "2007-01-01"}, false, 60, "year_mismatch=0"}, {"large mismatch", model.MovieCandidate{ID: 5, Title: "Film", ReleaseDate: "2010-01-01"}, false, 20, "year_mismatch=-40"}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			validated := map[int]bool{}
			if tc.validated {
				validated[tc.candidate.ID] = true
			}
			scored := ScoreMovies(evidence, []model.MovieCandidate{tc.candidate}, validated)
			if scored[0].Score != tc.wantScore || scored[0].Breakdown[1] != tc.wantYear {
				t.Fatalf("score=%+v", scored[0])
			}
			assertBreakdownSum(t, scored[0])
		})
	}
}

func TestMovieMinMarginStillRejectsSameTitleRemakes(t *testing.T) {
	evidence := model.Evidence{Titles: []model.WeightedTitle{{Title: "The Film", Weight: 3}}, Year: 2005}
	candidates := []model.MovieCandidate{{ID: 1, Title: "The Film", ReleaseDate: "2005-01-01"}, {ID: 2, Title: "The Film", ReleaseDate: "2005-06-01"}}
	match, all := Movie(evidence, candidates, nil, 80, 15)
	if match.ID != 0 || len(all) != 2 || all[0].Score != all[1].Score {
		t.Fatalf("ambiguous movies matched: match=%+v all=%+v", match, all)
	}
}

func TestWeakTitleWithInvalidStructureCannotPass(t *testing.T) {
	evidence := model.Evidence{Titles: []model.WeightedTitle{{Title: "Horseman", Weight: 3}}, Episodes: []model.EpisodeRef{{Season: 9, Episode: 1}}}
	match, _, err := TV(context.Background(), missingSeasons{}, evidence, []model.TVCandidate{{ID: 1, Name: "BoJack Horseman", FirstAirDate: "2014-08-22"}}, 80, 15)
	if err != nil {
		t.Fatal(err)
	}
	if match.ID != 0 {
		t.Fatalf("weak structurally invalid candidate matched: %+v", match)
	}
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
