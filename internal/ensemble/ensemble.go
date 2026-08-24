package ensemble

import (
	"context"
	"sort"

	"github.com/SleepingCat/PlexLink/internal/model"
)

// Resolver is an external evidence boundary. Resolvers report observations;
// they do not make the final matching decision.
type Resolver interface {
	Name() string
	Supports(kind model.Kind) bool
	Resolve(ctx context.Context, req ResolveRequest) ResolverResult
}

type ResolverStatus string

const (
	ResolverOK      ResolverStatus = "ok"
	ResolverAbstain ResolverStatus = "abstain"
	ResolverError   ResolverStatus = "error"
)

type ResolveRequest struct {
	Kind            model.Kind
	Title           string
	TitleHypotheses []string
	Year            int
	Season          int
	Files           []model.MediaFile
	TorrentName     string
	ParsedEvidence  model.Evidence
}

type ResolverResult struct {
	Name        string            `json:"name"`
	Status      ResolverStatus    `json:"status"`
	Candidates  []Candidate       `json:"candidates,omitempty"`
	Diagnostics []string          `json:"diagnostics,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
	Error       *OperationalError `json:"error,omitempty"`
}

type OperationalErrorKind string

const (
	ErrorTimeout         OperationalErrorKind = "timeout"
	ErrorRateLimited     OperationalErrorKind = "rate_limited"
	ErrorAuthentication  OperationalErrorKind = "authentication"
	ErrorConfiguration   OperationalErrorKind = "configuration"
	ErrorProvider        OperationalErrorKind = "provider"
	ErrorInvalidResponse OperationalErrorKind = "invalid_response"
	ErrorCanceled        OperationalErrorKind = "canceled"
)

// OperationalError contains only safe, bounded metadata. Resolver
// implementations must not put request headers, URLs with secrets, raw
// response bodies, or credentials in Message.
type OperationalError struct {
	Kind       OperationalErrorKind `json:"kind"`
	StatusCode int                  `json:"status_code,omitempty"`
	Message    string               `json:"message,omitempty"`
	Retryable  bool                 `json:"retryable"`
}

type EntityIdentity struct {
	Kind          model.Kind `json:"kind"`
	TMDBID        int        `json:"tmdb_id,omitempty"`
	IMDbID        string     `json:"imdb_id,omitempty"`
	ProviderID    string     `json:"provider_id,omitempty"`
	Title         string     `json:"title,omitempty"`
	TrustedTitles []string   `json:"trusted_titles,omitempty"`
	Year          int        `json:"year,omitempty"`
}

type Candidate struct {
	Identity EntityIdentity `json:"identity"`
	Evidence []Evidence     `json:"evidence"`
}

type EvidenceFamily string

const (
	FamilyFileIdentity     EvidenceFamily = "FILE_IDENTITY"
	FamilyExternalIdentity EvidenceFamily = "EXTERNAL_IDENTITY"
	FamilyTitle            EvidenceFamily = "TITLE"
	FamilyTime             EvidenceFamily = "TIME"
	FamilyEpisode          EvidenceFamily = "EPISODE"
	FamilyContext          EvidenceFamily = "CONTEXT"
	FamilySourceAgreement  EvidenceFamily = "SOURCE_AGREEMENT"
)

type EvidenceType string

const (
	EvidenceOpenSubtitlesHashExact          EvidenceType = "opensubtitles_hash_exact"
	EvidenceExternalTMDBExact               EvidenceType = "external_tmdb_exact"
	EvidenceExternalIMDbMapsSameTMDB        EvidenceType = "external_imdb_maps_same_tmdb"
	EvidenceTitleExactCanonical             EvidenceType = "title_exact_canonical"
	EvidenceTitleExactLocalized             EvidenceType = "title_exact_localized"
	EvidenceTitleExactAKA                   EvidenceType = "title_exact_aka"
	EvidenceTitleTransliterationStrong      EvidenceType = "title_transliteration_strong"
	EvidenceTitleFuzzyStrong                EvidenceType = "title_fuzzy_strong"
	EvidenceTitleFuzzyWeak                  EvidenceType = "title_fuzzy_weak"
	EvidenceYearReleaseDateExact            EvidenceType = "year_release_date_exact"
	EvidenceYearPrimaryExact                EvidenceType = "year_primary_exact"
	EvidenceYearNearPlausible               EvidenceType = "year_near_plausible"
	EvidenceYearClearMismatch               EvidenceType = "year_clear_mismatch"
	EvidenceEpisodeTitleExact               EvidenceType = "episode_title_exact"
	EvidenceEpisodeSXXEXXExists             EvidenceType = "episode_sxxexx_exists"
	EvidenceSeasonExists                    EvidenceType = "season_exists"
	EvidenceEpisodePackConsistent           EvidenceType = "episode_pack_consistent"
	EvidenceSiblingFilesSameShowStrong      EvidenceType = "sibling_files_same_show_strong"
	EvidenceSameSeasonContext               EvidenceType = "same_season_context"
	EvidenceSameReleaseNamingPattern        EvidenceType = "same_release_naming_pattern"
	EvidenceExternalIdentityConflict        EvidenceType = "external_identity_conflict"
	EvidenceWrongMediaKind                  EvidenceType = "wrong_media_kind"
	EvidenceFileFingerprintIdentityConflict EvidenceType = "file_fingerprint_identity_conflict"
	EvidenceTitleStrongConflict             EvidenceType = "title_strong_conflict"
)

type Evidence struct {
	Family      EvidenceFamily `json:"family"`
	Type        EvidenceType   `json:"type"`
	Source      string         `json:"source"`
	Points      int            `json:"points"`
	SafeDetails string         `json:"safe_details,omitempty"`
}

const (
	PointsOpenSubtitlesHashExact          = 1000
	PointsExternalTMDBExact               = 900
	PointsExternalIMDbMapsSameTMDB        = 800
	PointsTitleExactCanonical             = 300
	PointsTitleExactLocalized             = 300
	PointsTitleExactAKA                   = 280
	PointsTitleTransliterationStrong      = 220
	PointsTitleFuzzyStrong                = 100
	PointsTitleFuzzyWeak                  = 20
	PointsYearReleaseDateExact            = 200
	PointsYearPrimaryExact                = 180
	PointsYearNearPlausible               = 80
	PointsYearClearMismatch               = -250
	PointsEpisodeTitleExact               = 300
	PointsEpisodeSXXEXXExists             = 200
	PointsSeasonExists                    = 100
	PointsEpisodePackConsistent           = 100
	PointsSiblingFilesSameShowStrong      = 250
	PointsSameSeasonContext               = 150
	PointsSameReleaseNamingPattern        = 100
	PointsExternalIdentityConflict        = -1200
	PointsWrongMediaKind                  = -1000
	PointsFileFingerprintIdentityConflict = -1000
	PointsTitleStrongConflict             = -400

	MinTotalScore = 500
	MinMargin     = 200
	MinFamilies   = 2
)

var familyCaps = map[EvidenceFamily]int{
	FamilyFileIdentity: 1000, FamilyExternalIdentity: 900, FamilyTitle: 300,
	FamilyTime: 200, FamilyEpisode: 400, FamilyContext: 300,
	FamilySourceAgreement: 200,
}

func FamilyCap(family EvidenceFamily) int { return familyCaps[family] }

type Conflict struct {
	Type        EvidenceType `json:"type"`
	Source      string       `json:"source"`
	Points      int          `json:"points"`
	SafeDetails string       `json:"safe_details,omitempty"`
}

type AggregateCandidate struct {
	TMDBID          int                    `json:"tmdb_id"`
	Identity        EntityIdentity         `json:"identity"`
	FamilyScores    map[EvidenceFamily]int `json:"family_subtotals"`
	Evidence        []Evidence             `json:"evidence"`
	AgreementScore  int                    `json:"agreement_score"`
	TotalScore      int                    `json:"total_score"`
	HardConflicts   []Conflict             `json:"hard_conflicts"`
	FamilyCount     int                    `json:"family_count"`
	IdentityAnchors int                    `json:"identity_anchors"`
}

type DecisionType string

const (
	DecisionMatch      DecisionType = "MATCH"
	DecisionAIAssisted DecisionType = "AI_ASSISTED_MATCH"
	DecisionAmbiguous  DecisionType = "AMBIGUOUS"
	DecisionConflict   DecisionType = "CONFLICT"
	DecisionNoEvidence DecisionType = "NO_EVIDENCE"
)

type Decision struct {
	Type       DecisionType         `json:"decision"`
	Candidates []AggregateCandidate `json:"candidates"`
	Margin     int                  `json:"margin"`
	Reason     string               `json:"reason"`
}

// Aggregate groups already-normalized candidates by TMDB ID and applies the
// correlation, cap, agreement, and acceptance rules.
func Aggregate(results []ResolverResult) Decision {
	grouped := make(map[int][]sourcedCandidate)
	anchorSourcesByID := make(map[int]map[string]bool)
	for _, result := range results {
		// Operational failures and abstentions are absence of evidence. Even if
		// a buggy adapter attaches stale candidates, they must not affect score,
		// conflicts, identity-anchor diversity, or source agreement.
		if result.Status != ResolverOK {
			continue
		}
		for _, candidate := range result.Candidates {
			if candidate.Identity.TMDBID > 0 {
				grouped[candidate.Identity.TMDBID] = append(grouped[candidate.Identity.TMDBID], sourcedCandidate{Candidate: candidate, Resolver: result.Name})
				for _, evidence := range candidate.Evidence {
					if evidence.Points > 0 && isIdentityAnchorFamily(evidence.Family) {
						source := result.Name
						if source == "" {
							source = evidence.Source
						}
						if source != "" {
							if anchorSourcesByID[candidate.Identity.TMDBID] == nil {
								anchorSourcesByID[candidate.Identity.TMDBID] = make(map[string]bool)
							}
							anchorSourcesByID[candidate.Identity.TMDBID][source] = true
						}
					}
				}
			}
		}
	}
	markContradictorySourceAnchors(grouped, anchorSourcesByID)

	ids := make([]int, 0, len(grouped))
	for id := range grouped {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	candidates := make([]AggregateCandidate, 0, len(ids))
	for _, id := range ids {
		candidates = append(candidates, scoreCandidate(id, grouped[id]))
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].TotalScore != candidates[j].TotalScore {
			return candidates[i].TotalScore > candidates[j].TotalScore
		}
		return candidates[i].TMDBID < candidates[j].TMDBID
	})

	if len(candidates) == 0 {
		return Decision{Type: DecisionNoEvidence, Reason: "no normalized TMDB evidence"}
	}
	margin := candidates[0].TotalScore
	if len(candidates) > 1 {
		margin -= candidates[1].TotalScore
	}
	top := candidates[0]
	decision := Decision{Type: DecisionAmbiguous, Candidates: candidates, Margin: margin}
	switch {
	case len(top.HardConflicts) > 0:
		decision.Type, decision.Reason = DecisionConflict, "top candidate has hard conflicting evidence"
	case top.FamilyCount < MinFamilies && top.IdentityAnchors < 2:
		decision.Reason = "insufficient independent evidence families"
	case top.TotalScore < MinTotalScore:
		decision.Reason = "top score below acceptance threshold"
	case margin < MinMargin:
		decision.Reason = "top candidate margin below acceptance threshold"
	default:
		decision.Type, decision.Reason = DecisionMatch, "score, margin, diversity, and conflict gates passed"
	}
	return decision
}

type sourcedCandidate struct {
	Candidate
	Resolver string
}

func scoreCandidate(id int, candidates []sourcedCandidate) AggregateCandidate {
	result := AggregateCandidate{TMDBID: id, Identity: candidates[0].Identity, FamilyScores: make(map[EvidenceFamily]int)}
	var all []Evidence
	for _, candidate := range candidates {
		all = append(all, candidate.Evidence...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Family != all[j].Family {
			return all[i].Family < all[j].Family
		}
		if all[i].Type != all[j].Type {
			return all[i].Type < all[j].Type
		}
		if all[i].Source != all[j].Source {
			return all[i].Source < all[j].Source
		}
		if all[i].Points != all[j].Points {
			return all[i].Points > all[j].Points
		}
		return all[i].SafeDetails < all[j].SafeDetails
	})
	result.Evidence = all

	strongest := make(map[EvidenceType]Evidence)
	for _, evidence := range all {
		current, ok := strongest[evidence.Type]
		if !ok || stronger(evidence.Points, current.Points) {
			strongest[evidence.Type] = evidence
		}
	}
	types := make([]string, 0, len(strongest))
	byName := make(map[string]Evidence, len(strongest))
	for typ, evidence := range strongest {
		key := string(typ)
		types = append(types, key)
		byName[key] = evidence
	}
	sort.Strings(types)
	positive := make(map[EvidenceFamily]int)
	negative := make(map[EvidenceFamily]int)
	positiveFamilies := make(map[EvidenceFamily]bool)
	for _, name := range types {
		evidence := byName[name]
		if evidence.Points > 0 {
			positive[evidence.Family] += evidence.Points
			positiveFamilies[evidence.Family] = true
		} else if evidence.Points < 0 {
			negative[evidence.Family] += evidence.Points
		}
		if isHardConflict(evidence.Type) {
			result.HardConflicts = append(result.HardConflicts, Conflict{Type: evidence.Type, Source: evidence.Source, Points: evidence.Points, SafeDetails: evidence.SafeDetails})
		}
	}
	for family, score := range positive {
		if cap := FamilyCap(family); cap > 0 && score > cap {
			score = cap
		}
		result.FamilyScores[family] = score + negative[family]
	}
	for family, score := range negative {
		if _, ok := positive[family]; !ok {
			result.FamilyScores[family] = score
		}
	}
	result.FamilyCount = len(positiveFamilies)

	sources := make(map[string]bool)
	anchorSources := make(map[string]bool)
	for _, candidate := range candidates {
		resolver := candidate.Resolver
		for _, evidence := range candidate.Evidence {
			if evidence.Points <= 0 {
				continue
			}
			// Name is the authoritative resolver identity. Falling back to the
			// evidence source keeps hand-built/local evidence usable without
			// allowing one resolver to manufacture several agreement votes.
			if resolver == "" {
				resolver = evidence.Source
			}
			if resolver == "" {
				continue
			}
			if supportsAgreement(evidence.Family) {
				sources[resolver] = true
			}
			if isIdentityAnchorFamily(evidence.Family) {
				anchorSources[resolver] = true
			}
		}
	}
	result.IdentityAnchors = len(anchorSources)
	if len(sources) > 1 {
		result.AgreementScore = (len(sources) - 1) * 50
		if result.AgreementScore > 200 {
			result.AgreementScore = 200
		}
	}
	result.FamilyScores[FamilySourceAgreement] = result.AgreementScore
	for _, score := range result.FamilyScores {
		result.TotalScore += score
	}
	sort.SliceStable(result.HardConflicts, func(i, j int) bool {
		if result.HardConflicts[i].Type != result.HardConflicts[j].Type {
			return result.HardConflicts[i].Type < result.HardConflicts[j].Type
		}
		return result.HardConflicts[i].Source < result.HardConflicts[j].Source
	})
	return result
}

func supportsAgreement(family EvidenceFamily) bool {
	switch family {
	case FamilyFileIdentity, FamilyTitle, FamilyTime, FamilyEpisode, FamilyContext:
		return true
	default:
		return false
	}
}

func isIdentityAnchorFamily(family EvidenceFamily) bool {
	return family == FamilyFileIdentity || family == FamilyExternalIdentity
}

func markContradictorySourceAnchors(grouped map[int][]sourcedCandidate, sourcesByID map[int]map[string]bool) {
	for id, sources := range sourcesByID {
		conflicts := false
		for otherID, otherSources := range sourcesByID {
			if otherID == id {
				continue
			}
			for source := range sources {
				for otherSource := range otherSources {
					if source != otherSource {
						conflicts = true
					}
				}
			}
		}
		if conflicts && len(grouped[id]) > 0 {
			grouped[id][0].Evidence = append(grouped[id][0].Evidence, Evidence{
				Family: FamilyExternalIdentity, Type: EvidenceExternalIdentityConflict,
				Source: "source_identity_anchors", Points: PointsExternalIdentityConflict,
				SafeDetails: "independent source identity anchors disagree",
			})
		}
	}
}

func stronger(candidate, current int) bool {
	if candidate < 0 || current < 0 {
		return candidate < current
	}
	return candidate > current
}

func isHardConflict(typ EvidenceType) bool {
	switch typ {
	case EvidenceExternalIdentityConflict, EvidenceWrongMediaKind, EvidenceFileFingerprintIdentityConflict, EvidenceTitleStrongConflict:
		return true
	default:
		return false
	}
}
