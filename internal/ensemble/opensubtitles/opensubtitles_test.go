package opensubtitles

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/ensemble"
	"github.com/SleepingCat/PlexLink/internal/model"
)

func mediaFile(t *testing.T, dir, name string, size int) model.MediaFile {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return model.MediaFile{Source: path, Name: name}
}

func TestMovieHashKnownFixture(t *testing.T) {
	f := mediaFile(t, t.TempDir(), "zero.mkv", 2*int(hashChunkSize))
	hash, size, err := MovieHash(f.Source)
	if err != nil {
		t.Fatal(err)
	}
	if hash != "0000000000020000" || size != 131072 {
		t.Fatalf("hash=%s size=%d", hash, size)
	}
}

func TestMovieHashTooSmall(t *testing.T) {
	f := mediaFile(t, t.TempDir(), "small.mkv", 100)
	_, _, err := MovieHash(f.Source)
	var hashErr *HashError
	if !errors.As(err, &hashErr) || !errors.Is(err, ErrFileTooSmall) {
		t.Fatalf("error=%v", err)
	}
}

func TestClientSendsFingerprintAndRequiredHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/subtitles" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.URL.Query().Get("moviehash") != "abcd" || r.URL.Query().Get("moviebytesize") != "123" {
			t.Errorf("query=%s", r.URL.RawQuery)
		}
		if r.Header.Get("Api-Key") != "secret" || r.Header.Get("User-Agent") != "PlexLink/tests" {
			t.Errorf("headers=%v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	client := NewClient(server.URL+"/api/v1", "secret", "PlexLink/tests", server.Client())
	if _, err := client.Search(context.Background(), "abcd", 123); err != nil {
		t.Fatal(err)
	}
}

func TestExactResultsExtractIDsDeduplicateAndPreserveConflict(t *testing.T) {
	body := `{"data":[
		{"attributes":{"moviehash_match":true,"feature_details":{"feature_type":"Movie","title":"One","year":2001,"tmdb_id":11,"imdb_id":123}}},
		{"attributes":{"moviehash_match":true,"feature_details":{"feature_type":"Movie","title":"One","year":2001,"tmdb_id":11,"imdb_id":"tt123"}}},
		{"attributes":{"moviehash_match":true,"feature_details":{"feature_type":"Movie","title":"Other","year":2002,"tmdb_id":22,"imdb_id":456}}},
		{"attributes":{"moviehash_match":false,"feature_details":{"title":"Not exact","tmdb_id":33}}}
	]}`
	r, _ := testResolver(t, body, http.StatusOK)
	file := mediaFile(t, t.TempDir(), "movie.mkv", 2*int(hashChunkSize))
	got := r.Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Files: []model.MediaFile{file}})
	if got.Status != ensemble.ResolverOK || len(got.Candidates) != 2 {
		t.Fatalf("result=%+v", got)
	}
	if got.Candidates[0].Identity.TMDBID != 11 || got.Candidates[0].Identity.IMDbID != "tt123" {
		t.Fatalf("identity=%+v", got.Candidates[0].Identity)
	}
	if len(got.Candidates[0].Evidence) != 1 {
		t.Fatalf("evidence=%+v", got.Candidates[0].Evidence)
	}
	if got.Candidates[0].Evidence[0].Type != ensemble.EvidenceOpenSubtitlesHashExact || got.Candidates[0].Evidence[0].Points != 1000 {
		t.Fatalf("evidence=%+v", got.Candidates[0].Evidence)
	}
	if got.Candidates[1].Identity.TMDBID != 22 {
		t.Fatalf("conflicting candidate lost: %+v", got.Candidates)
	}
	decision := ensemble.Aggregate([]ensemble.ResolverResult{got})
	for _, candidate := range decision.Candidates {
		if candidate.FamilyScores[ensemble.FamilyFileIdentity] != ensemble.PointsOpenSubtitlesHashExact || candidate.FamilyScores[ensemble.FamilyExternalIdentity] != 0 || candidate.IdentityAnchors != 1 {
			t.Fatalf("fingerprint identity was double-counted: %+v", candidate)
		}
	}
}

func TestTVUsesParentIdentity(t *testing.T) {
	body := `{"data":[{"attributes":{"moviehash_match":true,"feature_details":{"feature_type":"Episode","title":"Pilot","tmdb_id":999,"imdb_id":888,"parent_title":"Show","parent_tmdb_id":42,"parent_imdb_id":777,"season_number":1,"episode_number":2}}}]}`
	r, _ := testResolver(t, body, http.StatusOK)
	file := mediaFile(t, t.TempDir(), "episode.mkv", 2*int(hashChunkSize))
	got := r.Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindTV, Files: []model.MediaFile{file}})
	id := got.Candidates[0].Identity
	if id.TMDBID != 42 || id.IMDbID != "tt777" || id.Title != "Show" {
		t.Fatalf("identity=%+v", id)
	}
}

func TestNoUsefulMatchAbstains(t *testing.T) {
	r, _ := testResolver(t, `{"data":[]}`, http.StatusOK)
	file := mediaFile(t, t.TempDir(), "movie.mkv", 2*int(hashChunkSize))
	got := r.Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindMovie, Files: []model.MediaFile{file}})
	if got.Status != ensemble.ResolverAbstain || got.Error != nil {
		t.Fatalf("result=%+v", got)
	}
}

func TestRepresentativeSelectionDeterministicFirstMiddleLast(t *testing.T) {
	files := []model.MediaFile{{Source: "/z"}, {Source: "/b"}, {Source: "/m"}, {Source: "/a"}, {Source: "/x"}}
	got := representativeFiles(model.KindTV, files, 3)
	paths := []string{got[0].Source, got[1].Source, got[2].Source}
	if !reflect.DeepEqual(paths, []string{"/a", "/m", "/z"}) {
		t.Fatalf("paths=%v", paths)
	}
	if got := representativeFiles(model.KindMovie, files, 3); len(got) != 1 {
		t.Fatalf("movie representatives=%d", len(got))
	}
}

func TestPartialRepresentativeFailurePreservesEvidence(t *testing.T) {
	r, calls := testResolver(t, `{"data":[{"attributes":{"moviehash_match":true,"feature_details":{"title":"Show","parent_tmdb_id":42}}}]}`, http.StatusOK)
	dir := t.TempDir()
	files := []model.MediaFile{mediaFile(t, dir, "a-small.mkv", 10), mediaFile(t, dir, "b-good.mkv", 2*int(hashChunkSize))}
	got := r.Resolve(context.Background(), ensemble.ResolveRequest{Kind: model.KindTV, Files: files})
	if got.Status != ensemble.ResolverOK || len(got.Candidates) != 1 || len(got.Warnings) == 0 || *calls != 1 {
		t.Fatalf("result=%+v calls=%d", got, *calls)
	}
}

func TestHTTPFailuresAreTypedAndSafe(t *testing.T) {
	tests := []struct {
		status int
		kind   ensemble.OperationalErrorKind
	}{
		{401, ensemble.ErrorAuthentication}, {403, ensemble.ErrorAuthentication}, {429, ensemble.ErrorRateLimited}, {500, ensemble.ErrorProvider},
	}
	for _, tc := range tests {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			client, _ := testClient(t, `{"secret":"must not leak"}`, tc.status)
			_, got := client.Search(context.Background(), "abcd", 123)
			if got == nil || got.Kind != tc.kind || got.StatusCode != tc.status || strings.Contains(got.Message, "secret") {
				t.Fatalf("error=%+v", got)
			}
		})
	}
}

func TestInvalidSchemaAndCancellation(t *testing.T) {
	client, _ := testClient(t, `{`, http.StatusOK)
	if _, got := client.Search(context.Background(), "abcd", 123); got == nil || got.Kind != ensemble.ErrorInvalidResponse {
		t.Fatalf("error=%+v", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client, _ = testClient(t, `{"data":[]}`, http.StatusOK)
	if _, got := client.Search(ctx, "abcd", 123); got == nil || got.Kind != ensemble.ErrorCanceled {
		t.Fatalf("error=%+v", got)
	}
}

func testResolver(t *testing.T, body string, status int) (*Resolver, *int) {
	t.Helper()
	client, calls := testClient(t, body, status)
	return New(client, 3), calls
}

func testClient(t *testing.T, body string, status int) (*Client, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return NewClient(server.URL, "key", "test", server.Client()), &calls
}
