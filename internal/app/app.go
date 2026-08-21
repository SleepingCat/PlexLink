package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SleepingCat/PlexLink/internal/ai"
	"github.com/SleepingCat/PlexLink/internal/config"
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
	Torrents   TorrentClient
	Metadata   MetadataProvider
	AI         ai.Resolver
	AIProvider string
	AIModel    string
	Config     config.Config
}
type ProcessOptions struct {
	DryRun   bool
	ManualID int
	NoAI     bool
}
type AIDiagnostics struct {
	Used             bool       `json:"ai_used"`
	Provider         string     `json:"provider,omitempty"`
	Model            string     `json:"configured_model,omitempty"`
	ActualModel      string     `json:"actual_model,omitempty"`
	FinishReason     string     `json:"finish_reason,omitempty"`
	CompletionTokens int        `json:"completion_tokens,omitempty"`
	ReasoningTokens  int        `json:"reasoning_tokens,omitempty"`
	PromptVersion    string     `json:"prompt_version,omitempty"`
	WebSearchPolicy  string     `json:"web_search_policy,omitempty"`
	WebSearchUsed    *bool      `json:"web_search_used,omitempty"`
	CacheHit         bool       `json:"cache_hit"`
	Calls            int        `json:"calls"`
	ProviderRequests int        `json:"provider_requests"`
	Hypothesis       *ai.Result `json:"hypothesis,omitempty"`
	Verified         bool       `json:"final_tmdb_verified"`
}
type Result struct {
	Torrent           model.Torrent             `json:"torrent"`
	Kind              model.Kind                `json:"kind"`
	Evidence          model.Evidence            `json:"evidence"`
	Candidates        []model.Match             `json:"candidates"`
	Match             model.Match               `json:"match"`
	EpisodeValidation []model.EpisodeValidation `json:"episode_validation,omitempty"`
	Plan              []model.LinkPlan          `json:"plan"`
	Actions           []linker.Action           `json:"actions"`
	AI                AIDiagnostics             `json:"ai"`
}

func (p *Processor) Process(ctx context.Context, hash string, dry bool, manualID int) (Result, error) {
	return p.ProcessWithOptions(ctx, hash, ProcessOptions{DryRun: dry, ManualID: manualID})
}

func (p *Processor) ProcessWithOptions(ctx context.Context, hash string, options ProcessOptions) (Result, error) {
	dry, manualID := options.DryRun, options.ManualID
	explicitID := manualID
	if manualID == 0 {
		var err error
		manualID, err = state.Resolution(p.Config.State.Directory, hash)
		if err != nil {
			return Result{}, err
		}
	}
	t, err := p.Torrents.GetTorrent(ctx, hash)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrTorrent, err)
	}
	r := Result{Torrent: t}
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
	match, show, err := p.resolve(ctx, kind, e, manualID, &r)
	if errors.Is(err, ErrUnresolved) && manualID == 0 && !options.NoAI && p.Config.AI.Enabled {
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
		validations, valid := p.validateEpisodes(ctx, match.ID, media)
		r.EpisodeValidation = validations
		if !valid && !options.NoAI && p.Config.AI.Enabled {
			if mapErr := p.resolveAIEpisodes(ctx, kind, t.Name, match.ID, media, e, &r); mapErr != nil {
				if !errors.Is(mapErr, ErrUnresolved) {
					return r, mapErr
				}
			} else {
				validations, valid = p.validateEpisodes(ctx, match.ID, media)
				r.EpisodeValidation = validations
			}
		}
		if !valid {
			if !dry {
				_ = p.saveUnresolved(hash, r)
			}
			return r, fmt.Errorf("%w: unmapped files: %s", ErrUnresolved, unresolvedEpisodeSummary(validations))
		}
	}
	for _, f := range media {
		if kind != model.KindMovie && (f.Ref.Season < 0 || f.Ref.Episode == 0) {
			return r, fmt.Errorf("%w: missing episode numbering for %s", ErrUnresolved, f.Name)
		}
		target, err := plexpath.Build(targetRoot, kind, match, f)
		if err != nil {
			return r, err
		}
		r.Plan = append(r.Plan, model.LinkPlan{Source: f.Source, Target: target})
	}
	for _, plan := range r.Plan {
		action, err := linker.Link(sourceRoot, targetRoot, plan.Source, plan.Target, dry)
		if err != nil {
			return r, err
		}
		r.Actions = append(r.Actions, action)
		if action == linker.Conflict {
			return r, ErrConflict
		}
	}
	if explicitID > 0 && !dry {
		if err := state.SaveResolution(p.Config.State.Directory, hash, explicitID); err != nil {
			return r, fmt.Errorf("save manual resolution: %w", err)
		}
	}
	return r, nil
}

func (p *Processor) resolveAIEpisodes(ctx context.Context, kind model.Kind, torrentName string, tmdbID int, media []model.MediaFile, evidence model.Evidence, result *Result) error {
	if p.AI == nil {
		return fmt.Errorf("%w: resolver is not configured", ErrAI)
	}
	files := sampledRelativeFiles(media, 60, 12000)
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
	if p.Config.AI.Cache {
		if err := cache.Save(key, p.AIProvider, p.AIModel, req, resolved); err != nil {
			return ai.Result{}, false, err
		}
	}
	return resolved, false, nil
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
	allValid := true
	for i := range files {
		file := &files[i]
		parsedSeason, parsedEpisode := file.Ref.Season, file.Ref.Episode
		validation := model.EpisodeValidation{File: file.Name, EpisodeTitle: file.EpisodeTitle, ParsedSeason: parsedSeason, ParsedEpisode: parsedEpisode, Season: file.Ref.Season, Episode: file.Ref.Episode, EpisodeEnd: file.Ref.EpisodeEnd, State: model.EpisodeValid}
		end := file.Ref.EpisodeEnd
		if end < file.Ref.Episode {
			end = file.Ref.Episode
		}
		if file.Ref.Season < 0 || file.Ref.Episode <= 0 || seasonFailed[file.Ref.Season] {
			validation.State = model.EpisodeUnresolved
		}
		if validation.State == model.EpisodeValid {
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
				}
			}
			if len(missingEpisodes) > 0 {
				validation.MissingEpisodes = missingEpisodes
				validation.State = model.EpisodeUnresolved
			}
		}
		if validation.State == model.EpisodeUnresolved {
			allValid = false
		}
		validations = append(validations, validation)
	}
	return validations, allValid
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
