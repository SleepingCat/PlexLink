package ensemble

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SleepingCat/PlexLink/internal/model"
)

type fakeResolver struct {
	name     string
	supports bool
	resolve  func(context.Context, ResolveRequest) ResolverResult
	calls    atomic.Int32
}

func (f *fakeResolver) Name() string             { return f.name }
func (f *fakeResolver) Supports(model.Kind) bool { return f.supports }
func (f *fakeResolver) Resolve(ctx context.Context, req ResolveRequest) ResolverResult {
	f.calls.Add(1)
	return f.resolve(ctx, req)
}

type passNormalizer struct{}

func (passNormalizer) Normalize(_ context.Context, candidate Candidate) ([]Candidate, *OperationalError) {
	return []Candidate{candidate}, nil
}

func winningResult(name string) ResolverResult {
	return ResolverResult{Name: name, Status: ResolverOK, Candidates: []Candidate{{Identity: EntityIdentity{Kind: model.KindMovie, TMDBID: 7}, Evidence: []Evidence{
		{Family: FamilyTitle, Type: EvidenceTitleExactCanonical, Source: name, Points: PointsTitleExactCanonical},
		{Family: FamilyTime, Type: EvidenceYearReleaseDateExact, Source: name, Points: PointsYearReleaseDateExact},
	}}}}
}

func TestExecuteStartsApplicableResolversConcurrentlyAndIgnoresFailure(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	makeResolver := func(name string, result ResolverResult) *fakeResolver {
		return &fakeResolver{name: name, supports: true, resolve: func(context.Context, ResolveRequest) ResolverResult {
			started <- name
			<-release
			return result
		}}
	}
	failed := ResolverResult{Name: "failed", Status: ResolverError, Error: &OperationalError{Kind: ErrorRateLimited, StatusCode: 429}}
	resolvers := []Resolver{makeResolver("winner", winningResult("winner")), makeResolver("failed", failed)}
	done := make(chan Run, 1)
	go func() {
		done <- Execute(context.Background(), time.Second, resolvers, ResolveRequest{Kind: model.KindMovie}, passNormalizer{})
	}()
	seen := map[string]bool{}
	for range resolvers {
		select {
		case name := <-started:
			seen[name] = true
		case <-time.After(time.Second):
			t.Fatal("resolvers did not start concurrently")
		}
	}
	close(release)
	run := <-done
	if len(seen) != 2 || run.Decision.Type != DecisionMatch || run.Decision.Candidates[0].TotalScore != 500 {
		t.Fatalf("seen=%v run=%+v", seen, run)
	}
}

func TestExecuteSkipsUnsupportedResolver(t *testing.T) {
	unsupported := &fakeResolver{name: "tvmaze", supports: false, resolve: func(context.Context, ResolveRequest) ResolverResult { return winningResult("tvmaze") }}
	run := Execute(context.Background(), time.Second, []Resolver{unsupported}, ResolveRequest{Kind: model.KindMovie}, passNormalizer{})
	if unsupported.calls.Load() != 0 || len(run.Results) != 0 || run.Decision.Type != DecisionNoEvidence {
		t.Fatalf("calls=%d run=%+v", unsupported.calls.Load(), run)
	}
}

func TestExecuteGlobalCancellationStopsResolver(t *testing.T) {
	resolver := &fakeResolver{name: "blocking", supports: true, resolve: func(ctx context.Context, _ ResolveRequest) ResolverResult {
		<-ctx.Done()
		return ResolverResult{Name: "blocking", Status: ResolverError, Error: &OperationalError{Kind: ErrorCanceled}}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := Execute(ctx, time.Second, []Resolver{resolver}, ResolveRequest{Kind: model.KindTV}, passNormalizer{})
	if len(run.Results) != 1 || run.Results[0].Status != ResolverError {
		t.Fatalf("run=%+v", run)
	}
}

func TestOptionalProviderFailuresAreNeutral(t *testing.T) {
	failures := []struct {
		name string
		err  *OperationalError
	}{
		{"timeout", &OperationalError{Kind: ErrorTimeout, Retryable: true}},
		{"unauthorized", &OperationalError{Kind: ErrorAuthentication, StatusCode: 401}},
		{"forbidden", &OperationalError{Kind: ErrorAuthentication, StatusCode: 403}},
		{"rate-limited", &OperationalError{Kind: ErrorRateLimited, StatusCode: 429, Retryable: true}},
		{"server-500", &OperationalError{Kind: ErrorProvider, StatusCode: 500, Retryable: true}},
		{"server-503", &OperationalError{Kind: ErrorProvider, StatusCode: 503, Retryable: true}},
		{"malformed", &OperationalError{Kind: ErrorInvalidResponse}},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			winner := &fakeResolver{name: "tmdb", supports: true, resolve: func(context.Context, ResolveRequest) ResolverResult { return winningResult("tmdb") }}
			failed := &fakeResolver{name: "optional", supports: true, resolve: func(context.Context, ResolveRequest) ResolverResult {
				return ResolverResult{Name: "optional", Status: ResolverError, Error: tc.err, Candidates: winningResult("optional").Candidates}
			}}
			withFailure := Execute(context.Background(), time.Second, []Resolver{failed, winner}, ResolveRequest{Kind: model.KindMovie}, passNormalizer{})
			withoutFailure := Execute(context.Background(), time.Second, []Resolver{winner}, ResolveRequest{Kind: model.KindMovie}, passNormalizer{})
			if withFailure.Decision.Type != DecisionMatch || !reflect.DeepEqual(withFailure.Decision, withoutFailure.Decision) {
				t.Fatalf("failure changed decision: with=%+v without=%+v", withFailure.Decision, withoutFailure.Decision)
			}
		})
	}
}

func TestMultipleOptionalFailuresLeaveInsufficientEvidenceUnresolved(t *testing.T) {
	weak := &fakeResolver{name: "tmdb", supports: true, resolve: func(context.Context, ResolveRequest) ResolverResult {
		return result(7, ev(FamilyTitle, EvidenceTitleFuzzyStrong, "tmdb", PointsTitleFuzzyStrong))
	}}
	failed := func(name string) *fakeResolver {
		return &fakeResolver{name: name, supports: true, resolve: func(context.Context, ResolveRequest) ResolverResult {
			return ResolverResult{Name: name, Status: ResolverError, Error: &OperationalError{Kind: ErrorProvider, StatusCode: 503, Retryable: true}}
		}}
	}
	run := Execute(context.Background(), time.Second, []Resolver{failed("opensubtitles"), failed("kinopoisk"), failed("tvmaze"), weak}, ResolveRequest{Kind: model.KindMovie}, passNormalizer{})
	if run.Decision.Type != DecisionAmbiguous || run.Decision.Candidates[0].TotalScore != PointsTitleFuzzyStrong {
		t.Fatalf("unsafe decision after outages: %+v", run.Decision)
	}
}
