package matcher

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/SleepingCat/PlexLink/internal/model"
	"github.com/SleepingCat/PlexLink/internal/release"
)

type TVMetadata interface {
	GetSeason(context.Context, int, int) (model.Season, error)
}
type Scored struct {
	Match model.Match
	Valid bool
}

func TV(ctx context.Context, p TVMetadata, e model.Evidence, candidates []model.TVCandidate, minScore, minMargin int) (model.Match, []model.Match, error) {
	var scored []Scored
	for _, c := range candidates {
		s := scoreTitles(e, c.Name, c.OriginalName)
		b := []string{fmt.Sprintf("title=%d", s)}
		year := yearOf(c.FirstAirDate)
		ys := yearScore(e.Year, year, 20, -25)
		s += ys
		b = append(b, fmt.Sprintf("year=%d", ys))
		valid := true
		bySeason := episodesBySeason(e.Episodes)
		for _, season := range sortedSeasons(bySeason) {
			eps := bySeason[season]
			if season == 0 {
				continue
			}
			detail, err := p.GetSeason(ctx, c.ID, season)
			if err != nil {
				valid = false
				b = append(b, fmt.Sprintf("season_%02d=0", season))
				continue
			}
			s += 15
			b = append(b, fmt.Sprintf("season_%02d=15", season))
			exists := map[int]bool{}
			for _, ep := range detail.Episodes {
				exists[ep.EpisodeNumber] = true
			}
			for _, n := range representatives(eps) {
				if exists[n] {
					s += 5
					b = append(b, fmt.Sprintf("episode_S%02dE%02d=5", season, n))
				} else {
					b = append(b, fmt.Sprintf("episode_S%02dE%02d=0", season, n))
				}
			}
		}
		scored = append(scored, Scored{model.Match{ID: c.ID, Name: c.Name, Year: year, Score: s, Breakdown: b}, valid})
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Match.Score > scored[j].Match.Score })
	all := make([]model.Match, len(scored))
	for i := range scored {
		all[i] = scored[i].Match
	}
	if len(scored) == 0 {
		return model.Match{}, all, nil
	}
	margin := scored[0].Match.Score
	if len(scored) > 1 {
		margin -= scored[1].Match.Score
	}
	scored[0].Match.Margin = margin
	if !scored[0].Valid || scored[0].Match.Score < minScore || margin < minMargin {
		return model.Match{}, all, nil
	}
	return scored[0].Match, all, nil
}

func Movie(e model.Evidence, candidates []model.MovieCandidate, validatedReleaseYears map[int]bool, minScore, minMargin int) (model.Match, []model.Match) {
	all := ScoreMovies(e, candidates, validatedReleaseYears)
	if len(all) == 0 {
		return model.Match{}, all
	}
	margin := all[0].Score
	if len(all) > 1 {
		margin -= all[1].Score
	}
	all[0].Margin = margin
	if all[0].Score < minScore || margin < minMargin {
		return model.Match{}, all
	}
	return all[0], all
}

func ScoreMovies(e model.Evidence, candidates []model.MovieCandidate, validatedReleaseYears map[int]bool) []model.Match {
	var all []model.Match
	for _, c := range candidates {
		titleScore := scoreTitles(e, c.Title, c.OriginalTitle)
		year := yearOf(c.ReleaseDate)
		yearLabel, yearContribution := movieYearScore(e.Year, year, validatedReleaseYears[c.ID])
		s := titleScore + yearContribution
		breakdown := []string{fmt.Sprintf("title=%d", titleScore), fmt.Sprintf("%s=%d", yearLabel, yearContribution)}
		all = append(all, model.Match{ID: c.ID, Name: c.Title, Year: year, Score: s, Breakdown: breakdown})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	return all
}

func movieYearScore(source, candidate int, releaseDateValidated bool) (string, int) {
	if source == 0 || candidate == 0 {
		return "year_unknown", 0
	}
	if source == candidate {
		return "year_primary", 30
	}
	if releaseDateValidated {
		return "year_release_date", 30
	}
	difference := source - candidate
	if difference < 0 {
		difference = -difference
	}
	if difference == 1 {
		return "year_nearby_unverified", 5
	}
	if difference == 2 {
		return "year_mismatch", 0
	}
	return "year_mismatch", -40
}

func scoreTitles(e model.Evidence, names ...string) int {
	best := 0
	for _, t := range e.Titles {
		a := release.NormalizeTitle(t.Title)
		for _, name := range names {
			b := release.NormalizeTitle(name)
			v := similarity(a, b)
			if v > best {
				best = v
			}
		}
	}
	return best
}
func similarity(a, b string) int {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 60
	}
	as, bs := strings.Fields(a), strings.Fields(b)
	hits := 0
	bm := map[string]bool{}
	for _, x := range bs {
		bm[x] = true
	}
	for _, x := range as {
		if bm[x] {
			hits++
		}
	}
	den := len(as)
	if len(bs) > den {
		den = len(bs)
	}
	v := 45 * hits / den
	if strings.Contains(a, b) || strings.Contains(b, a) {
		if v < 25 {
			v = 25
		}
	}
	return v
}
func yearOf(s string) int {
	if len(s) < 4 {
		return 0
	}
	n, _ := strconv.Atoi(s[:4])
	return n
}
func yearScore(source, candidate, exact, penalty int) int {
	if source == 0 || candidate == 0 {
		return 0
	}
	d := source - candidate
	if d < 0 {
		d = -d
	}
	if d == 0 {
		return exact
	}
	if d == 1 {
		return 10
	}
	if d > 2 {
		return penalty
	}
	return 0
}
func episodesBySeason(in []model.EpisodeRef) map[int][]int {
	m := map[int][]int{}
	for _, e := range in {
		if e.Episode > 0 {
			m[e.Season] = append(m[e.Season], e.Episode)
		}
	}
	return m
}
func sortedSeasons(bySeason map[int][]int) []int {
	seasons := make([]int, 0, len(bySeason))
	for season := range bySeason {
		seasons = append(seasons, season)
	}
	sort.Ints(seasons)
	return seasons
}
func representatives(in []int) []int {
	sort.Ints(in)
	u := []int{}
	for _, i := range []int{0, len(in) / 2, len(in) - 1} {
		if len(in) > 0 && (len(u) == 0 || u[len(u)-1] != in[i]) {
			u = append(u, in[i])
		}
	}
	return u
}

func MapAnimeAbsolute(show model.TVShow, files []model.MediaFile) error {
	regular := []model.SeasonSummary{}
	for _, s := range show.Seasons {
		if s.SeasonNumber != 0 {
			regular = append(regular, s)
		}
	}
	for i := range files {
		if !files[i].Ref.Absolute {
			continue
		}
		if len(regular) != 1 || files[i].Ref.Episode > regular[0].EpisodeCount {
			return fmt.Errorf("unresolved anime numbering")
		}
		files[i].Ref.Season = regular[0].SeasonNumber
	}
	return nil
}
