package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/SleepingCat/PlexLink/internal/ensemble"
	"github.com/SleepingCat/PlexLink/internal/model"
	"github.com/SleepingCat/PlexLink/internal/release"
)

type externalFinder interface {
	FindByIMDb(context.Context, string) (model.ExternalFindResult, error)
}

type tmdbNormalizer struct{ metadata MetadataProvider }

func (n tmdbNormalizer) Normalize(ctx context.Context, candidate ensemble.Candidate) ([]ensemble.Candidate, *ensemble.OperationalError) {
	if candidate.Identity.TMDBID > 0 {
		mapped, err := n.verify(ctx, candidate, candidate.Identity.TMDBID)
		if err != nil {
			return nil, normalizeError(ctx, "TMDB identity verification failed")
		}
		return []ensemble.Candidate{mapped}, nil
	}
	if candidate.Identity.IMDbID != "" {
		finder, ok := n.metadata.(externalFinder)
		if ok {
			found, err := finder.FindByIMDb(ctx, candidate.Identity.IMDbID)
			if err != nil {
				return nil, normalizeError(ctx, "TMDB IMDb lookup failed")
			}
			ids := externalIDs(candidate.Identity.Kind, found)
			var mapped []ensemble.Candidate
			for _, id := range ids {
				verified, err := n.verify(ctx, candidate, id)
				if err != nil {
					continue
				}
				mapped = append(mapped, verified)
			}
			if len(mapped) > 0 {
				return mapped, nil
			}
		}
	}
	if strings.TrimSpace(candidate.Identity.Title) == "" {
		return nil, nil
	}
	return n.normalizeTitle(ctx, candidate)
}

func (n tmdbNormalizer) normalizeTitle(ctx context.Context, candidate ensemble.Candidate) ([]ensemble.Candidate, *ensemble.OperationalError) {
	wanted := release.NormalizeTitle(candidate.Identity.Title)
	var mapped []ensemble.Candidate
	if candidate.Identity.Kind == model.KindMovie {
		rows, err := n.metadata.SearchMovie(ctx, candidate.Identity.Title)
		if err != nil {
			return nil, normalizeError(ctx, "TMDB title normalization failed")
		}
		for _, row := range rows {
			if !titleAndYearBridge(wanted, candidate.Identity.Year, year(row.ReleaseDate), row.Title, row.OriginalTitle) {
				continue
			}
			verified, err := n.verify(ctx, candidate, row.ID)
			if err == nil {
				mapped = append(mapped, verified)
			}
		}
	} else {
		rows, err := n.metadata.SearchTV(ctx, candidate.Identity.Title)
		if err != nil {
			return nil, normalizeError(ctx, "TMDB title normalization failed")
		}
		for _, row := range rows {
			if !titleAndYearBridge(wanted, candidate.Identity.Year, year(row.FirstAirDate), row.Name, row.OriginalName) {
				continue
			}
			verified, err := n.verify(ctx, candidate, row.ID)
			if err == nil {
				mapped = append(mapped, verified)
			}
		}
	}
	sort.SliceStable(mapped, func(i, j int) bool { return mapped[i].Identity.TMDBID < mapped[j].Identity.TMDBID })
	return mapped, nil
}

func (n tmdbNormalizer) verify(ctx context.Context, candidate ensemble.Candidate, id int) (ensemble.Candidate, error) {
	if candidate.Identity.Kind == model.KindMovie {
		movie, err := n.metadata.GetMovie(ctx, id)
		if err != nil || movie.ID != id {
			return ensemble.Candidate{}, fmt.Errorf("movie identity not verified")
		}
		candidate.Identity.TMDBID, candidate.Identity.Title, candidate.Identity.Year = id, movie.Title, year(movie.ReleaseDate)
		return candidate, nil
	}
	show, err := n.metadata.GetTV(ctx, id)
	if err != nil || show.ID != id {
		return ensemble.Candidate{}, fmt.Errorf("show identity not verified")
	}
	candidate.Identity.TMDBID, candidate.Identity.Title, candidate.Identity.Year = id, show.Name, year(show.FirstAirDate)
	return candidate, nil
}

func externalIDs(kind model.Kind, found model.ExternalFindResult) []int {
	seen := make(map[int]bool)
	var ids []int
	if kind == model.KindMovie {
		for _, row := range found.MovieResults {
			if row.ID > 0 && !seen[row.ID] {
				seen[row.ID] = true
				ids = append(ids, row.ID)
			}
		}
	} else {
		for _, row := range found.TVResults {
			if row.ID > 0 && !seen[row.ID] {
				seen[row.ID] = true
				ids = append(ids, row.ID)
			}
		}
	}
	sort.Ints(ids)
	return ids
}

func titleAndYearBridge(wanted string, sourceYear, candidateYear int, names ...string) bool {
	if sourceYear > 0 && candidateYear > 0 && abs(sourceYear-candidateYear) > 1 {
		return false
	}
	for _, name := range names {
		if release.NormalizeTitle(name) == wanted {
			return true
		}
	}
	return false
}

func normalizeError(ctx context.Context, message string) *ensemble.OperationalError {
	if ctx.Err() == context.Canceled {
		return &ensemble.OperationalError{Kind: ensemble.ErrorCanceled, Message: message}
	}
	if ctx.Err() == context.DeadlineExceeded {
		return &ensemble.OperationalError{Kind: ensemble.ErrorTimeout, Message: message, Retryable: true}
	}
	return &ensemble.OperationalError{Kind: ensemble.ErrorProvider, Message: message, Retryable: true}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
