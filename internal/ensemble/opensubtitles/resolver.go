package opensubtitles

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/SleepingCat/PlexLink/internal/ensemble"
	"github.com/SleepingCat/PlexLink/internal/model"
)

const resolverName = "opensubtitles"

type Resolver struct {
	client              *Client
	representativeFiles int
}

func New(client *Client, representativeFiles int) *Resolver {
	if representativeFiles <= 0 {
		representativeFiles = 3
	}
	return &Resolver{client: client, representativeFiles: representativeFiles}
}

func (r *Resolver) Name() string { return resolverName }
func (r *Resolver) Supports(kind model.Kind) bool {
	return kind == model.KindMovie || kind == model.KindTV || kind == model.KindAnime
}

func (r *Resolver) Resolve(ctx context.Context, req ensemble.ResolveRequest) ensemble.ResolverResult {
	result := ensemble.ResolverResult{Name: resolverName}
	if r == nil || r.client == nil {
		result.Status = ensemble.ResolverError
		result.Error = opError(ensemble.ErrorConfiguration, 0, "OpenSubtitles resolver is not configured", false)
		return result
	}
	files := representativeFiles(req.Kind, req.Files, r.representativeFiles)
	if len(files) == 0 {
		result.Status = ensemble.ResolverAbstain
		result.Diagnostics = []string{"no media files available for fingerprinting"}
		return result
	}

	seen := make(map[string]bool)
	var failures int
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			result.Status = ensemble.ResolverError
			result.Candidates = nil
			if err == context.DeadlineExceeded {
				result.Error = opError(ensemble.ErrorTimeout, 0, "OpenSubtitles resolution timed out", true)
			} else {
				result.Error = opError(ensemble.ErrorCanceled, 0, "OpenSubtitles resolution canceled", false)
			}
			return result
		}
		hash, size, err := MovieHash(file.Source)
		if err != nil {
			failures++
			result.Warnings = append(result.Warnings, "representative file could not be fingerprinted")
			continue
		}
		matches, opErr := r.client.Search(ctx, hash, size)
		if opErr != nil {
			if ctx.Err() != nil {
				result.Status = ensemble.ResolverError
				result.Candidates = nil
				result.Error = opErr
				return result
			}
			failures++
			if len(result.Candidates) == 0 && failures == len(files) {
				result.Status = ensemble.ResolverError
				result.Error = opErr
				return result
			}
			result.Warnings = append(result.Warnings, opErr.Message)
			continue
		}
		for _, match := range matches {
			candidate, ok := candidateFromMatch(req.Kind, match, file)
			if !ok {
				continue
			}
			key := identityKey(candidate.Identity)
			if seen[key] {
				continue
			}
			seen[key] = true
			result.Candidates = append(result.Candidates, candidate)
		}
	}

	if len(result.Candidates) > 0 {
		result.Status = ensemble.ResolverOK
		sort.SliceStable(result.Candidates, func(i, j int) bool {
			return identityKey(result.Candidates[i].Identity) < identityKey(result.Candidates[j].Identity)
		})
		return result
	}
	if failures > 0 {
		result.Status = ensemble.ResolverError
		result.Error = opError(ensemble.ErrorProvider, 0, "OpenSubtitles resolution failed for all representative files", true)
		return result
	}
	result.Status = ensemble.ResolverAbstain
	result.Diagnostics = []string{"OpenSubtitles returned no useful exact fingerprint match"}
	return result
}

func representativeFiles(kind model.Kind, files []model.MediaFile, maximum int) []model.MediaFile {
	if len(files) == 0 {
		return nil
	}
	ordered := append([]model.MediaFile(nil), files...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i].Source, ordered[j].Source
		if a == "" {
			a = ordered[i].Name
		}
		if b == "" {
			b = ordered[j].Name
		}
		return a < b
	})
	if kind == model.KindMovie {
		return ordered[:1]
	}
	if maximum <= 0 {
		maximum = 3
	}
	if len(ordered) <= maximum {
		return ordered
	}
	indices := make([]int, 0, maximum)
	if maximum == 1 {
		indices = append(indices, 0)
	} else {
		for i := 0; i < maximum; i++ {
			indices = append(indices, i*(len(ordered)-1)/(maximum-1))
		}
	}
	out := make([]model.MediaFile, 0, maximum)
	last := -1
	for _, i := range indices {
		if i != last {
			out = append(out, ordered[i])
			last = i
		}
	}
	return out
}

func candidateFromMatch(kind model.Kind, match SearchResult, file model.MediaFile) (ensemble.Candidate, bool) {
	if !match.MovieHashMatch {
		return ensemble.Candidate{}, false
	}
	f := match.Feature
	id := ensemble.EntityIdentity{Kind: kind, TMDBID: f.TMDBID, IMDbID: f.IMDbID, Title: f.Title, Year: f.Year}
	if kind != model.KindMovie && (f.ParentTMDBID != 0 || f.ParentIMDbID != "" || f.ParentTitle != "") {
		id.TMDBID, id.IMDbID, id.Title = f.ParentTMDBID, f.ParentIMDbID, f.ParentTitle
	}
	if id.TMDBID == 0 && id.IMDbID == "" && strings.TrimSpace(id.Title) == "" {
		return ensemble.Candidate{}, false
	}
	detail := "exact OpenSubtitles file fingerprint"
	if f.Season > 0 || f.Episode > 0 {
		detail = fmt.Sprintf("exact fingerprint for S%02dE%02d", f.Season, f.Episode)
	}
	evidence := []ensemble.Evidence{{Family: ensemble.FamilyFileIdentity, Type: ensemble.EvidenceOpenSubtitlesHashExact, Source: resolverName, Points: ensemble.PointsOpenSubtitlesHashExact, SafeDetails: detail}}
	return ensemble.Candidate{Identity: id, Evidence: evidence}, true
}

func identityKey(id ensemble.EntityIdentity) string {
	return fmt.Sprintf("%s|%d|%s|%s|%d", id.Kind, id.TMDBID, id.IMDbID, strings.ToLower(strings.TrimSpace(id.Title)), id.Year)
}
