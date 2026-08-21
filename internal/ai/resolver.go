package ai

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/SleepingCat/PlexLink/internal/model"
)

const PromptVersion = "plexlink-media-resolver-v1"

type Task string

const (
	IdentifyMedia   Task = "identify_media"
	SelectCandidate Task = "select_candidate"
	MapEpisodes     Task = "map_episodes"
)

type Status string

const (
	Resolved  Status = "resolved"
	Ambiguous Status = "ambiguous"
	Unknown   Status = "unknown"
)

type WebSearchPolicy string

const (
	WebNever   WebSearchPolicy = "never"
	WebAllow   WebSearchPolicy = "allow"
	WebRequire WebSearchPolicy = "require"
)

type Capabilities struct {
	StructuredOutput              bool
	WebSearch                     bool
	StructuredOutputWithWebSearch bool
}

type Resolver interface {
	Resolve(context.Context, Request) (Result, error)
	Capabilities() Capabilities
}

type ParsedEvidence struct {
	Titles   []string           `json:"titles"`
	Year     int                `json:"year,omitempty"`
	Episodes []model.EpisodeRef `json:"episodes,omitempty"`
}

type Candidate struct {
	ID            int    `json:"tmdb_id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title,omitempty"`
	Year          int    `json:"year,omitempty"`
}

type SourceEpisode struct {
	File         string `json:"source_file"`
	Season       int    `json:"season"`
	Episode      int    `json:"episode"`
	EpisodeTitle string `json:"episode_title,omitempty"`
}

type CanonicalEpisode struct {
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	Title   string `json:"title,omitempty"`
}

type Request struct {
	Task           Task               `json:"task"`
	Kind           model.Kind         `json:"media_type"`
	TorrentName    string             `json:"torrent_name"`
	Files          []string           `json:"relative_media_files"`
	Parsed         ParsedEvidence     `json:"parsed_evidence"`
	Candidates     []Candidate        `json:"candidates,omitempty"`
	SourceEpisodes []SourceEpisode    `json:"source_episodes,omitempty"`
	Episodes       []CanonicalEpisode `json:"canonical_episodes,omitempty"`
	WebSearch      WebSearchPolicy    `json:"-"`
}

type EpisodeMapping struct {
	SourceFile string  `json:"source_file"`
	Season     int     `json:"season"`
	Episode    int     `json:"episode"`
	Confidence float64 `json:"confidence"`
}

type Result struct {
	Status           Status           `json:"status"`
	MediaType        model.Kind       `json:"media_type"`
	CanonicalTitle   string           `json:"canonical_title"`
	LocalizedTitles  []string         `json:"localized_titles"`
	Year             int              `json:"year"`
	Season           int              `json:"season"`
	SearchQueries    []string         `json:"search_queries"`
	SelectedTMDBID   *int             `json:"selected_tmdb_id"`
	EpisodeMappings  []EpisodeMapping `json:"episode_mappings"`
	Confidence       float64          `json:"confidence"`
	EvidenceSummary  []string         `json:"evidence_summary"`
	WebSearchUsed    *bool            `json:"-"`
	ProviderRequests int              `json:"-"`
	ActualModel      string           `json:"-"`
}

var (
	ErrInvalidResult         = errors.New("invalid AI result")
	ErrProviderOutput        = errors.New("invalid AI provider output")
	ErrUnsupportedCapability = errors.New("unsupported AI capability")
)

type ProviderRequestError struct {
	Err      error
	Requests int
}

type ProviderOutputError struct {
	Err              error
	ConfiguredModel  string
	ActualModel      string
	FinishReason     string
	CompletionTokens int
	ReasoningTokens  int
}

func (e *ProviderOutputError) Error() string { return e.Err.Error() }
func (e *ProviderOutputError) Unwrap() error { return e.Err }

func ProviderOutputDiagnostics(err error) (ProviderOutputError, bool) {
	var outputErr *ProviderOutputError
	if !errors.As(err, &outputErr) {
		return ProviderOutputError{}, false
	}
	return *outputErr, true
}

func (e *ProviderRequestError) Error() string { return e.Err.Error() }
func (e *ProviderRequestError) Unwrap() error { return e.Err }

func WithProviderRequests(err error, requests int) error {
	if err == nil || requests == 0 {
		return err
	}
	return &ProviderRequestError{Err: err, Requests: requests}
}

func ProviderRequestsFromError(err error) int {
	var providerErr *ProviderRequestError
	if errors.As(err, &providerErr) {
		return providerErr.Requests
	}
	return 0
}

func Validate(req Request, result Result) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidResult, fmt.Sprintf(format, args...))
	}
	if result.Status != Resolved && result.Status != Ambiguous && result.Status != Unknown {
		return bad("unknown status %q", result.Status)
	}
	if result.MediaType != req.Kind {
		return bad("media type %q does not match request", result.MediaType)
	}
	if result.Confidence < 0 || result.Confidence > 1 {
		return bad("confidence is outside 0..1")
	}
	if len(result.SearchQueries) > 8 || len(result.LocalizedTitles) > 8 || len(result.EvidenceSummary) > 8 || len(result.EpisodeMappings) > 200 {
		return bad("output exceeds bounds")
	}
	for _, query := range result.SearchQueries {
		if strings.TrimSpace(query) == "" || len(query) > 200 {
			return bad("invalid search query")
		}
	}
	allowedIDs := map[int]bool{}
	for _, candidate := range req.Candidates {
		allowedIDs[candidate.ID] = true
	}
	if req.Task == SelectCandidate {
		if result.SelectedTMDBID != nil && !allowedIDs[*result.SelectedTMDBID] {
			return bad("selected TMDB ID is outside supplied candidates")
		}
	} else if result.SelectedTMDBID != nil {
		return bad("TMDB ID is not allowed for task %s", req.Task)
	}
	allowedFiles := map[string]bool{}
	for _, file := range req.Files {
		allowedFiles[file] = true
	}
	seen := map[string]bool{}
	for _, mapping := range result.EpisodeMappings {
		if req.Task != MapEpisodes || !allowedFiles[mapping.SourceFile] || seen[mapping.SourceFile] || mapping.Season < 0 || mapping.Episode <= 0 || mapping.Confidence < 0 || mapping.Confidence > 1 {
			return bad("invalid or conflicting episode mapping")
		}
		seen[mapping.SourceFile] = true
	}
	return nil
}

func CandidateIDs(candidates []Candidate) []int {
	ids := make([]int, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
	}
	sort.Ints(ids)
	return ids
}
