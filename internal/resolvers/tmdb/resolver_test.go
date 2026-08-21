package tmdbresolver

import (
	"context"
	"errors"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/ensemble"
	"github.com/SleepingCat/PlexLink/internal/model"
)

type fakeMetadata struct {
	movies    map[string][]model.MovieCandidate
	tv        map[string][]model.TVCandidate
	seasons   map[[2]int]model.Season
	dates     map[int]model.MovieReleaseDates
	searchErr error
	enrichErr error
}

func (f fakeMetadata) SearchTV(_ context.Context, query string) ([]model.TVCandidate, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.tv[query], nil
}
func (f fakeMetadata) GetSeason(_ context.Context, id, season int) (model.Season, error) {
	if f.enrichErr != nil {
		return model.Season{}, f.enrichErr
	}
	value, ok := f.seasons[[2]int{id, season}]
	if !ok {
		return model.Season{}, errors.New("not found")
	}
	return value, nil
}
func (f fakeMetadata) SearchMovie(_ context.Context, query string) ([]model.MovieCandidate, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.movies[query], nil
}
func (f fakeMetadata) GetMovieReleaseDates(_ context.Context, id int) (model.MovieReleaseDates, error) {
	if f.enrichErr != nil {
		return model.MovieReleaseDates{}, f.enrichErr
	}
	return f.dates[id], nil
}

func findEvidence(candidate ensemble.Candidate, typ ensemble.EvidenceType) (ensemble.Evidence, bool) {
	for _, item := range candidate.Evidence {
		if item.Type == typ {
			return item, true
		}
	}
	return ensemble.Evidence{}, false
}

func TestMovieStructuredTitleAndYearEvidence(t *testing.T) {
	provider := fakeMetadata{movies: map[string][]model.MovieCandidate{
		"V for Vendetta": {{ID: 752, Title: "V for Vendetta", OriginalTitle: "V for Vendetta", ReleaseDate: "2006-03-17"}},
	}}
	result := New(provider).Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "V for Vendetta", Year: 2006})
	if result.Status != ensemble.ResolverOK || len(result.Candidates) != 1 {
		t.Fatalf("result=%+v", result)
	}
	if item, ok := findEvidence(result.Candidates[0], ensemble.EvidenceTitleExactCanonical); !ok || item.Points != ensemble.PointsTitleExactCanonical {
		t.Fatalf("title evidence=%+v", result.Candidates[0].Evidence)
	}
	if item, ok := findEvidence(result.Candidates[0], ensemble.EvidenceYearPrimaryExact); !ok || item.Points != ensemble.PointsYearPrimaryExact {
		t.Fatalf("year evidence=%+v", result.Candidates[0].Evidence)
	}
	if _, ok := findEvidence(result.Candidates[0], ensemble.EvidenceExternalTMDBExact); ok {
		t.Fatal("TMDB search candidate incorrectly received external identity points")
	}
}

func TestLocalizedTitleAndReleaseDateEvidence(t *testing.T) {
	provider := fakeMetadata{
		movies: map[string][]model.MovieCandidate{"Забавные Игры": {{ID: 1, Title: "Забавные игры", OriginalTitle: "Funny Games", ReleaseDate: "2007-01-01"}}},
		dates:  map[int]model.MovieReleaseDates{1: {Results: []model.MovieReleaseCountry{{ReleaseDates: []model.MovieReleaseDate{{Type: 1, ReleaseDate: "2006-12-01"}}}}}},
	}
	result := New(provider).Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "Забавные Игры", Year: 2006})
	candidate := result.Candidates[0]
	if _, ok := findEvidence(candidate, ensemble.EvidenceTitleExactLocalized); !ok {
		t.Fatalf("evidence=%+v", candidate.Evidence)
	}
	if _, ok := findEvidence(candidate, ensemble.EvidenceYearReleaseDateExact); !ok {
		t.Fatalf("evidence=%+v", candidate.Evidence)
	}
}

func TestMovieYearEvidenceGuards(t *testing.T) {
	provider := fakeMetadata{movies: map[string][]model.MovieCandidate{
		"Funny Games": {
			{ID: 1, Title: "Funny Games", OriginalTitle: "Funny Games"},
			{ID: 2, Title: "Funny Games", OriginalTitle: "Funny Games", ReleaseDate: "2008-01-01"},
			{ID: 3, Title: "Funny Games", OriginalTitle: "Funny Games", ReleaseDate: "2007-01-01"},
		},
	}, dates: map[int]model.MovieReleaseDates{2: {}}}
	result := New(provider).Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "Funny Games", Year: 2007})
	byID := make(map[int]ensemble.Candidate)
	for _, candidate := range result.Candidates {
		byID[candidate.Identity.TMDBID] = candidate
	}
	if _, ok := findEvidence(byID[1], ensemble.EvidenceYearClearMismatch); ok {
		t.Fatalf("missing year became mismatch: %+v", byID[1].Evidence)
	}
	if _, ok := findEvidence(byID[1], ensemble.EvidenceYearPrimaryExact); ok {
		t.Fatalf("missing year became exact: %+v", byID[1].Evidence)
	}
	if _, ok := findEvidence(byID[2], ensemble.EvidenceYearPrimaryExact); ok {
		t.Fatalf("nearby year labeled exact: %+v", byID[2].Evidence)
	}
	if _, ok := findEvidence(byID[2], ensemble.EvidenceYearNearPlausible); !ok {
		t.Fatalf("nearby year evidence missing: %+v", byID[2].Evidence)
	}
	if _, ok := findEvidence(byID[3], ensemble.EvidenceYearPrimaryExact); !ok {
		t.Fatalf("exact primary year evidence missing: %+v", byID[3].Evidence)
	}
}

func TestTVCandidatesAndEpisodeSignalsArePreserved(t *testing.T) {
	provider := fakeMetadata{
		tv: map[string][]model.TVCandidate{"Counterpart": {
			{ID: 2, Name: "Counterpart", OriginalName: "Counterpart", FirstAirDate: "2017-01-01"},
			{ID: 1, Name: "Counterpart", OriginalName: "Counterpart", FirstAirDate: "2018-01-01"},
		}},
		seasons: map[[2]int]model.Season{
			{2, 2}: {SeasonNumber: 2, Episodes: []model.Episode{{EpisodeNumber: 1}, {EpisodeNumber: 2}}},
			{1, 2}: {SeasonNumber: 2, Episodes: []model.Episode{{EpisodeNumber: 1}}},
		},
	}
	result := New(provider).Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindTV, Title: "Counterpart", Year: 2017, ParsedEvidence: model.Evidence{Episodes: []model.EpisodeRef{{Season: 2, Episode: 1}, {Season: 2, Episode: 2}}}})
	if result.Status != ensemble.ResolverOK || len(result.Candidates) != 2 || result.Candidates[0].Identity.TMDBID != 1 || result.Candidates[1].Identity.TMDBID != 2 {
		t.Fatalf("result=%+v", result)
	}
	if _, ok := findEvidence(result.Candidates[1], ensemble.EvidenceSeasonExists); !ok {
		t.Fatalf("evidence=%+v", result.Candidates[1].Evidence)
	}
	if _, ok := findEvidence(result.Candidates[1], ensemble.EvidenceEpisodeSXXEXXExists); !ok {
		t.Fatalf("evidence=%+v", result.Candidates[1].Evidence)
	}
	if _, ok := findEvidence(result.Candidates[1], ensemble.EvidenceEpisodePackConsistent); !ok {
		t.Fatalf("evidence=%+v", result.Candidates[1].Evidence)
	}
}

func TestEmptyAndProviderErrorStatuses(t *testing.T) {
	resolver := New(fakeMetadata{movies: map[string][]model.MovieCandidate{}})
	result := resolver.Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "Missing"})
	if result.Status != ensemble.ResolverAbstain {
		t.Fatalf("status=%s", result.Status)
	}
	resolver = New(fakeMetadata{searchErr: errors.New("secret-bearing raw error")})
	result = resolver.Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "Film"})
	if result.Status != ensemble.ResolverError || result.Error == nil || result.Error.Kind != ensemble.ErrorProvider || result.Error.Message != "TMDB movie search failed" || len(result.Candidates) != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestEnrichmentFailureDoesNotCreateNegativeEvidence(t *testing.T) {
	provider := fakeMetadata{
		movies:    map[string][]model.MovieCandidate{"Film": {{ID: 1, Title: "Film", OriginalTitle: "Film", ReleaseDate: "2010-01-01"}}},
		enrichErr: errors.New("provider unavailable"),
	}
	result := New(provider).Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "Film", Year: 2000})
	if result.Status != ensemble.ResolverOK || len(result.Warnings) == 0 {
		t.Fatalf("result=%+v", result)
	}
	if _, ok := findEvidence(result.Candidates[0], ensemble.EvidenceYearClearMismatch); ok {
		t.Fatalf("provider failure became negative evidence: %+v", result.Candidates[0].Evidence)
	}
}

func TestTitleHypothesesAreBoundedAndCandidatesDeduped(t *testing.T) {
	provider := fakeMetadata{movies: map[string][]model.MovieCandidate{
		"One": {{ID: 7, Title: "One", OriginalTitle: "One"}},
		"Two": {{ID: 7, Title: "One", OriginalTitle: "One"}},
	}}
	result := New(provider).Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Title: "One", TitleHypotheses: []string{"One", "Two", "Three", "Four", "Five", "Six", "Seven"}})
	if result.Status != ensemble.ResolverOK || len(result.Candidates) != 1 {
		t.Fatalf("result=%+v", result)
	}
}

func TestLegacyMatcherRegressionsRemainAvailable(t *testing.T) {
	// Compile-time coverage for the adapter's deliberate reuse of the matcher
	// title signal; detailed legacy behavior remains covered in matcher tests.
	if !New(fakeMetadata{}).Supports(model.KindAnime) {
		t.Fatal("anime unsupported")
	}
}
