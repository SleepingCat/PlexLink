package ensemble

import (
	"reflect"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/model"
)

func ev(family EvidenceFamily, typ EvidenceType, source string, points int) Evidence {
	return Evidence{Family: family, Type: typ, Source: source, Points: points}
}

func result(id int, evidence ...Evidence) ResolverResult {
	name := ""
	if len(evidence) > 0 {
		name = evidence[0].Source
	}
	return ResolverResult{Name: name, Status: ResolverOK, Candidates: []Candidate{{Identity: EntityIdentity{Kind: model.KindMovie, TMDBID: id}, Evidence: evidence}}}
}

func top(t *testing.T, decision Decision) AggregateCandidate {
	t.Helper()
	if len(decision.Candidates) == 0 {
		t.Fatal("decision has no candidates")
	}
	return decision.Candidates[0]
}

func TestDuplicateEvidenceTypeCountsOnceAndAgreementCountsSources(t *testing.T) {
	d := Aggregate([]ResolverResult{
		result(1, ev(FamilyTitle, EvidenceTitleExactCanonical, "tmdb", 300)),
		result(1, ev(FamilyTitle, EvidenceTitleExactCanonical, "kinopoisk", 300)),
		result(1, ev(FamilyTitle, EvidenceTitleExactCanonical, "tvmaze", 300)),
	})
	c := top(t, d)
	if c.FamilyScores[FamilyTitle] != 300 || c.AgreementScore != 100 || c.TotalScore != 400 {
		t.Fatalf("scores=%v agreement=%d total=%d", c.FamilyScores, c.AgreementScore, c.TotalScore)
	}
	if d.Type != DecisionAmbiguous {
		t.Fatalf("decision=%s", d.Type)
	}
}

func TestFamilyCapAndDistinctFamilies(t *testing.T) {
	d := Aggregate([]ResolverResult{result(1,
		ev(FamilyTitle, EvidenceTitleExactCanonical, "tmdb", 300),
		ev(FamilyTitle, EvidenceTitleExactAKA, "tmdb", 280),
		ev(FamilyTime, EvidenceYearReleaseDateExact, "tmdb", 200),
	)})
	c := top(t, d)
	if c.FamilyScores[FamilyTitle] != 300 || c.FamilyScores[FamilyTime] != 200 || c.FamilyCount != 2 || c.TotalScore != 500 {
		t.Fatalf("candidate=%+v", c)
	}
	if d.Type != DecisionMatch {
		t.Fatalf("decision=%s reason=%s", d.Type, d.Reason)
	}
}

func TestExactTitleAndReleaseYearPassOnlyAllAcceptanceGates(t *testing.T) {
	d := Aggregate([]ResolverResult{result(1,
		ev(FamilyTitle, EvidenceTitleExactCanonical, "tmdb", PointsTitleExactCanonical),
		ev(FamilyTime, EvidenceYearReleaseDateExact, "tmdb", PointsYearReleaseDateExact),
	)})
	if d.Type != DecisionMatch || top(t, d).TotalScore != MinTotalScore || top(t, d).FamilyCount != MinFamilies {
		t.Fatalf("decision=%+v", d)
	}

	withNeighbor := Aggregate([]ResolverResult{
		result(1, ev(FamilyTitle, EvidenceTitleExactCanonical, "tmdb", PointsTitleExactCanonical), ev(FamilyTime, EvidenceYearReleaseDateExact, "tmdb", PointsYearReleaseDateExact)),
		result(2, ev(FamilyTitle, EvidenceTitleExactCanonical, "tmdb", PointsTitleExactCanonical), ev(FamilyTime, EvidenceYearReleaseDateExact, "tmdb", PointsYearReleaseDateExact)),
	})
	if withNeighbor.Type != DecisionAmbiguous || withNeighbor.Margin != 0 {
		t.Fatalf("equal neighbor bypassed margin gate: %+v", withNeighbor)
	}
}

func TestFuzzyTitleAndYearCannotAutoMatchThroughSourceAgreement(t *testing.T) {
	d := Aggregate([]ResolverResult{
		result(1, ev(FamilyTitle, EvidenceTitleFuzzyStrong, "tmdb", PointsTitleFuzzyStrong), ev(FamilyTime, EvidenceYearReleaseDateExact, "tmdb", PointsYearReleaseDateExact)),
		result(1, ev(FamilyTitle, EvidenceTitleFuzzyStrong, "kinopoisk", PointsTitleFuzzyStrong), ev(FamilyTime, EvidenceYearReleaseDateExact, "kinopoisk", PointsYearReleaseDateExact)),
	})
	c := top(t, d)
	if d.Type != DecisionAmbiguous || c.FamilyScores[FamilyTitle] != 100 || c.FamilyScores[FamilyTime] != 200 || c.AgreementScore != 50 || c.TotalScore != 350 {
		t.Fatalf("decision=%+v", d)
	}
}

func TestNegativeEvidenceStillAppliesAfterPositiveCap(t *testing.T) {
	d := Aggregate([]ResolverResult{result(1,
		ev(FamilyTime, EvidenceYearReleaseDateExact, "tmdb", 200),
		ev(FamilyTime, EvidenceYearPrimaryExact, "kinopoisk", 180),
		ev(FamilyTime, EvidenceYearClearMismatch, "tvmaze", -250),
	)})
	c := top(t, d)
	if c.FamilyScores[FamilyTime] != -50 || c.TotalScore != -50 {
		t.Fatalf("scores=%v total=%d", c.FamilyScores, c.TotalScore)
	}
}

func TestAgreementIsCappedAtTwoHundred(t *testing.T) {
	var results []ResolverResult
	for _, source := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		results = append(results, result(1, ev(FamilyTitle, EvidenceTitleExactCanonical, source, 300)))
	}
	c := top(t, Aggregate(results))
	if c.AgreementScore != 200 || c.TotalScore != 500 {
		t.Fatalf("agreement=%d total=%d", c.AgreementScore, c.TotalScore)
	}
}

func TestHardConflictForcesConflict(t *testing.T) {
	d := Aggregate([]ResolverResult{result(1,
		ev(FamilyFileIdentity, EvidenceOpenSubtitlesHashExact, "opensubtitles", 1000),
		ev(FamilyTitle, EvidenceTitleExactCanonical, "tmdb", 300),
		ev(FamilyExternalIdentity, EvidenceWrongMediaKind, "kinopoisk", -1000),
	)})
	if d.Type != DecisionConflict || len(top(t, d).HardConflicts) != 1 {
		t.Fatalf("decision=%+v", d)
	}
}

func TestFingerprintNeedsIndependentCorroboration(t *testing.T) {
	d := Aggregate([]ResolverResult{result(1, ev(FamilyFileIdentity, EvidenceOpenSubtitlesHashExact, "opensubtitles", 1000))})
	if d.Type != DecisionAmbiguous {
		t.Fatalf("fingerprint-only decision=%s", d.Type)
	}
	d = Aggregate([]ResolverResult{
		result(1, ev(FamilyFileIdentity, EvidenceOpenSubtitlesHashExact, "opensubtitles", 1000)),
		result(1, ev(FamilyTime, EvidenceYearPrimaryExact, "tmdb", 180)),
	})
	if d.Type != DecisionMatch {
		t.Fatalf("corroborated decision=%s reason=%s", d.Type, d.Reason)
	}
}

func TestTwoIndependentIdentityAnchorsSatisfyDiversity(t *testing.T) {
	d := Aggregate([]ResolverResult{
		result(1, ev(FamilyExternalIdentity, EvidenceExternalTMDBExact, "kinopoisk", 900)),
		result(1, ev(FamilyExternalIdentity, EvidenceExternalTMDBExact, "tvmaze", 900)),
	})
	c := top(t, d)
	if c.IdentityAnchors != 2 || c.FamilyCount != 1 || d.Type != DecisionMatch {
		t.Fatalf("anchors=%d families=%d decision=%s reason=%s", c.IdentityAnchors, c.FamilyCount, d.Type, d.Reason)
	}
}

func TestMarginAndDeterministicCandidateOrdering(t *testing.T) {
	inputs := []ResolverResult{
		result(9, ev(FamilyTitle, EvidenceTitleExactCanonical, "tmdb", 300), ev(FamilyTime, EvidenceYearReleaseDateExact, "tmdb", 200)),
		result(3, ev(FamilyTitle, EvidenceTitleExactCanonical, "tmdb", 300), ev(FamilyTime, EvidenceYearReleaseDateExact, "tmdb", 200)),
	}
	first := Aggregate(inputs)
	second := Aggregate([]ResolverResult{inputs[1], inputs[0]})
	ids := func(d Decision) []int {
		out := make([]int, len(d.Candidates))
		for i := range d.Candidates {
			out[i] = d.Candidates[i].TMDBID
		}
		return out
	}
	if !reflect.DeepEqual(ids(first), []int{3, 9}) || !reflect.DeepEqual(ids(first), ids(second)) {
		t.Fatalf("orders=%v and %v", ids(first), ids(second))
	}
	if first.Type != DecisionAmbiguous || first.Margin != 0 {
		t.Fatalf("decision=%s margin=%d", first.Type, first.Margin)
	}
}

func TestScoreBreakdownEqualsTotal(t *testing.T) {
	c := top(t, Aggregate([]ResolverResult{
		result(1, ev(FamilyTitle, EvidenceTitleExactCanonical, "tmdb", 300), ev(FamilyTime, EvidenceYearPrimaryExact, "tmdb", 180)),
		result(1, ev(FamilyContext, EvidenceSameReleaseNamingPattern, "parser", 100)),
	}))
	sum := 0
	for _, score := range c.FamilyScores {
		sum += score
	}
	if sum != c.TotalScore {
		t.Fatalf("breakdown=%d total=%d", sum, c.TotalScore)
	}
}

func TestNoNormalizedEvidence(t *testing.T) {
	d := Aggregate([]ResolverResult{result(0, ev(FamilyTitle, EvidenceTitleExactCanonical, "tmdb", 300))})
	if d.Type != DecisionNoEvidence {
		t.Fatalf("decision=%s", d.Type)
	}
}

func TestErrorAndAbstainResultsAreNeutral(t *testing.T) {
	failed := result(1,
		ev(FamilyExternalIdentity, EvidenceExternalIdentityConflict, "failed-api", -1200),
		ev(FamilyTitle, EvidenceTitleExactCanonical, "failed-api", 300),
	)
	failed.Status = ResolverError
	failed.Error = &OperationalError{Kind: ErrorRateLimited, StatusCode: 429, Message: "provider unavailable", Retryable: true}
	abstained := result(1, ev(FamilyTitle, EvidenceTitleExactCanonical, "empty-api", 300))
	abstained.Status = ResolverAbstain
	ok := result(1,
		ev(FamilyTitle, EvidenceTitleExactCanonical, "tmdb", 300),
		ev(FamilyTime, EvidenceYearReleaseDateExact, "tmdb", 200),
	)

	d := Aggregate([]ResolverResult{failed, abstained, ok})
	c := top(t, d)
	if d.Type != DecisionMatch || c.TotalScore != 500 || c.AgreementScore != 0 || len(c.HardConflicts) != 0 {
		t.Fatalf("decision=%s candidate=%+v", d.Type, c)
	}
	if len(c.Evidence) != 2 {
		t.Fatalf("failed/abstaining evidence was retained: %+v", c.Evidence)
	}
}

func TestOneResolverCannotManufactureAgreementWithEvidenceSourceLabels(t *testing.T) {
	r := result(1,
		ev(FamilyTitle, EvidenceTitleExactCanonical, "catalog-title", 300),
		ev(FamilyTime, EvidenceYearReleaseDateExact, "catalog-date", 200),
	)
	r.Name = "one-resolver"
	c := top(t, Aggregate([]ResolverResult{r}))
	if c.AgreementScore != 0 {
		t.Fatalf("agreement=%d", c.AgreementScore)
	}
}
