package tmdbresolver

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/SleepingCat/PlexLink/internal/ensemble"
	"github.com/SleepingCat/PlexLink/internal/matcher"
	"github.com/SleepingCat/PlexLink/internal/model"
	"github.com/SleepingCat/PlexLink/internal/release"
)

const source = "tmdb"

type Metadata interface {
	SearchTV(context.Context, string) ([]model.TVCandidate, error)
	GetSeason(context.Context, int, int) (model.Season, error)
	SearchMovie(context.Context, string) ([]model.MovieCandidate, error)
	GetMovieReleaseDates(context.Context, int) (model.MovieReleaseDates, error)
}

type Resolver struct{ metadata Metadata }

func New(metadata Metadata) *Resolver { return &Resolver{metadata: metadata} }
func (*Resolver) Name() string        { return source }
func (*Resolver) Supports(kind model.Kind) bool {
	return kind == model.KindMovie || kind == model.KindTV || kind == model.KindAnime
}

func (r *Resolver) Resolve(ctx context.Context, req ensemble.ResolveRequest) ensemble.ResolverResult {
	if !r.Supports(req.Kind) {
		return ensemble.ResolverResult{Name: source, Status: ensemble.ResolverAbstain}
	}
	if r.metadata == nil {
		return failed(ensemble.ErrorConfiguration, 0, "TMDB metadata client is not configured", false)
	}
	if req.Kind == model.KindMovie {
		return r.resolveMovies(ctx, req)
	}
	return r.resolveTV(ctx, req)
}

func (r *Resolver) resolveMovies(ctx context.Context, req ensemble.ResolveRequest) ensemble.ResolverResult {
	evidence := requestEvidence(req)
	queries := titleQueries(req, evidence)
	if len(queries) == 0 {
		return ensemble.ResolverResult{Name: source, Status: ensemble.ResolverAbstain}
	}
	byID := make(map[int]model.MovieCandidate)
	searchSucceeded := false
	searchFailed := false
	var diagnostics []string
	for _, query := range queries {
		rows, err := r.metadata.SearchMovie(ctx, query)
		if err != nil {
			if ctx.Err() != nil {
				failed := operationalFailure(ctx, "TMDB movie search failed")
				failed.Diagnostics = appendBounded(diagnostics, fmt.Sprintf("TMDB hypothesis %q: ERROR", safeDiagnosticValue(query)))
				return failed
			}
			searchFailed = true
			diagnostics = appendBounded(diagnostics, fmt.Sprintf("TMDB hypothesis %q: ERROR", safeDiagnosticValue(query)))
			continue
		}
		searchSucceeded = true
		diagnostics = appendBounded(diagnostics, movieSearchDiagnostic(query, rows))
		for _, row := range rows {
			if row.ID > 0 {
				byID[row.ID] = row
			}
		}
	}
	if len(byID) == 0 {
		if searchFailed {
			failed := operationalFailure(ctx, "TMDB movie search failed")
			failed.Diagnostics = diagnostics
			return failed
		}
		return ensemble.ResolverResult{Name: source, Status: ensemble.ResolverAbstain, Diagnostics: diagnostics}
	}

	ids := sortedIDs(byID)
	result := ensemble.ResolverResult{Name: source, Status: ensemble.ResolverOK, Diagnostics: diagnostics}
	if searchFailed && searchSucceeded {
		result.Warnings = append(result.Warnings, "TMDB movie search partially failed")
	}
	for _, id := range ids {
		row := byID[id]
		items := titleEvidence(evidence, row.OriginalTitle, row.Title)
		candidateYear := yearOf(row.ReleaseDate)
		if evidence.Year > 0 && candidateYear > 0 {
			switch distance(evidence.Year, candidateYear) {
			case 0:
				items = append(items, ev(ensemble.FamilyTime, ensemble.EvidenceYearPrimaryExact, ensemble.PointsYearPrimaryExact, "primary release year matches"))
			case 1:
				if matched, _ := r.movieReleaseYearMatches(ctx, id, evidence.Year, &result); matched {
					items = append(items, ev(ensemble.FamilyTime, ensemble.EvidenceYearReleaseDateExact, ensemble.PointsYearReleaseDateExact, "release date matches source year"))
				} else {
					items = append(items, ev(ensemble.FamilyTime, ensemble.EvidenceYearNearPlausible, ensemble.PointsYearNearPlausible, "nearby release year"))
				}
			default:
				if matched, known := r.movieReleaseYearMatches(ctx, id, evidence.Year, &result); matched {
					items = append(items, ev(ensemble.FamilyTime, ensemble.EvidenceYearReleaseDateExact, ensemble.PointsYearReleaseDateExact, "release date matches source year"))
				} else if known {
					items = append(items, ev(ensemble.FamilyTime, ensemble.EvidenceYearClearMismatch, ensemble.PointsYearClearMismatch, "release year conflicts with source year"))
				}
			}
		}
		result.Candidates = append(result.Candidates, ensemble.Candidate{Identity: ensemble.EntityIdentity{Kind: model.KindMovie, TMDBID: id, Title: row.Title, Year: candidateYear}, Evidence: dedupeEvidence(items)})
	}
	return result
}

func (r *Resolver) movieReleaseYearMatches(ctx context.Context, id, year int, result *ensemble.ResolverResult) (bool, bool) {
	dates, err := r.metadata.GetMovieReleaseDates(ctx, id)
	if err != nil {
		result.Warnings = appendBounded(result.Warnings, "TMDB release-date enrichment failed")
		return false, false
	}
	for _, country := range dates.Results {
		for _, date := range country.ReleaseDates {
			if date.Type >= 1 && date.Type <= 6 && yearOf(date.ReleaseDate) == year {
				return true, true
			}
		}
	}
	return false, true
}

func (r *Resolver) resolveTV(ctx context.Context, req ensemble.ResolveRequest) ensemble.ResolverResult {
	evidence := requestEvidence(req)
	queries := titleQueries(req, evidence)
	if len(queries) == 0 {
		return ensemble.ResolverResult{Name: source, Status: ensemble.ResolverAbstain}
	}
	byID := make(map[int]model.TVCandidate)
	searchSucceeded := false
	searchFailed := false
	var diagnostics []string
	for _, query := range queries {
		rows, err := r.metadata.SearchTV(ctx, query)
		if err != nil {
			if ctx.Err() != nil {
				failed := operationalFailure(ctx, "TMDB TV search failed")
				failed.Diagnostics = appendBounded(diagnostics, fmt.Sprintf("TMDB hypothesis %q: ERROR", safeDiagnosticValue(query)))
				return failed
			}
			searchFailed = true
			diagnostics = appendBounded(diagnostics, fmt.Sprintf("TMDB hypothesis %q: ERROR", safeDiagnosticValue(query)))
			continue
		}
		searchSucceeded = true
		diagnostics = appendBounded(diagnostics, tvSearchDiagnostic(query, rows))
		for _, row := range rows {
			if row.ID > 0 {
				byID[row.ID] = row
			}
		}
	}
	if len(byID) == 0 {
		if searchFailed {
			failed := operationalFailure(ctx, "TMDB TV search failed")
			failed.Diagnostics = diagnostics
			return failed
		}
		return ensemble.ResolverResult{Name: source, Status: ensemble.ResolverAbstain, Diagnostics: diagnostics}
	}

	ids := sortedIDs(byID)
	result := ensemble.ResolverResult{Name: source, Status: ensemble.ResolverOK, Diagnostics: diagnostics}
	if searchFailed && searchSucceeded {
		result.Warnings = append(result.Warnings, "TMDB TV search partially failed")
	}
	for _, id := range ids {
		row := byID[id]
		items := titleEvidence(evidence, row.OriginalName, row.Name)
		candidateYear := yearOf(row.FirstAirDate)
		if evidence.Year > 0 && candidateYear > 0 {
			switch distance(evidence.Year, candidateYear) {
			case 0:
				items = append(items, ev(ensemble.FamilyTime, ensemble.EvidenceYearPrimaryExact, ensemble.PointsYearPrimaryExact, "first-air year matches"))
			case 1:
				items = append(items, ev(ensemble.FamilyTime, ensemble.EvidenceYearNearPlausible, ensemble.PointsYearNearPlausible, "nearby first-air year"))
			default:
				items = append(items, ev(ensemble.FamilyTime, ensemble.EvidenceYearClearMismatch, ensemble.PointsYearClearMismatch, "first-air year conflicts with source year"))
			}
		}
		items = append(items, r.episodeEvidence(ctx, id, evidence.Episodes, &result)...)
		result.Candidates = append(result.Candidates, ensemble.Candidate{Identity: ensemble.EntityIdentity{Kind: req.Kind, TMDBID: id, Title: row.Name, Year: candidateYear}, Evidence: dedupeEvidence(items)})
	}
	return result
}

func (r *Resolver) episodeEvidence(ctx context.Context, id int, refs []model.EpisodeRef, result *ensemble.ResolverResult) []ensemble.Evidence {
	bySeason := make(map[int][]int)
	for _, ref := range refs {
		if ref.Season >= 0 && ref.Episode > 0 {
			bySeason[ref.Season] = append(bySeason[ref.Season], ref.Episode)
		}
	}
	seasons := make([]int, 0, len(bySeason))
	for season := range bySeason {
		seasons = append(seasons, season)
	}
	sort.Ints(seasons)
	var items []ensemble.Evidence
	for _, seasonNumber := range seasons {
		season, err := r.metadata.GetSeason(ctx, id, seasonNumber)
		if err != nil {
			result.Warnings = appendBounded(result.Warnings, "TMDB season enrichment failed")
			continue
		}
		items = append(items, ev(ensemble.FamilyEpisode, ensemble.EvidenceSeasonExists, ensemble.PointsSeasonExists, "parsed season exists"))
		exists := make(map[int]bool)
		for _, episode := range season.Episodes {
			exists[episode.EpisodeNumber] = true
		}
		allExist := true
		anyExists := false
		for _, episode := range bySeason[seasonNumber] {
			if exists[episode] {
				anyExists = true
			} else {
				allExist = false
			}
		}
		if anyExists {
			items = append(items, ev(ensemble.FamilyEpisode, ensemble.EvidenceEpisodeSXXEXXExists, ensemble.PointsEpisodeSXXEXXExists, "parsed episode exists"))
		}
		if allExist && len(bySeason[seasonNumber]) > 1 {
			items = append(items, ev(ensemble.FamilyEpisode, ensemble.EvidenceEpisodePackConsistent, ensemble.PointsEpisodePackConsistent, "episode pack is consistent"))
		}
	}
	return items
}

func requestEvidence(req ensemble.ResolveRequest) model.Evidence {
	evidence := req.ParsedEvidence
	if evidence.Year == 0 {
		evidence.Year = req.Year
	}
	if len(evidence.Titles) == 0 && strings.TrimSpace(req.Title) != "" {
		evidence.Titles = []model.WeightedTitle{{Title: req.Title, Weight: 1}}
	}
	for _, title := range req.TitleHypotheses {
		evidence.Titles = append(evidence.Titles, model.WeightedTitle{Title: title, Weight: 1})
	}
	return evidence
}

func titleQueries(req ensemble.ResolveRequest, evidence model.Evidence) []string {
	seen := make(map[string]bool)
	var queries []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		key := release.NormalizeTitle(value)
		if value == "" || key == "" || seen[key] || len(queries) >= 5 {
			return
		}
		seen[key] = true
		queries = append(queries, value)
	}
	add(req.Title)
	for _, title := range req.TitleHypotheses {
		add(title)
	}
	for _, title := range evidence.Titles {
		add(title.Title)
	}
	return queries
}

func titleEvidence(input model.Evidence, canonical, localized string) []ensemble.Evidence {
	canonicalNormalized := release.NormalizeTitle(canonical)
	localizedNormalized := release.NormalizeTitle(localized)
	for _, title := range input.Titles {
		normalized := release.NormalizeTitle(title.Title)
		if normalized != "" && normalized == canonicalNormalized {
			return []ensemble.Evidence{ev(ensemble.FamilyTitle, ensemble.EvidenceTitleExactCanonical, ensemble.PointsTitleExactCanonical, "canonical title matches")}
		}
	}
	if localizedNormalized != "" && localizedNormalized != canonicalNormalized {
		for _, title := range input.Titles {
			if release.NormalizeTitle(title.Title) == localizedNormalized {
				return []ensemble.Evidence{ev(ensemble.FamilyTitle, ensemble.EvidenceTitleExactLocalized, ensemble.PointsTitleExactLocalized, "localized title matches")}
			}
		}
	}
	score := matcher.TitleSimilarity(input, canonical, localized)
	if score >= 30 {
		return []ensemble.Evidence{ev(ensemble.FamilyTitle, ensemble.EvidenceTitleFuzzyStrong, ensemble.PointsTitleFuzzyStrong, "strong normalized title overlap")}
	}
	if score > 0 {
		return []ensemble.Evidence{ev(ensemble.FamilyTitle, ensemble.EvidenceTitleFuzzyWeak, ensemble.PointsTitleFuzzyWeak, "weak normalized title overlap")}
	}
	return nil
}

func ev(family ensemble.EvidenceFamily, typ ensemble.EvidenceType, points int, details string) ensemble.Evidence {
	return ensemble.Evidence{Family: family, Type: typ, Source: source, Points: points, SafeDetails: details}
}

func dedupeEvidence(items []ensemble.Evidence) []ensemble.Evidence {
	byType := make(map[ensemble.EvidenceType]ensemble.Evidence)
	for _, item := range items {
		current, ok := byType[item.Type]
		if !ok || item.Points > current.Points {
			byType[item.Type] = item
		}
	}
	keys := make([]string, 0, len(byType))
	for typ := range byType {
		keys = append(keys, string(typ))
	}
	sort.Strings(keys)
	result := make([]ensemble.Evidence, 0, len(keys))
	for _, key := range keys {
		result = append(result, byType[ensemble.EvidenceType(key)])
	}
	return result
}

func sortedIDs[T any](values map[int]T) []int {
	ids := make([]int, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func operationalFailure(ctx context.Context, message string) ensemble.ResolverResult {
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return failed(ensemble.ErrorTimeout, 0, message, true)
	case context.Canceled:
		return failed(ensemble.ErrorCanceled, 0, message, false)
	default:
		return failed(ensemble.ErrorProvider, 0, message, true)
	}
}

func failed(kind ensemble.OperationalErrorKind, status int, message string, retryable bool) ensemble.ResolverResult {
	return ensemble.ResolverResult{Name: source, Status: ensemble.ResolverError, Error: &ensemble.OperationalError{Kind: kind, StatusCode: status, Message: message, Retryable: retryable}}
}

func appendBounded(values []string, value string) []string {
	if len(values) >= 8 {
		return values
	}
	return append(values, value)
}

func movieSearchDiagnostic(query string, rows []model.MovieCandidate) string {
	query = safeDiagnosticValue(query)
	if len(rows) == 0 {
		return fmt.Sprintf("TMDB hypothesis %q: MISS", query)
	}
	first := rows[0]
	return fmt.Sprintf("TMDB hypothesis %q: HIT candidates=%d first=%q (%d) tmdb=%d", query, len(rows), safeDiagnosticValue(first.Title), yearOf(first.ReleaseDate), first.ID)
}

func tvSearchDiagnostic(query string, rows []model.TVCandidate) string {
	query = safeDiagnosticValue(query)
	if len(rows) == 0 {
		return fmt.Sprintf("TMDB hypothesis %q: MISS", query)
	}
	first := rows[0]
	return fmt.Sprintf("TMDB hypothesis %q: HIT candidates=%d first=%q (%d) tmdb=%d", query, len(rows), safeDiagnosticValue(first.Name), yearOf(first.FirstAirDate), first.ID)
}

func safeDiagnosticValue(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < ' ' || r == '\u007f' {
			return ' '
		}
		return r
	}, strings.TrimSpace(value))
	runes := []rune(value)
	if len(runes) > 120 {
		value = string(runes[:120]) + "…"
	}
	return value
}

func yearOf(value string) int {
	if len(value) < 4 {
		return 0
	}
	year, _ := strconv.Atoi(value[:4])
	return year
}

func distance(a, b int) int {
	if a < b {
		return b - a
	}
	return a - b
}
