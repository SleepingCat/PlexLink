package kinopoisk

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/SleepingCat/PlexLink/internal/ensemble"
	"github.com/SleepingCat/PlexLink/internal/model"
)

const (
	resolverName = "kinopoisk"
	maxQueries   = 4
)

type Resolver struct{ client *Client }

func NewResolver(client *Client) *Resolver { return &Resolver{client: client} }
func (r *Resolver) Name() string           { return resolverName }
func (r *Resolver) Supports(kind model.Kind) bool {
	return kind == model.KindMovie || kind == model.KindTV || kind == model.KindAnime
}

func (r *Resolver) Resolve(ctx context.Context, req ensemble.ResolveRequest) ensemble.ResolverResult {
	result := ensemble.ResolverResult{Name: resolverName}
	if r == nil || r.client == nil || strings.TrimSpace(r.client.apiKey) == "" {
		result.Status = ensemble.ResolverError
		result.Error = &ensemble.OperationalError{Kind: ensemble.ErrorConfiguration, Message: "kinopoisk resolver is not configured"}
		return result
	}
	queries := titleQueries(req)
	if len(queries) == 0 {
		result.Status = ensemble.ResolverAbstain
		result.Diagnostics = []string{"no usable title queries"}
		return result
	}

	byID := make(map[int]ensemble.Candidate)
	var failures []*ensemble.OperationalError
	successful := 0
	for _, query := range queries {
		response, err := r.client.Search(ctx, query)
		if err != nil {
			failure := operationalError(err)
			failures = append(failures, failure)
			if ctx.Err() != nil {
				break
			}
			if failure.Kind == ensemble.ErrorRateLimited && !failure.Retryable {
				break
			}
			continue
		}
		successful++
		for _, movie := range response.Docs {
			kind, ok := mediaKind(movie.Type, req.Kind)
			if !ok || movie.ID <= 0 {
				continue
			}
			candidate := candidateFor(movie, kind, query, req.Year)
			if previous, exists := byID[movie.ID]; exists {
				candidate = mergeCandidate(previous, candidate)
			}
			byID[movie.ID] = candidate
		}
	}
	if ctx.Err() != nil {
		result.Status = ensemble.ResolverError
		result.Candidates = nil
		result.Error = operationalError(ctx.Err())
		return result
	}

	ids := make([]int, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		result.Candidates = append(result.Candidates, byID[id])
	}
	if len(result.Candidates) > 0 {
		result.Status = ensemble.ResolverOK
		if len(failures) > 0 {
			result.Warnings = []string{fmt.Sprintf("%d of %d kinopoisk searches failed", len(failures), len(queries))}
		}
		return result
	}
	if successful > 0 {
		result.Status = ensemble.ResolverAbstain
		if len(failures) > 0 {
			result.Warnings = []string{fmt.Sprintf("%d of %d kinopoisk searches failed", len(failures), len(queries))}
		}
		return result
	}
	result.Status = ensemble.ResolverError
	result.Error = failures[0]
	return result
}

func titleQueries(req ensemble.ResolveRequest) []string {
	values := []string{req.Title}
	values = append(values, req.TitleHypotheses...)
	titles := append([]model.WeightedTitle(nil), req.ParsedEvidence.Titles...)
	sort.SliceStable(titles, func(i, j int) bool { return titles[i].Weight > titles[j].Weight })
	for _, title := range titles {
		values = append(values, title.Title)
	}
	if req.TorrentName != "" {
		values = append(values, req.TorrentName)
	}
	for _, file := range req.Files {
		values = append(values, strings.TrimSuffix(filepath.Base(file.Name), filepath.Ext(file.Name)))
	}
	seen := make(map[string]bool)
	queries := make([]string, 0, maxQueries)
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := normalizeTitle(value)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		queries = append(queries, value)
		if len(queries) == maxQueries {
			break
		}
	}
	return queries
}

func mediaKind(providerType string, requested model.Kind) (model.Kind, bool) {
	switch providerType {
	case "movie", "cartoon":
		return model.KindMovie, requested == model.KindMovie
	case "tv-series":
		return model.KindTV, requested == model.KindTV
	case "animated-series":
		if requested == model.KindAnime {
			return model.KindAnime, true
		}
		return model.KindTV, requested == model.KindTV
	case "anime":
		return model.KindAnime, requested == model.KindAnime
	default:
		return "", false
	}
}

func candidateFor(movie Movie, kind model.Kind, query string, sourceYear int) ensemble.Candidate {
	names := uniqueNames(movie)
	title := movie.Name
	if title == "" {
		title = movie.AlternativeName
	}
	candidate := ensemble.Candidate{Identity: ensemble.EntityIdentity{
		Kind: kind, TMDBID: int(movie.ExternalID.TMDB), IMDbID: movie.ExternalID.IMDb,
		ProviderID: fmt.Sprintf("%d", movie.ID), Title: title, Year: movie.Year,
	}}
	queryNorm := normalizeTitle(query)
	seenExact := false
	bestSimilarity := 0.0
	for _, name := range names {
		nameNorm := normalizeTitle(name.value)
		if similarity(queryNorm, nameNorm) > bestSimilarity {
			bestSimilarity = similarity(queryNorm, nameNorm)
		}
		if nameNorm != queryNorm {
			continue
		}
		typ := ensemble.EvidenceTitleExactAKA
		points := ensemble.PointsTitleExactAKA
		if name.localized {
			typ, points = ensemble.EvidenceTitleExactLocalized, ensemble.PointsTitleExactLocalized
		}
		if !seenExact {
			candidate.Evidence = append(candidate.Evidence, ensemble.Evidence{Family: ensemble.FamilyTitle, Type: typ, Source: resolverName, Points: points, SafeDetails: "title matched"})
			seenExact = true
		}
	}
	if !seenExact && bestSimilarity >= 0.85 {
		candidate.Evidence = append(candidate.Evidence, ensemble.Evidence{Family: ensemble.FamilyTitle, Type: ensemble.EvidenceTitleFuzzyStrong, Source: resolverName, Points: ensemble.PointsTitleFuzzyStrong, SafeDetails: "title strongly matched"})
	}
	if sourceYear > 0 && movie.Year > 0 {
		switch delta := sourceYear - movie.Year; {
		case delta == 0:
			candidate.Evidence = append(candidate.Evidence, ensemble.Evidence{Family: ensemble.FamilyTime, Type: ensemble.EvidenceYearPrimaryExact, Source: resolverName, Points: ensemble.PointsYearPrimaryExact, SafeDetails: "year matched"})
		case delta >= -1 && delta <= 1:
			candidate.Evidence = append(candidate.Evidence, ensemble.Evidence{Family: ensemble.FamilyTime, Type: ensemble.EvidenceYearNearPlausible, Source: resolverName, Points: ensemble.PointsYearNearPlausible, SafeDetails: "year is near"})
		case delta < -2 || delta > 2:
			candidate.Evidence = append(candidate.Evidence, ensemble.Evidence{Family: ensemble.FamilyTime, Type: ensemble.EvidenceYearClearMismatch, Source: resolverName, Points: ensemble.PointsYearClearMismatch, SafeDetails: "year conflicts with source year"})
		}
	}
	return candidate
}

type namedTitle struct {
	value     string
	localized bool
}

func uniqueNames(movie Movie) []namedTitle {
	values := []namedTitle{{movie.Name, true}, {movie.AlternativeName, false}, {movie.EnName, false}}
	for _, name := range movie.Names {
		values = append(values, namedTitle{name.Name, name.Language == "ru"})
	}
	seen := make(map[string]bool)
	out := values[:0]
	for _, value := range values {
		key := normalizeTitle(value.value)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func mergeCandidate(a, b ensemble.Candidate) ensemble.Candidate {
	if a.Identity.TMDBID == 0 {
		a.Identity.TMDBID = b.Identity.TMDBID
	}
	if a.Identity.IMDbID == "" {
		a.Identity.IMDbID = b.Identity.IMDbID
	}
	seen := make(map[string]bool)
	for _, evidence := range a.Evidence {
		seen[string(evidence.Type)] = true
	}
	for _, evidence := range b.Evidence {
		if !seen[string(evidence.Type)] {
			a.Evidence = append(a.Evidence, evidence)
			seen[string(evidence.Type)] = true
		}
	}
	return a
}

func normalizeTitle(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		if unicode.IsSpace(r) {
			return ' '
		}
		return -1
	}, strings.TrimSpace(value))
}

func similarity(a, b string) float64 {
	ar, br := []rune(a), []rune(b)
	longest := len(ar)
	if len(br) > longest {
		longest = len(br)
	}
	if longest == 0 {
		return 1
	}
	previous := make([]int, len(br)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, ra := range ar {
		current := make([]int, len(br)+1)
		current[0] = i + 1
		for j, rb := range br {
			cost := 0
			if ra != rb {
				cost = 1
			}
			current[j+1] = min(current[j]+1, previous[j+1]+1, previous[j]+cost)
		}
		previous = current
	}
	return 1 - float64(previous[len(br)])/float64(longest)
}

func operationalError(err error) *ensemble.OperationalError {
	if errors.Is(err, context.Canceled) {
		return &ensemble.OperationalError{Kind: ensemble.ErrorCanceled, Message: "kinopoisk request canceled"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &ensemble.OperationalError{Kind: ensemble.ErrorTimeout, Message: "kinopoisk request timed out", Retryable: true}
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.DailyQuota {
			return &ensemble.OperationalError{Kind: ensemble.ErrorRateLimited, StatusCode: httpErr.StatusCode, Message: "kinopoisk daily request quota exhausted"}
		}
		switch httpErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return &ensemble.OperationalError{Kind: ensemble.ErrorAuthentication, StatusCode: httpErr.StatusCode, Message: "kinopoisk authentication failed"}
		case http.StatusTooManyRequests:
			return &ensemble.OperationalError{Kind: ensemble.ErrorRateLimited, StatusCode: httpErr.StatusCode, Message: "kinopoisk rate limit reached", Retryable: true}
		default:
			return &ensemble.OperationalError{Kind: ensemble.ErrorProvider, StatusCode: httpErr.StatusCode, Message: "kinopoisk provider error", Retryable: httpErr.StatusCode >= 500}
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &ensemble.OperationalError{Kind: ensemble.ErrorTimeout, Message: "kinopoisk request timed out", Retryable: true}
	}
	if strings.Contains(err.Error(), "decode search response") {
		return &ensemble.OperationalError{Kind: ensemble.ErrorInvalidResponse, Message: "kinopoisk returned an invalid response"}
	}
	return &ensemble.OperationalError{Kind: ensemble.ErrorProvider, Message: "kinopoisk request failed", Retryable: true}
}
