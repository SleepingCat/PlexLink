package ensemble

import (
	"context"
	"sort"
	"sync"
	"time"
)

type Normalizer interface {
	Normalize(context.Context, Candidate) ([]Candidate, *OperationalError)
}

type Run struct {
	Results  []ResolverResult
	Decision Decision
}

// Execute runs every applicable resolver independently under one bounded
// context. Provider failures never cancel sibling resolvers.
func Execute(ctx context.Context, timeout time.Duration, resolvers []Resolver, req ResolveRequest, normalizer Normalizer) Run {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	applicable := make([]Resolver, 0, len(resolvers))
	for _, resolver := range resolvers {
		if resolver != nil && resolver.Supports(req.Kind) {
			applicable = append(applicable, resolver)
		}
	}
	results := make([]ResolverResult, len(applicable))
	var wg sync.WaitGroup
	for i, resolver := range applicable {
		wg.Add(1)
		go func(index int, resolver Resolver) {
			defer wg.Done()
			result := resolver.Resolve(ctx, req)
			if result.Name == "" {
				result.Name = resolver.Name()
			}
			results[index] = result
		}(i, resolver)
	}
	wg.Wait()

	for i := range results {
		if results[i].Status != ResolverOK || normalizer == nil {
			continue
		}
		var normalized []Candidate
		var normalizeErr *OperationalError
		for _, candidate := range results[i].Candidates {
			mapped, opErr := normalizer.Normalize(ctx, candidate)
			if opErr != nil {
				normalizeErr = opErr
				continue
			}
			normalized = append(normalized, mapped...)
		}
		results[i].Candidates = normalized
		if len(normalized) == 0 {
			if normalizeErr != nil {
				results[i].Status, results[i].Error = ResolverError, normalizeErr
			} else {
				results[i].Status = ResolverAbstain
			}
		} else if normalizeErr != nil {
			results[i].Warnings = appendBoundedStrings(results[i].Warnings, "some candidates could not be normalized")
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return Run{Results: results, Decision: Aggregate(results)}
}

func appendBoundedStrings(values []string, value string) []string {
	if len(values) >= 8 {
		return values
	}
	return append(values, value)
}
