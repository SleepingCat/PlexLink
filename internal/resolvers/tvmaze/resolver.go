package tvmaze

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/SleepingCat/PlexLink/internal/ensemble"
	"github.com/SleepingCat/PlexLink/internal/model"
)

const (
	resolverName       = "tvmaze"
	maxQueries         = 8
	maxCandidates      = 20
	maxEnrichedResults = 10
)

type Resolver struct{ client *Client }

func NewResolver(client *Client) *Resolver {
	if client == nil {
		client = New("", nil)
	}
	return &Resolver{client: client}
}

func (r *Resolver) Name() string { return resolverName }

func (r *Resolver) Supports(kind model.Kind) bool {
	return kind == model.KindTV || kind == model.KindAnime
}

func (r *Resolver) Resolve(ctx context.Context, req ensemble.ResolveRequest) ensemble.ResolverResult {
	result := ensemble.ResolverResult{Name: r.Name()}
	if !r.Supports(req.Kind) {
		result.Status = ensemble.ResolverAbstain
		return result
	}
	queries := uniqueQueries(req)
	if len(queries) == 0 {
		result.Status = ensemble.ResolverAbstain
		return result
	}

	shows := make(map[int]Show)
	var searchFailure error
	successfulSearches := 0
	for _, query := range queries {
		found, err := r.client.SearchShows(ctx, query)
		if err != nil {
			if ctx.Err() != nil {
				return failure(ctx.Err())
			}
			if errors.Is(err, ErrNotFound) {
				successfulSearches++
				continue
			}
			searchFailure = err
			continue
		}
		successfulSearches++
		for _, item := range found {
			if item.Show.ID <= 0 || strings.TrimSpace(item.Show.Name) == "" {
				if len(shows) > 0 {
					result.Warnings = appendWarning(result.Warnings, "TVMaze skipped an invalid additional search result")
					break
				}
				searchFailure = fmt.Errorf("invalid TVMaze search result schema")
				continue
			}
			if len(shows) >= maxCandidates {
				break
			}
			shows[item.Show.ID] = item.Show
		}
	}
	if len(shows) == 0 {
		if successfulSearches == 0 && searchFailure != nil {
			return failure(searchFailure)
		}
		result.Status = ensemble.ResolverAbstain
		if searchFailure != nil {
			result.Warnings = appendWarning(result.Warnings, "TVMaze skipped a failed title query")
		}
		return result
	}
	if searchFailure != nil {
		result.Warnings = appendWarning(result.Warnings, "TVMaze skipped a failed additional title query")
	}

	ids := make([]int, 0, len(shows))
	for id := range shows {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for i, id := range ids {
		show := shows[id]
		var akas []AKA
		var seasons []Season
		if i < maxEnrichedResults {
			var err error
			akas, err = r.client.AKAs(ctx, id)
			if err != nil && !errors.Is(err, ErrNotFound) {
				if ctx.Err() != nil {
					return failure(ctx.Err())
				}
				result.Warnings = appendWarning(result.Warnings, "TVMaze AKA enrichment unavailable")
			}
			if req.Season > 0 {
				seasons, err = r.client.Seasons(ctx, id)
				if err != nil && !errors.Is(err, ErrNotFound) {
					if ctx.Err() != nil {
						return failure(ctx.Err())
					}
					result.Warnings = appendWarning(result.Warnings, "TVMaze season enrichment unavailable")
				}
			}
		}
		result.Candidates = append(result.Candidates, candidate(req, show, akas, seasons))
	}
	result.Status = ensemble.ResolverOK
	return result
}

func uniqueQueries(req ensemble.ResolveRequest) []string {
	all := requestTitles(req)
	seen := make(map[string]struct{})
	result := make([]string, 0, min(len(all), maxQueries))
	for _, value := range all {
		value = strings.TrimSpace(value)
		key := normalizeTitle(value)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
		if len(result) == maxQueries {
			break
		}
	}
	return result
}

func requestTitles(req ensemble.ResolveRequest) []string {
	all := append([]string{req.Title}, req.TitleHypotheses...)
	for _, title := range req.ParsedEvidence.Titles {
		all = append(all, title.Title)
	}
	return all
}

func candidate(req ensemble.ResolveRequest, show Show, akas []AKA, seasons []Season) ensemble.Candidate {
	year := yearFromDate(show.Premiered)
	imdb := ""
	if show.Externals.IMDb != nil {
		imdb = strings.TrimSpace(*show.Externals.IMDb)
	}
	result := ensemble.Candidate{Identity: ensemble.EntityIdentity{
		Kind: req.Kind, ProviderID: fmt.Sprintf("tvmaze:%d", show.ID), IMDbID: imdb,
		Title: show.Name, Year: year,
	}}
	titles := requestTitles(req)
	if evidence, ok := bestTitleEvidence(titles, show.Name, false); ok {
		result.Evidence = append(result.Evidence, evidence)
	}
	for _, aka := range akas {
		if evidence, ok := bestTitleEvidence(titles, aka.Name, true); ok {
			result.Evidence = append(result.Evidence, evidence)
		}
	}
	if req.Year > 0 && year > 0 {
		delta := int(math.Abs(float64(req.Year - year)))
		switch {
		case delta == 0:
			result.Evidence = append(result.Evidence, ev(ensemble.FamilyTime, ensemble.EvidenceYearPrimaryExact, ensemble.PointsYearPrimaryExact, "premiere year matches"))
		case delta == 1:
			result.Evidence = append(result.Evidence, ev(ensemble.FamilyTime, ensemble.EvidenceYearNearPlausible, ensemble.PointsYearNearPlausible, "premiere year is near"))
		case delta >= 3:
			result.Evidence = append(result.Evidence, ev(ensemble.FamilyTime, ensemble.EvidenceYearClearMismatch, ensemble.PointsYearClearMismatch, "premiere year differs"))
		}
	}
	if req.Season > 0 {
		for _, season := range seasons {
			if season.Number == req.Season {
				result.Evidence = append(result.Evidence, ev(ensemble.FamilyEpisode, ensemble.EvidenceSeasonExists, ensemble.PointsSeasonExists, "requested season exists"))
				break
			}
		}
	}
	return result
}

func appendWarning(warnings []string, warning string) []string {
	for _, existing := range warnings {
		if existing == warning {
			return warnings
		}
	}
	if len(warnings) < 4 {
		return append(warnings, warning)
	}
	return warnings
}

func bestTitleEvidence(inputs []string, candidate string, aka bool) (ensemble.Evidence, bool) {
	candidate = normalizeTitle(candidate)
	best := 0.0
	for _, input := range inputs {
		best = max(best, similarity(normalizeTitle(input), candidate))
	}
	switch {
	case best == 1 && aka:
		return ev(ensemble.FamilyTitle, ensemble.EvidenceTitleExactAKA, ensemble.PointsTitleExactAKA, "AKA title matches"), true
	case best == 1:
		return ev(ensemble.FamilyTitle, ensemble.EvidenceTitleExactCanonical, ensemble.PointsTitleExactCanonical, "show title matches"), true
	case best >= .82:
		return ev(ensemble.FamilyTitle, ensemble.EvidenceTitleFuzzyStrong, ensemble.PointsTitleFuzzyStrong, "strong fuzzy title match"), true
	case best >= .6:
		return ev(ensemble.FamilyTitle, ensemble.EvidenceTitleFuzzyWeak, ensemble.PointsTitleFuzzyWeak, "weak fuzzy title match"), true
	default:
		return ensemble.Evidence{}, false
	}
}

func ev(family ensemble.EvidenceFamily, kind ensemble.EvidenceType, points int, details string) ensemble.Evidence {
	return ensemble.Evidence{Family: family, Type: kind, Source: resolverName, Points: points, SafeDetails: details}
}

func yearFromDate(value string) int {
	if len(value) < 4 {
		return 0
	}
	year, _ := strconv.Atoi(value[:4])
	return year
}

func normalizeTitle(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ")
}

func similarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	distance := levenshtein([]rune(a), []rune(b))
	return 1 - float64(distance)/float64(max(len([]rune(a)), len([]rune(b))))
}

func levenshtein(a, b []rune) int {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, ar := range a {
		current := make([]int, len(b)+1)
		current[0] = i + 1
		for j, br := range b {
			cost := 0
			if ar != br {
				cost = 1
			}
			current[j+1] = min(current[j]+1, previous[j+1]+1, previous[j]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}

func failure(err error) ensemble.ResolverResult {
	op := &ensemble.OperationalError{Kind: ensemble.ErrorProvider, Message: "TVMaze request failed", Retryable: true}
	var httpErr *HTTPError
	switch {
	case errors.Is(err, context.Canceled):
		op.Kind, op.Message, op.Retryable = ensemble.ErrorCanceled, "TVMaze request canceled", false
	case errors.Is(err, context.DeadlineExceeded):
		op.Kind, op.Message = ensemble.ErrorTimeout, "TVMaze request timed out"
	case errors.As(err, &httpErr):
		op.StatusCode = httpErr.StatusCode
		op.Retryable = httpErr.StatusCode >= 500
		if httpErr.StatusCode == http.StatusTooManyRequests {
			op.Kind, op.Message, op.Retryable = ensemble.ErrorRateLimited, "TVMaze rate limited request", true
		}
	case isNetworkTimeout(err):
		op.Kind, op.Message = ensemble.ErrorTimeout, "TVMaze request timed out"
	case strings.Contains(err.Error(), "decode TVMaze") || strings.Contains(err.Error(), "schema"):
		op.Kind, op.Message, op.Retryable = ensemble.ErrorInvalidResponse, "TVMaze returned an invalid response", false
	}
	return ensemble.ResolverResult{Name: resolverName, Status: ensemble.ResolverError, Error: op, Diagnostics: []string{op.Message}}
}

func isNetworkTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
