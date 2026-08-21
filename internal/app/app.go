package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/SleepingCat/PlexLink/internal/ai"
	"github.com/SleepingCat/PlexLink/internal/config"
	"github.com/SleepingCat/PlexLink/internal/ensemble"
	"github.com/SleepingCat/PlexLink/internal/linker"
	"github.com/SleepingCat/PlexLink/internal/matcher"
	"github.com/SleepingCat/PlexLink/internal/model"
	"github.com/SleepingCat/PlexLink/internal/pathutil"
	"github.com/SleepingCat/PlexLink/internal/plexpath"
	"github.com/SleepingCat/PlexLink/internal/release"
	"github.com/SleepingCat/PlexLink/internal/state"
)

var (
	ErrIgnored        = errors.New("ignored")
	ErrUnresolved     = errors.New("unresolved")
	ErrAnimeNumbering = errors.New("unresolved anime numbering")
	ErrConflict       = errors.New("target conflict")
	ErrTorrent        = errors.New("qBittorrent error")
	ErrMetadata       = errors.New("TMDB error")
	ErrAI             = errors.New("AI provider error")
)

type TorrentClient interface {
	GetTorrent(context.Context, string) (model.Torrent, error)
	GetFiles(context.Context, string) ([]model.TorrentFile, error)
}
type MetadataProvider interface {
	SearchTV(context.Context, string) ([]model.TVCandidate, error)
	GetTV(context.Context, int) (model.TVShow, error)
	GetSeason(context.Context, int, int) (model.Season, error)
	SearchMovie(context.Context, string) ([]model.MovieCandidate, error)
	GetMovie(context.Context, int) (model.Movie, error)
	GetMovieReleaseDates(context.Context, int) (model.MovieReleaseDates, error)
}
type Processor struct {
	Torrents       TorrentClient
	Metadata       MetadataProvider
	AI             ai.Resolver
	AIProvider     string
	AIModel        string
	Resolvers      []ensemble.Resolver
	EpisodeCatalog EpisodeCatalog
	Config         config.Config
}
type ProcessOptions struct {
	DryRun   bool
	ManualID int
	NoAI     bool
}

type dryRunContextKey struct{}

type AIDiagnostics struct {
	Used              bool       `json:"ai_used"`
	Provider          string     `json:"provider,omitempty"`
	Model             string     `json:"configured_model,omitempty"`
	ActualModel       string     `json:"actual_model,omitempty"`
	FinishReason      string     `json:"finish_reason,omitempty"`
	CompletionTokens  int        `json:"completion_tokens,omitempty"`
	ReasoningTokens   int        `json:"reasoning_tokens,omitempty"`
	PromptVersion     string     `json:"prompt_version,omitempty"`
	WebSearchPolicy   string     `json:"web_search_policy,omitempty"`
	WebSearchUsed     *bool      `json:"web_search_used,omitempty"`
	CacheHit          bool       `json:"cache_hit"`
	Calls             int        `json:"calls"`
	ProviderRequests  int        `json:"provider_requests"`
	HTTPStatus        int        `json:"http_status,omitempty"`
	ProviderErrorCode string     `json:"provider_error_code,omitempty"`
	RetryAfterSeconds int        `json:"retry_after_seconds,omitempty"`
	Hypothesis        *ai.Result `json:"hypothesis,omitempty"`
	Verified          bool       `json:"-"`
	Error             string     `json:"error,omitempty"`
}

type ResolutionDiagnostics struct {
	FinalTMDBVerified       bool   `json:"final_tmdb_verified"`
	ResolutionSchemaVersion string `json:"resolution_schema_version"`
	ScoringVersion          string `json:"scoring_version"`
	EpisodeMappingVersion   string `json:"episode_mapping_version"`
}
type Result struct {
	Torrent                model.Torrent             `json:"torrent"`
	Kind                   model.Kind                `json:"kind"`
	Evidence               model.Evidence            `json:"evidence"`
	Candidates             []model.Match             `json:"candidates"`
	Match                  model.Match               `json:"match"`
	EpisodeValidation      []model.EpisodeValidation `json:"episode_validation,omitempty"`
	MappingStatus          model.MappingStatus       `json:"mapping_status,omitempty"`
	Plan                   []model.LinkPlan          `json:"plan"`
	Actions                []linker.Action           `json:"actions"`
	AI                     AIDiagnostics             `json:"ai"`
	Ensemble               EnsembleDiagnostics       `json:"ensemble"`
	Resolution             ResolutionDiagnostics     `json:"resolution"`
	ReconciliationWarnings []string                  `json:"reconciliation_warnings,omitempty"`
}

type EnsembleDiagnostics struct {
	Used              bool                      `json:"used"`
	CachedResolution  bool                      `json:"cached_resolution"`
	ResolverResults   []ensemble.ResolverResult `json:"resolver_results,omitempty"`
	FirstPass         *ensemble.Decision        `json:"first_pass,omitempty"`
	SecondPassUsed    bool                      `json:"second_pass_used"`
	FinalDecision     *ensemble.Decision        `json:"final_decision,omitempty"`
	FinalTMDBVerified bool                      `json:"-"`
}

func (p *Processor) Process(ctx context.Context, hash string, dry bool, manualID int) (Result, error) {
	return p.ProcessWithOptions(ctx, hash, ProcessOptions{DryRun: dry, ManualID: manualID})
}

func (p *Processor) ProcessWithOptions(ctx context.Context, hash string, options ProcessOptions) (Result, error) {
	dry, manualID := options.DryRun, options.ManualID
	if dry {
		ctx = context.WithValue(ctx, dryRunContextKey{}, true)
	}
	explicitID := manualID
	var cached state.VerifiedResolution
	cachedResolution := false
	if manualID == 0 {
		var err error
		cached, cachedResolution, err = state.Verified(p.Config.State.Directory, hash)
		if err != nil {
			return Result{}, err
		}
		if cachedResolution {
			manualID = cached.TMDBID
		} else {
			manualID, err = state.Resolution(p.Config.State.Directory, hash)
			if err != nil {
				return Result{}, err
			}
		}
	}
	t, err := p.Torrents.GetTorrent(ctx, hash)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrTorrent, err)
	}
	r := Result{Torrent: t, Resolution: ResolutionDiagnostics{ResolutionSchemaVersion: state.ResolutionSchemaVersion, ScoringVersion: state.ScoringVersion, EpisodeMappingVersion: state.EpisodeMappingVersion}}
	if cachedResolution {
		r.Ensemble.CachedResolution = true
		r.AI.ActualModel = cached.ActualAIModel
	}
	if t.Progress < 1 {
		return r, fmt.Errorf("%w: torrent is not complete", ErrIgnored)
	}
	kind, sourceRoot, targetRoot, ok := p.kind(t.ContentPath)
	if !ok {
		return r, fmt.Errorf("%w: torrent is outside configured source roots", ErrIgnored)
	}
	r.Kind = kind
	files, err := p.Torrents.GetFiles(ctx, hash)
	if err != nil {
		return r, fmt.Errorf("%w: %v", ErrTorrent, err)
	}
	e, media := release.Parse(t, files, kind)
	r.Evidence = e
	if len(media) == 0 {
		return r, fmt.Errorf("%w: no supported completed media files", ErrIgnored)
	}
	for i := range media {
		media[i].Source = filepath.Join(t.SavePath, filepath.FromSlash(media[i].Name))
	}
	useEnsemble := manualID == 0 && len(p.Resolvers) > 0
	var match model.Match
	var show model.TVShow
	if cachedResolution {
		match, show, err = p.resolveCached(ctx, kind, cached)
	} else if useEnsemble {
		match, show, err = p.resolveEnsemble(ctx, kind, t.Name, media, e, !options.NoAI && p.Config.AI.Enabled, &r)
	} else {
		match, show, err = p.resolve(ctx, kind, e, manualID, &r)
	}
	if !useEnsemble && errors.Is(err, ErrUnresolved) && manualID == 0 && !options.NoAI && p.Config.AI.Enabled {
		match, show, err = p.resolveAI(ctx, kind, t.Name, media, e, &r)
	}
	if err != nil {
		if errors.Is(err, ErrUnresolved) || errors.Is(err, ErrAnimeNumbering) {
			if !dry {
				_ = p.saveUnresolved(hash, r)
			}
		}
		return r, err
	}
	r.Resolution.FinalTMDBVerified = true
	r.Match = match
	if kind == model.KindAnime {
		if err := matcher.MapAnimeAbsolute(show, media); err != nil {
			if !dry {
				_ = p.saveUnresolved(hash, r)
			}
			return r, ErrAnimeNumbering
		}
	}
	if kind != model.KindMovie {
		validations, valid := p.validateEpisodesForKind(ctx, kind, match.ID, media)
		r.EpisodeValidation = validations
		if !valid && hasUnresolvedMappings(validations) && !options.NoAI && p.Config.AI.Enabled {
			if mapErr := p.resolveAIEpisodes(ctx, kind, t.Name, match.ID, media, e, &r); mapErr != nil {
				if !errors.Is(mapErr, ErrUnresolved) {
					return r, mapErr
				}
			} else {
				validations, valid = p.validateEpisodesForKind(ctx, kind, match.ID, media)
				r.EpisodeValidation = validations
			}
		}
		for _, file := range files {
			if file.Priority > 0 && file.Progress >= 1 && release.IsIgnored(file.Name) {
				r.EpisodeValidation = append(r.EpisodeValidation, model.EpisodeValidation{File: file.Name, State: model.EpisodeIgnored, Reason: "sample/trailer/extra ignored by media policy"})
			}
		}
		if cachedResolution {
			for i := range r.EpisodeValidation {
				mapping := &r.EpisodeValidation[i]
				previous, ok := cached.Files[mapping.File]
				if !ok || previous.State != string(model.EpisodeProvisional) || (previous.Season == mapping.Season && previous.Episode == mapping.Episode) {
					continue
				}
				mapping.State = model.EpisodeUnresolved
				mapping.Reason = "previous provisional numbering changed; reconciliation required and old target is retained"
				r.ReconciliationWarnings = append(r.ReconciliationWarnings, mapping.File+": provisional mapping changed")
			}
		}
		r.MappingStatus = mappingStatus(r.EpisodeValidation)
		if safeMappingCount(r.EpisodeValidation) == 0 {
			if !dry {
				_ = p.saveUnresolved(hash, r)
			}
			return r, fmt.Errorf("%w: unmapped files: %s", ErrUnresolved, unresolvedEpisodeSummary(validations))
		}
	}
	mappings := make(map[string]*model.EpisodeValidation)
	for i := range r.EpisodeValidation {
		mappings[r.EpisodeValidation[i].File] = &r.EpisodeValidation[i]
	}
	for _, f := range media {
		if mapping := mappings[f.Name]; mapping != nil && mapping.State != model.EpisodeResolved && mapping.State != model.EpisodeProvisional {
			continue
		}
		if kind != model.KindMovie && (f.Ref.Season < 0 || f.Ref.Episode == 0) {
			continue
		}
		target, err := plexpath.Build(targetRoot, kind, match, f)
		if err != nil {
			return r, err
		}
		r.Plan = append(r.Plan, model.LinkPlan{Source: f.Source, Target: target})
		if mapping := mappings[f.Name]; mapping != nil {
			mapping.PlannedTarget = target
		}
	}
	hadConflict := false
	for _, plan := range r.Plan {
		action, err := linker.Link(sourceRoot, targetRoot, plan.Source, plan.Target, dry)
		if err != nil {
			return r, err
		}
		r.Actions = append(r.Actions, action)
		if action == linker.Conflict {
			hadConflict = true
			if mapping := mappingsBySource(r.EpisodeValidation, plan.Source, media); mapping != nil {
				mapping.State, mapping.Reason = model.EpisodeUnresolved, "target exists for a different source"
			}
			continue
		}
	}
	if hadConflict {
		r.MappingStatus = model.MappingConflict
		return r, ErrConflict
	}
	if explicitID > 0 && !dry {
		if err := state.SaveResolution(p.Config.State.Directory, hash, explicitID); err != nil {
			return r, fmt.Errorf("save manual resolution: %w", err)
		}
	}
	if !dry && (useEnsemble || cachedResolution || explicitID > 0) {
		verified := state.VerifiedResolution{TMDBID: match.ID, Kind: string(kind), Title: match.Name, Year: match.Year, ActualAIModel: r.AI.ActualModel, Files: make(map[string]state.VerifiedFileMapping)}
		for _, mapping := range r.EpisodeValidation {
			verified.Files[mapping.File] = state.VerifiedFileMapping{State: string(mapping.State), Season: mapping.Season, Episode: mapping.Episode}
		}
		if err := state.SaveVerified(p.Config.State.Directory, hash, verified); err != nil {
			return r, fmt.Errorf("save verified resolution: %w", err)
		}
	}
	return r, nil
}

func (p *Processor) resolveCached(ctx context.Context, kind model.Kind, cached state.VerifiedResolution) (model.Match, model.TVShow, error) {
	match := model.Match{ID: cached.TMDBID, Name: cached.Title, Year: cached.Year, Score: ensemble.MinTotalScore}
	if kind == model.KindMovie {
		if movie, err := p.Metadata.GetMovie(ctx, cached.TMDBID); err == nil && movie.ID == cached.TMDBID {
			match.Name, match.Year = movie.Title, year(movie.ReleaseDate)
		}
		return match, model.TVShow{}, nil
	}
	if show, err := p.Metadata.GetTV(ctx, cached.TMDBID); err == nil && show.ID == cached.TMDBID {
		match.Name, match.Year = show.Name, year(show.FirstAirDate)
		return match, show, nil
	}
	return match, model.TVShow{ID: cached.TMDBID, Name: cached.Title}, nil
}

func (p *Processor) resolveEnsemble(ctx context.Context, kind model.Kind, torrentName string, media []model.MediaFile, evidence model.Evidence, allowAI bool, result *Result) (model.Match, model.TVShow, error) {
	req := ensembleRequest(kind, torrentName, media, evidence, nil)
	run := ensemble.Execute(ctx, p.resolverTimeout(), p.Resolvers, req, tmdbNormalizer{metadata: p.Metadata})
	result.Ensemble.Used = true
	result.Ensemble.ResolverResults = run.Results
	result.Ensemble.FirstPass = &run.Decision
	decision := run.Decision
	allResults := append([]ensemble.ResolverResult(nil), run.Results...)

	if decision.Type != ensemble.DecisionMatch && allowAI {
		hypotheses, err := p.ensembleConsultAI(ctx, req, decision, result)
		if err != nil {
			return model.Match{}, model.TVShow{}, err
		}
		if len(hypotheses) > 0 {
			requeryReq := ensembleRequest(kind, torrentName, media, evidence, hypotheses)
			second := ensemble.Execute(ctx, p.resolverTimeout(), catalogResolvers(p.Resolvers), requeryReq, tmdbNormalizer{metadata: p.Metadata})
			result.Ensemble.SecondPassUsed = true
			allResults = append(allResults, second.Results...)
			decision = ensemble.Aggregate(allResults)
			result.Ensemble.ResolverResults = allResults
		}
	}
	result.Ensemble.FinalDecision = &decision
	result.Candidates = aggregateMatches(decision)
	if decision.Type != ensemble.DecisionMatch || len(decision.Candidates) == 0 {
		return model.Match{}, model.TVShow{}, ErrUnresolved
	}
	top := decision.Candidates[0]
	match := model.Match{ID: top.TMDBID, Name: top.Identity.Title, Year: top.Identity.Year, Score: top.TotalScore, Margin: decision.Margin, Breakdown: aggregateBreakdown(top)}
	if kind == model.KindMovie {
		movie, err := p.Metadata.GetMovie(ctx, top.TMDBID)
		if err != nil || movie.ID != top.TMDBID {
			return model.Match{}, model.TVShow{}, fmt.Errorf("%w: final movie verification failed", ErrMetadata)
		}
		match.Name, match.Year = movie.Title, year(movie.ReleaseDate)
		result.Ensemble.FinalTMDBVerified, result.AI.Verified = true, result.AI.Used
		return match, model.TVShow{}, nil
	}
	show, err := p.Metadata.GetTV(ctx, top.TMDBID)
	if err != nil || show.ID != top.TMDBID {
		return model.Match{}, model.TVShow{}, fmt.Errorf("%w: final show verification failed", ErrMetadata)
	}
	match.Name, match.Year = show.Name, year(show.FirstAirDate)
	result.Ensemble.FinalTMDBVerified, result.AI.Verified = true, result.AI.Used
	return match, show, nil
}

func (p *Processor) ensembleConsultAI(ctx context.Context, req ensemble.ResolveRequest, decision ensemble.Decision, result *Result) ([]string, error) {
	if p.AI == nil {
		return nil, nil
	}
	titles := make([]string, 0, len(req.ParsedEvidence.Titles))
	for _, title := range req.ParsedEvidence.Titles {
		titles = append(titles, title.Title)
	}
	aiReq := ai.Request{Task: ai.IdentifyMedia, Kind: req.Kind, TorrentName: req.TorrentName, Files: sampledRelativeFiles(req.Files, 60, 12000), Parsed: ai.ParsedEvidence{Titles: titles, Year: req.Year, Episodes: req.ParsedEvidence.Episodes}, WebSearch: ai.WebSearchPolicy(p.Config.AI.WebSearch)}
	for _, candidate := range decision.Candidates {
		aiReq.Candidates = append(aiReq.Candidates, ai.Candidate{ID: candidate.TMDBID, Title: candidate.Identity.Title, Year: candidate.Identity.Year})
	}
	hypothesis, hit, err := p.callAI(ctx, aiReq, result)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: AI consultant canceled", ErrAI)
		}
		result.AI.Error = "AI consultant unavailable"
		return nil, nil
	}
	result.AI.CacheHit, result.AI.Hypothesis, result.AI.WebSearchUsed = hit, &hypothesis, hypothesis.WebSearchUsed
	if hypothesis.Status != ai.Resolved || hypothesis.Confidence < p.Config.AI.MinConfidence {
		return nil, nil
	}
	queries := append([]string(nil), hypothesis.SearchQueries...)
	queries = append(queries, hypothesis.CanonicalTitle)
	queries = append(queries, hypothesis.LocalizedTitles...)
	return uniqueQueries(queries), nil
}

func ensembleRequest(kind model.Kind, torrentName string, media []model.MediaFile, evidence model.Evidence, hypotheses []string) ensemble.ResolveRequest {
	title := ""
	if len(evidence.Titles) > 0 {
		title = evidence.Titles[0].Title
	}
	season := 0
	for _, episode := range evidence.Episodes {
		if episode.Season > 0 {
			season = episode.Season
			break
		}
	}
	return ensemble.ResolveRequest{Kind: kind, Title: title, TitleHypotheses: hypotheses, Year: evidence.Year, Season: season, Files: media, TorrentName: torrentName, ParsedEvidence: evidence}
}

func catalogResolvers(resolvers []ensemble.Resolver) []ensemble.Resolver {
	allowed := map[string]bool{"tmdb": true, "kinopoisk": true, "tvmaze": true}
	var result []ensemble.Resolver
	for _, resolver := range resolvers {
		if resolver != nil && allowed[resolver.Name()] {
			result = append(result, resolver)
		}
	}
	return result
}

func (p *Processor) resolverTimeout() time.Duration {
	timeout := p.Config.ResolverTimeout()
	if timeout <= 0 {
		return 10 * time.Second
	}
	return timeout
}

func aggregateMatches(decision ensemble.Decision) []model.Match {
	result := make([]model.Match, 0, len(decision.Candidates))
	for _, candidate := range decision.Candidates {
		result = append(result, model.Match{ID: candidate.TMDBID, Name: candidate.Identity.Title, Year: candidate.Identity.Year, Score: candidate.TotalScore, Breakdown: aggregateBreakdown(candidate)})
	}
	if len(result) > 0 {
		result[0].Margin = decision.Margin
	}
	return result
}

func aggregateBreakdown(candidate ensemble.AggregateCandidate) []string {
	families := make([]string, 0, len(candidate.FamilyScores))
	for family := range candidate.FamilyScores {
		families = append(families, string(family))
	}
	sort.Strings(families)
	result := make([]string, 0, len(families))
	for _, family := range families {
		result = append(result, fmt.Sprintf("%s=%d", family, candidate.FamilyScores[ensemble.EvidenceFamily(family)]))
	}
	return result
}

func (p *Processor) resolveAIEpisodes(ctx context.Context, kind model.Kind, torrentName string, tmdbID int, media []model.MediaFile, evidence model.Evidence, result *Result) error {
	if p.AI == nil {
		return fmt.Errorf("%w: resolver is not configured", ErrAI)
	}
	problematic := make(map[string]bool)
	for _, mapping := range result.EpisodeValidation {
		if mapping.State == model.EpisodeUnresolved {
			problematic[mapping.File] = true
		}
	}
	problemMedia := media
	if len(problematic) > 0 {
		problemMedia = nil
		for _, file := range media {
			if problematic[file.Name] {
				problemMedia = append(problemMedia, file)
			}
		}
	}
	files := sampledRelativeFiles(problemMedia, 60, 12000)
	allowed := map[string]bool{}
	for _, file := range files {
		allowed[file] = true
	}
	req := ai.Request{Task: ai.MapEpisodes, Kind: kind, TorrentName: torrentName, Files: files, Parsed: ai.ParsedEvidence{Year: evidence.Year, Episodes: evidence.Episodes}, WebSearch: ai.WebNever}
	seasonNumbers := map[int]bool{0: true}
	for _, file := range media {
		if allowed[file.Name] {
			req.SourceEpisodes = append(req.SourceEpisodes, ai.SourceEpisode{File: file.Name, Season: file.Ref.Season, Episode: file.Ref.Episode, EpisodeTitle: file.EpisodeTitle})
			seasonNumbers[file.Ref.Season] = true
		}
	}
	canonical := map[[2]int]bool{}
	for seasonNumber := range seasonNumbers {
		if seasonNumber < 0 {
			continue
		}
		season, err := p.Metadata.GetSeason(ctx, tmdbID, seasonNumber)
		if err != nil {
			continue
		}
		for _, episode := range season.Episodes {
			req.Episodes = append(req.Episodes, ai.CanonicalEpisode{Season: seasonNumber, Episode: episode.EpisodeNumber, Title: episode.Name})
			canonical[[2]int{seasonNumber, episode.EpisodeNumber}] = true
		}
	}
	mapping, hit, err := p.callAI(ctx, req, result)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAI, err)
	}
	result.AI.CacheHit = hit
	result.AI.Hypothesis = &mapping
	result.AI.WebSearchUsed = mapping.WebSearchUsed
	if mapping.Status != ai.Resolved || mapping.Confidence < p.Config.AI.MinConfidence {
		return ErrUnresolved
	}
	byFile := map[string]ai.EpisodeMapping{}
	for _, proposed := range mapping.EpisodeMappings {
		if proposed.Confidence < p.Config.AI.MinConfidence || !canonical[[2]int{proposed.Season, proposed.Episode}] {
			return ErrUnresolved
		}
		byFile[proposed.SourceFile] = proposed
	}
	for i := range media {
		if proposed, ok := byFile[media[i].Name]; ok {
			media[i].Ref.Season, media[i].Ref.Episode, media[i].Ref.EpisodeEnd = proposed.Season, proposed.Episode, 0
		}
	}
	result.AI.Verified = true
	return nil
}

func (p *Processor) resolveAI(ctx context.Context, kind model.Kind, torrentName string, media []model.MediaFile, evidence model.Evidence, result *Result) (model.Match, model.TVShow, error) {
	if p.AI == nil {
		return model.Match{}, model.TVShow{}, fmt.Errorf("%w: resolver is not configured", ErrAI)
	}
	files := sampledRelativeFiles(media, 60, 12000)
	titles := make([]string, 0, len(evidence.Titles))
	for _, title := range evidence.Titles {
		titles = append(titles, title.Title)
	}
	req := ai.Request{Task: ai.IdentifyMedia, Kind: kind, TorrentName: torrentName, Files: files, Parsed: ai.ParsedEvidence{Titles: titles, Year: evidence.Year, Episodes: evidence.Episodes}, WebSearch: ai.WebSearchPolicy(p.Config.AI.WebSearch)}
	for _, candidate := range result.Candidates {
		req.Candidates = append(req.Candidates, ai.Candidate{ID: candidate.ID, Title: candidate.Name, Year: candidate.Year})
	}
	hypothesis, cacheHit, err := p.callAI(ctx, req, result)
	if err != nil {
		return model.Match{}, model.TVShow{}, fmt.Errorf("%w: %v", ErrAI, err)
	}
	result.AI.CacheHit = cacheHit
	result.AI.Hypothesis = &hypothesis
	result.AI.WebSearchUsed = hypothesis.WebSearchUsed
	if hypothesis.Status != ai.Resolved || hypothesis.Confidence < p.Config.AI.MinConfidence {
		return model.Match{}, model.TVShow{}, ErrUnresolved
	}
	queries := append([]string{}, hypothesis.SearchQueries...)
	if hypothesis.CanonicalTitle != "" {
		queries = append(queries, hypothesis.CanonicalTitle)
	}
	queries = append(queries, hypothesis.LocalizedTitles...)
	queries = uniqueQueries(queries)
	if len(queries) == 0 {
		return model.Match{}, model.TVShow{}, ErrUnresolved
	}
	verifiedEvidence := evidence
	for _, query := range queries {
		verifiedEvidence.Titles = append(verifiedEvidence.Titles, model.WeightedTitle{Title: query, Weight: 1})
	}
	if kind == model.KindMovie {
		candidates, err := p.searchMovies(ctx, queries)
		if err != nil {
			return model.Match{}, model.TVShow{}, fmt.Errorf("%w: %v", ErrMetadata, err)
		}
		years, err := p.validateTopMovieReleaseYears(ctx, evidence, candidates)
		if err != nil {
			return model.Match{}, model.TVShow{}, fmt.Errorf("%w: %v", ErrMetadata, err)
		}
		match, all := matcher.Movie(verifiedEvidence, candidates, years, p.Config.Matching.MinScore, p.Config.Matching.MinMargin)
		result.Candidates = all
		if match.ID == 0 {
			match = p.aiSelectCandidate(ctx, req, all, result)
		}
		if match.ID == 0 || !movieAIAnchor(evidence, match, years[match.ID]) {
			return model.Match{}, model.TVShow{}, ErrUnresolved
		}
		movie, err := p.Metadata.GetMovie(ctx, match.ID)
		if err != nil || movie.ID != match.ID {
			return model.Match{}, model.TVShow{}, fmt.Errorf("%w: verify AI movie %d: %v", ErrMetadata, match.ID, err)
		}
		match.Name, match.Year = movie.Title, year(movie.ReleaseDate)
		result.AI.Verified = true
		return match, model.TVShow{}, nil
	}
	candidates, err := p.searchTV(ctx, queries)
	if err != nil {
		return model.Match{}, model.TVShow{}, fmt.Errorf("%w: %v", ErrMetadata, err)
	}
	match, all, err := matcher.TV(ctx, p.Metadata, verifiedEvidence, candidates, p.Config.Matching.MinScore, p.Config.Matching.MinMargin)
	if err != nil {
		return model.Match{}, model.TVShow{}, err
	}
	result.Candidates = all
	if match.ID == 0 {
		match = p.aiSelectCandidate(ctx, req, all, result)
	}
	if match.ID == 0 {
		return model.Match{}, model.TVShow{}, ErrUnresolved
	}
	show, err := p.Metadata.GetTV(ctx, match.ID)
	if err != nil || show.ID != match.ID {
		return model.Match{}, show, fmt.Errorf("%w: verify AI show %d: %v", ErrMetadata, match.ID, err)
	}
	if !tvAIAnchor(ctx, p.Metadata, evidence, show.ID) {
		return model.Match{}, show, ErrUnresolved
	}
	match.Name, match.Year = show.Name, year(show.FirstAirDate)
	result.AI.Verified = true
	return match, show, nil
}

func (p *Processor) callAI(ctx context.Context, req ai.Request, result *Result) (ai.Result, bool, error) {
	result.AI.Used, result.AI.Provider, result.AI.Model, result.AI.PromptVersion, result.AI.WebSearchPolicy = true, p.AIProvider, p.AIModel, ai.PromptVersion, string(req.WebSearch)
	key, err := ai.Fingerprint(req, p.AIProvider, p.AIModel)
	if err != nil {
		return ai.Result{}, false, err
	}
	cache := ai.Cache{Directory: p.Config.State.Directory}
	if p.Config.AI.Cache {
		if cached, hit, err := cache.Load(key); err != nil {
			return ai.Result{}, false, err
		} else if hit {
			if err := ai.Validate(req, cached); err != nil {
				return ai.Result{}, false, err
			}
			result.AI.ActualModel = cached.ActualModel
			return cached, true, nil
		}
	}
	result.AI.Calls++
	resolved, err := p.AI.Resolve(ctx, req)
	if err != nil {
		result.AI.ProviderRequests += ai.ProviderRequestsFromError(err)
		if diagnostics, ok := ai.ProviderHTTPDiagnostics(err); ok {
			result.AI.HTTPStatus = diagnostics.StatusCode
			result.AI.ProviderErrorCode = diagnostics.ErrorCode
			result.AI.RetryAfterSeconds = diagnostics.RetryAfterSeconds
			if diagnostics.Provider != "" {
				result.AI.Provider = diagnostics.Provider
			}
		}
		if diagnostics, ok := ai.ProviderOutputDiagnostics(err); ok {
			if diagnostics.ConfiguredModel != "" {
				result.AI.Model = diagnostics.ConfiguredModel
			}
			result.AI.ActualModel = diagnostics.ActualModel
			result.AI.FinishReason = diagnostics.FinishReason
			result.AI.CompletionTokens = diagnostics.CompletionTokens
			result.AI.ReasoningTokens = diagnostics.ReasoningTokens
		}
		return ai.Result{}, false, err
	}
	result.AI.ProviderRequests += resolved.ProviderRequests
	result.AI.ActualModel = resolved.ActualModel
	if err := ai.Validate(req, resolved); err != nil {
		return ai.Result{}, false, err
	}
	if p.Config.AI.Cache && !isDryRun(ctx) {
		if err := cache.Save(key, p.AIProvider, p.AIModel, req, resolved); err != nil {
			return ai.Result{}, false, err
		}
	}
	return resolved, false, nil
}

func isDryRun(ctx context.Context) bool {
	dry, _ := ctx.Value(dryRunContextKey{}).(bool)
	return dry
}

func (p *Processor) aiSelectCandidate(ctx context.Context, base ai.Request, matches []model.Match, result *Result) model.Match {
	if len(matches) < 2 || result.AI.Calls >= 2 {
		return model.Match{}
	}
	req := base
	req.Task = ai.SelectCandidate
	req.WebSearch = ai.WebNever
	req.Candidates = nil
	byID := map[int]model.Match{}
	for i, match := range matches {
		if i == 5 {
			break
		}
		req.Candidates = append(req.Candidates, ai.Candidate{ID: match.ID, Title: match.Name, Year: match.Year})
		byID[match.ID] = match
	}
	selection, _, err := p.callAI(ctx, req, result)
	if err != nil || selection.Status != ai.Resolved || selection.Confidence < p.Config.AI.MinConfidence || selection.SelectedTMDBID == nil {
		return model.Match{}
	}
	return byID[*selection.SelectedTMDBID]
}

func movieAIAnchor(e model.Evidence, match model.Match, releaseYear bool) bool {
	return e.Year != 0 && (e.Year == match.Year || releaseYear)
}
func tvAIAnchor(ctx context.Context, metadata MetadataProvider, e model.Evidence, id int) bool {
	for _, ref := range e.Episodes {
		if ref.Season < 0 || ref.Episode <= 0 {
			continue
		}
		season, err := metadata.GetSeason(ctx, id, ref.Season)
		if err != nil {
			continue
		}
		for _, episode := range season.Episodes {
			if episode.EpisodeNumber == ref.Episode {
				return true
			}
		}
	}
	return false
}
func uniqueQueries(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, q := range in {
		key := release.NormalizeTitle(q)
		if key != "" && !seen[key] {
			seen[key] = true
			out = append(out, q)
		}
	}
	return out
}
func sampledRelativeFiles(media []model.MediaFile, maxFiles, maxChars int) []string {
	if len(media) == 0 {
		return nil
	}
	indexes := []int{}
	if len(media) <= maxFiles {
		for i := range media {
			indexes = append(indexes, i)
		}
	} else {
		for i := 0; i < maxFiles; i++ {
			indexes = append(indexes, i*(len(media)-1)/(maxFiles-1))
		}
	}
	out, chars := []string{}, 0
	for _, i := range indexes {
		name := media[i].Name
		if chars+len(name) > maxChars {
			break
		}
		out = append(out, name)
		chars += len(name)
	}
	return out
}

func (p *Processor) validateEpisodes(ctx context.Context, tmdbID int, files []model.MediaFile) ([]model.EpisodeValidation, bool) {
	return p.validateEpisodesForKind(ctx, model.KindTV, tmdbID, files)
}

func (p *Processor) validateEpisodesForKind(ctx context.Context, kind model.Kind, tmdbID int, files []model.MediaFile) ([]model.EpisodeValidation, bool) {
	seasons := map[int]map[int]bool{}
	seasonFailed := map[int]bool{}
	for _, file := range files {
		if file.Ref.Season < 0 || file.Ref.Episode <= 0 {
			continue
		}
		if _, loaded := seasons[file.Ref.Season]; loaded || seasonFailed[file.Ref.Season] {
			continue
		}
		detail, err := p.Metadata.GetSeason(ctx, tmdbID, file.Ref.Season)
		if err != nil {
			seasonFailed[file.Ref.Season] = true
			continue
		}
		episodes := map[int]bool{}
		for _, episode := range detail.Episodes {
			episodes[episode.EpisodeNumber] = true
		}
		seasons[file.Ref.Season] = episodes
	}
	var specials model.Season
	specialsLoaded := false
	validations := make([]model.EpisodeValidation, 0, len(files))
	for i := range files {
		file := &files[i]
		parsedSeason, parsedEpisode := file.Ref.Season, file.Ref.Episode
		validation := model.EpisodeValidation{File: file.Name, EpisodeTitle: file.EpisodeTitle, ParsedSeason: parsedSeason, ParsedEpisode: parsedEpisode, Season: file.Ref.Season, Episode: file.Ref.Episode, EpisodeEnd: file.Ref.EpisodeEnd, State: model.EpisodeResolved}
		end := file.Ref.EpisodeEnd
		if end < file.Ref.Episode {
			end = file.Ref.Episode
		}
		if file.Ref.Season < 0 || file.Ref.Episode <= 0 || seasonFailed[file.Ref.Season] {
			validation.State = model.EpisodeUnresolved
		}
		if validation.State == model.EpisodeResolved {
			var missingEpisodes []int
			for episode := file.Ref.Episode; episode <= end; episode++ {
				if !seasons[file.Ref.Season][episode] {
					missingEpisodes = append(missingEpisodes, episode)
				}
			}
			if len(missingEpisodes) > 0 && file.Ref.EpisodeEnd <= file.Ref.Episode && file.EpisodeTitle != "" {
				if !specialsLoaded {
					specials, _ = p.Metadata.GetSeason(ctx, tmdbID, 0)
					specialsLoaded = true
				}
				if special, ok := findSpecialEpisode(file.EpisodeTitle, specials.Episodes); ok {
					file.Ref.Season, file.Ref.Episode, file.Ref.EpisodeEnd = 0, special.EpisodeNumber, 0
					validation.Season, validation.Episode, validation.EpisodeEnd, validation.Remapped = 0, special.EpisodeNumber, 0, true
					missingEpisodes = nil
					validation.ProviderEvidence = append(validation.ProviderEvidence, "tmdb_special_title_exact")
				}
				if len(missingEpisodes) > 0 && p.EpisodeCatalog != nil {
					season, episode, providerEvidence, ok, _ := p.EpisodeCatalog.MapEpisode(ctx, tmdbID, *file)
					if ok && p.canonicalEpisodeExists(ctx, tmdbID, season, episode) {
						file.Ref.Season, file.Ref.Episode, file.Ref.EpisodeEnd = season, episode, 0
						validation.Season, validation.Episode, validation.EpisodeEnd, validation.Remapped = season, episode, 0, true
						validation.ProviderEvidence = append(validation.ProviderEvidence, providerEvidence)
						missingEpisodes = nil
					}
				}
			}
			if len(missingEpisodes) > 0 {
				validation.MissingEpisodes = missingEpisodes
				validation.State = model.EpisodeUnresolved
			}
		}
		validations = append(validations, validation)
	}

	resolved := 0
	recognized := 0
	resolvedBySeason := make(map[int]int)
	patterns := make(map[string]int)
	for i, validation := range validations {
		if validation.ParsedEpisode > 0 && validation.ParsedSeason >= 0 {
			recognized++
		}
		if validation.State == model.EpisodeResolved {
			resolved++
			resolvedBySeason[validation.ParsedSeason]++
			patterns[episodeNamingPattern(files[i].Name)]++
		}
	}
	for i := range validations {
		validation := &validations[i]
		if validation.State != model.EpisodeUnresolved || validation.ParsedSeason < 0 || validation.ParsedEpisode <= 0 || validation.EpisodeEnd > validation.Episode {
			if validation.State == model.EpisodeUnresolved && validation.Reason == "" {
				validation.Reason = "source numbering or canonical episode could not be verified"
			}
			continue
		}
		siblingStrong := resolved >= 2 && recognized > 0 && resolved*100 >= 70*recognized
		sameSeason := resolvedBySeason[validation.ParsedSeason] >= 2
		samePattern := patterns[episodeNamingPattern(files[i].Name)] >= 2
		context := provisionalContext(siblingStrong, sameSeason, samePattern)
		validation.ContextEvidence, validation.ContextScore = context.labels, context.score
		conflict, providerEvidence := p.provisionalFileConflict(ctx, kind, tmdbID, files[i])
		validation.ProviderEvidence = append(validation.ProviderEvidence, providerEvidence...)
		if siblingStrong && sameSeason && samePattern && !conflict {
			validation.State = model.EpisodeProvisional
			validation.Reason = "canonical episode is not yet available; source numbering is supported by sibling context"
			validation.Season, validation.Episode = validation.ParsedSeason, validation.ParsedEpisode
			files[i].Ref.Season, files[i].Ref.Episode = validation.Season, validation.Episode
		} else if conflict {
			validation.Reason = "file identity conflicts with the accepted show"
		} else {
			validation.Reason = "insufficient sibling/season/release context for provisional mapping"
		}
	}
	return validations, !hasUnresolvedMappings(validations)
}

func (p *Processor) canonicalEpisodeExists(ctx context.Context, tmdbID, seasonNumber, episodeNumber int) bool {
	if seasonNumber < 0 || episodeNumber <= 0 {
		return false
	}
	season, err := p.Metadata.GetSeason(ctx, tmdbID, seasonNumber)
	if err != nil {
		return false
	}
	for _, episode := range season.Episodes {
		if episode.EpisodeNumber == episodeNumber {
			return true
		}
	}
	return false
}

type contextScore struct {
	labels []string
	score  int
}

func provisionalContext(siblingStrong, sameSeason, samePattern bool) contextScore {
	result := contextScore{}
	if siblingStrong {
		result.labels = append(result.labels, string(ensemble.EvidenceSiblingFilesSameShowStrong))
		result.score += ensemble.PointsSiblingFilesSameShowStrong
	}
	if sameSeason {
		result.labels = append(result.labels, string(ensemble.EvidenceSameSeasonContext))
		result.score += ensemble.PointsSameSeasonContext
	}
	if samePattern {
		result.labels = append(result.labels, string(ensemble.EvidenceSameReleaseNamingPattern))
		result.score += ensemble.PointsSameReleaseNamingPattern
	}
	if result.score > ensemble.FamilyCap(ensemble.FamilyContext) {
		result.score = ensemble.FamilyCap(ensemble.FamilyContext)
	}
	return result
}

var episodePatternRE = regexp.MustCompile(`(?i)s\d{1,2}e\d{1,3}(?:-e\d{1,3})?`)

func episodeNamingPattern(name string) string {
	base := strings.ToLower(filepath.Base(name))
	return episodePatternRE.ReplaceAllString(base, "s00e00")
}

func (p *Processor) provisionalFileConflict(ctx context.Context, kind model.Kind, tmdbID int, file model.MediaFile) (bool, []string) {
	for _, resolver := range p.Resolvers {
		if resolver == nil || resolver.Name() != "opensubtitles" || !resolver.Supports(kind) {
			continue
		}
		result := resolver.Resolve(ctx, ensemble.ResolveRequest{Kind: kind, Files: []model.MediaFile{file}})
		if result.Status != ensemble.ResolverOK {
			return false, nil
		}
		var details []string
		for _, candidate := range result.Candidates {
			if candidate.Identity.TMDBID > 0 {
				details = append(details, "opensubtitles_file_identity")
				if candidate.Identity.TMDBID != tmdbID {
					return true, details
				}
			}
		}
		return false, details
	}
	return false, nil
}

func hasUnresolvedMappings(validations []model.EpisodeValidation) bool {
	for _, validation := range validations {
		if validation.State == model.EpisodeUnresolved {
			return true
		}
	}
	return false
}

func safeMappingCount(validations []model.EpisodeValidation) int {
	count := 0
	for _, validation := range validations {
		if validation.State == model.EpisodeResolved || validation.State == model.EpisodeProvisional {
			count++
		}
	}
	return count
}

func mappingStatus(validations []model.EpisodeValidation) model.MappingStatus {
	hasProvisional, hasUnresolved := false, false
	for _, validation := range validations {
		hasProvisional = hasProvisional || validation.State == model.EpisodeProvisional
		hasUnresolved = hasUnresolved || validation.State == model.EpisodeUnresolved
	}
	if hasUnresolved {
		return model.MappingPartial
	}
	if hasProvisional {
		return model.MappingResolvedWithWarnings
	}
	return model.MappingResolved
}

func mappingsBySource(validations []model.EpisodeValidation, source string, media []model.MediaFile) *model.EpisodeValidation {
	for _, file := range media {
		if file.Source != source {
			continue
		}
		for i := range validations {
			if validations[i].File == file.Name {
				return &validations[i]
			}
		}
	}
	return nil
}

func findSpecialEpisode(title string, episodes []model.Episode) (model.Episode, bool) {
	type scoredEpisode struct {
		episode model.Episode
		score   int
	}
	scored := make([]scoredEpisode, 0, len(episodes))
	for _, episode := range episodes {
		scored = append(scored, scoredEpisode{episode: episode, score: specialTitleScore(title, episode.Name)})
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	if len(scored) == 0 || scored[0].score < 90 {
		return model.Episode{}, false
	}
	second := 0
	if len(scored) > 1 {
		second = scored[1].score
	}
	if scored[0].score-second < 10 {
		return model.Episode{}, false
	}
	return scored[0].episode, true
}

func specialTitleScore(left, right string) int {
	a, b := release.NormalizeTitle(left), release.NormalizeTitle(right)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 100
	}
	at, bt := strings.Fields(a), strings.Fields(b)
	counts := map[string]int{}
	for _, token := range at {
		counts[token]++
	}
	common := 0
	for _, token := range bt {
		if counts[token] > 0 {
			common++
			counts[token]--
		}
	}
	return 200 * common / (len(at) + len(bt))
}

func unresolvedEpisodeSummary(validations []model.EpisodeValidation) string {
	var unresolved []string
	for _, validation := range validations {
		if validation.State == model.EpisodeUnresolved {
			unresolved = append(unresolved, fmt.Sprintf("%s (S%02dE%02d)", validation.File, validation.Season, validation.Episode))
		}
	}
	return strings.Join(unresolved, ", ")
}
func (p *Processor) resolve(ctx context.Context, kind model.Kind, e model.Evidence, id int, r *Result) (model.Match, model.TVShow, error) {
	queries := titleQueries(e)
	if len(queries) == 0 {
		return model.Match{}, model.TVShow{}, ErrUnresolved
	}
	if kind == model.KindMovie {
		if id > 0 {
			m, err := p.Metadata.GetMovie(ctx, id)
			if err != nil {
				return model.Match{}, model.TVShow{}, fmt.Errorf("%w: %v", ErrMetadata, err)
			}
			return model.Match{ID: m.ID, Name: m.Title, Year: year(m.ReleaseDate), Score: 999}, model.TVShow{}, nil
		}
		cs, err := p.searchMovies(ctx, queries)
		if err != nil {
			return model.Match{}, model.TVShow{}, fmt.Errorf("%w: %v", ErrMetadata, err)
		}
		validatedYears, err := p.validateTopMovieReleaseYears(ctx, e, cs)
		if err != nil {
			return model.Match{}, model.TVShow{}, fmt.Errorf("%w: %v", ErrMetadata, err)
		}
		m, all := matcher.Movie(e, cs, validatedYears, p.Config.Matching.MinScore, p.Config.Matching.MinMargin)
		r.Candidates = all
		if m.ID == 0 {
			return m, model.TVShow{}, ErrUnresolved
		}
		return m, model.TVShow{}, nil
	}
	if id > 0 {
		s, err := p.Metadata.GetTV(ctx, id)
		if err != nil {
			return model.Match{}, s, fmt.Errorf("%w: %v", ErrMetadata, err)
		}
		return model.Match{ID: s.ID, Name: s.Name, Year: year(s.FirstAirDate), Score: 999}, s, nil
	}
	cs, err := p.searchTV(ctx, queries)
	if err != nil {
		return model.Match{}, model.TVShow{}, fmt.Errorf("%w: %v", ErrMetadata, err)
	}
	m, all, err := matcher.TV(ctx, p.Metadata, e, cs, p.Config.Matching.MinScore, p.Config.Matching.MinMargin)
	r.Candidates = all
	if err != nil {
		return m, model.TVShow{}, err
	}
	if m.ID == 0 {
		return m, model.TVShow{}, ErrUnresolved
	}
	s, err := p.Metadata.GetTV(ctx, m.ID)
	if err != nil {
		return m, s, fmt.Errorf("%w: %v", ErrMetadata, err)
	}
	return m, s, nil
}

func (p *Processor) validateTopMovieReleaseYears(ctx context.Context, evidence model.Evidence, candidates []model.MovieCandidate) (map[int]bool, error) {
	validated := map[int]bool{}
	if evidence.Year == 0 {
		return validated, nil
	}
	initial := matcher.ScoreMovies(evidence, candidates, nil)
	limit := 3
	if len(initial) < limit {
		limit = len(initial)
	}
	for _, candidate := range initial[:limit] {
		if candidate.Year == 0 || candidate.Year == evidence.Year {
			continue
		}
		dates, err := p.Metadata.GetMovieReleaseDates(ctx, candidate.ID)
		if err != nil {
			return nil, fmt.Errorf("fetch release dates for movie %d: %w", candidate.ID, err)
		}
		validated[candidate.ID] = hasReleaseYear(dates, evidence.Year)
	}
	return validated, nil
}

func hasReleaseYear(dates model.MovieReleaseDates, sourceYear int) bool {
	for _, country := range dates.Results {
		for _, releaseDate := range country.ReleaseDates {
			if releaseDate.Type < 1 || releaseDate.Type > 6 {
				continue
			}
			if len(releaseDate.ReleaseDate) >= 4 && year(releaseDate.ReleaseDate) == sourceYear {
				return true
			}
		}
	}
	return false
}

func titleQueries(e model.Evidence) []string {
	seen := map[string]bool{}
	queries := make([]string, 0, len(e.Titles))
	for _, title := range e.Titles {
		key := release.NormalizeTitle(title.Title)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		queries = append(queries, title.Title)
	}
	return queries
}

func (p *Processor) searchTV(ctx context.Context, queries []string) ([]model.TVCandidate, error) {
	seen := map[int]bool{}
	var combined []model.TVCandidate
	var firstErr error
	for _, query := range queries {
		results, err := p.Metadata.SearchTV(ctx, query)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, candidate := range results {
			if !seen[candidate.ID] {
				seen[candidate.ID] = true
				combined = append(combined, candidate)
			}
		}
	}
	if len(combined) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return combined, nil
}

func (p *Processor) searchMovies(ctx context.Context, queries []string) ([]model.MovieCandidate, error) {
	seen := map[int]bool{}
	var combined []model.MovieCandidate
	var firstErr error
	for _, query := range queries {
		results, err := p.Metadata.SearchMovie(ctx, query)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, candidate := range results {
			if !seen[candidate.ID] {
				seen[candidate.ID] = true
				combined = append(combined, candidate)
			}
		}
	}
	if len(combined) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return combined, nil
}
func (p *Processor) kind(path string) (model.Kind, string, string, bool) {
	types := []model.Kind{model.KindTV, model.KindMovie, model.KindAnime}
	sort.SliceStable(types, func(i, j int) bool {
		a, _ := p.Config.Paths.Roots(types[i])
		b, _ := p.Config.Paths.Roots(types[j])
		return len(a) > len(b)
	})
	for _, k := range types {
		s, t := p.Config.Paths.Roots(k)
		if pathutil.Contains(s, path) {
			return k, s, t, true
		}
	}
	return "", "", "", false
}
func (p *Processor) saveUnresolved(hash string, r Result) error {
	dir := filepath.Join(p.Config.State.Directory, "unresolved")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, strings.ToLower(hash)+".json"), b, 0o600)
}
func year(s string) int {
	var y int
	if len(s) >= 4 {
		fmt.Sscanf(s[:4], "%d", &y)
	}
	return y
}
