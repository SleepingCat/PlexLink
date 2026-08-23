package model

type Kind string

const (
	KindTV    Kind = "tv"
	KindMovie Kind = "movie"
	KindAnime Kind = "anime"
)

type Torrent struct {
	Hash        string
	Name        string
	ContentPath string
	SavePath    string
	Category    string
	Tags        string
	Progress    float64
	State       string
}

type TorrentFile struct {
	Name     string
	Size     int64
	Priority int
	Progress float64
}

type EpisodeRef struct {
	Season     int
	Episode    int
	EpisodeEnd int
	Absolute   bool
}

type MediaFile struct {
	Source       string
	Name         string
	EpisodeTitle string
	Ref          EpisodeRef
}

type Evidence struct {
	Titles   []WeightedTitle
	Year     int
	Episodes []EpisodeRef
}

type WeightedTitle struct {
	Title  string
	Weight int
}

type TVCandidate struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
	FirstAirDate string `json:"first_air_date"`
}

type MovieCandidate struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	ReleaseDate   string `json:"release_date"`
}

type ExternalFindResult struct {
	MovieResults []MovieCandidate `json:"movie_results"`
	TVResults    []TVCandidate    `json:"tv_results"`
}

type TVExternalIDs struct {
	IMDbID string `json:"imdb_id"`
}

type TVShow struct {
	ID           int             `json:"id"`
	Name         string          `json:"name"`
	OriginalName string          `json:"original_name"`
	FirstAirDate string          `json:"first_air_date"`
	Seasons      []SeasonSummary `json:"seasons"`
}

type SeasonSummary struct {
	SeasonNumber int `json:"season_number"`
	EpisodeCount int `json:"episode_count"`
}

type Season struct {
	ID           int       `json:"id"`
	SeasonNumber int       `json:"season_number"`
	Episodes     []Episode `json:"episodes"`
}

type Episode struct {
	ID            int    `json:"id"`
	EpisodeNumber int    `json:"episode_number"`
	Name          string `json:"name"`
}

type Movie struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	ReleaseDate   string `json:"release_date"`
}

type MovieReleaseDates struct {
	Results []MovieReleaseCountry `json:"results"`
}

type MovieReleaseCountry struct {
	CountryCode  string             `json:"iso_3166_1"`
	ReleaseDates []MovieReleaseDate `json:"release_dates"`
}

type MovieReleaseDate struct {
	Type        int    `json:"type"`
	ReleaseDate string `json:"release_date"`
}

type Match struct {
	ID        int
	Name      string
	Year      int
	Score     int
	Margin    int
	Breakdown []string
}

type LinkPlan struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type EpisodeValidationState string

const (
	EpisodeResolved    EpisodeValidationState = "RESOLVED"
	EpisodeProvisional EpisodeValidationState = "PROVISIONAL"
	EpisodeUnresolved  EpisodeValidationState = "UNRESOLVED"
	EpisodeIgnored     EpisodeValidationState = "IGNORED"
	// EpisodeValid is retained as a source-compatible alias.
	EpisodeValid = EpisodeResolved
)

type MappingStatus string

const (
	MappingResolved             MappingStatus = "RESOLVED"
	MappingResolvedWithWarnings MappingStatus = "RESOLVED_WITH_WARNINGS"
	MappingPartial              MappingStatus = "PARTIAL"
	MappingConflict             MappingStatus = "CONFLICT"
)

type EpisodeValidation struct {
	File              string                 `json:"file"`
	EpisodeTitle      string                 `json:"episode_title,omitempty"`
	ParsedSeason      int                    `json:"parsed_season"`
	ParsedEpisode     int                    `json:"parsed_episode"`
	Season            int                    `json:"season"`
	Episode           int                    `json:"episode"`
	EpisodeEnd        int                    `json:"episode_end,omitempty"`
	Remapped          bool                   `json:"remapped,omitempty"`
	CanonicalVerified bool                   `json:"canonical_verified"`
	State             EpisodeValidationState `json:"state"`
	MissingEpisodes   []int                  `json:"missing_episodes,omitempty"`
	Reason            string                 `json:"reason,omitempty"`
	ContextEvidence   []string               `json:"context_evidence,omitempty"`
	ContextScore      int                    `json:"context_score,omitempty"`
	ProviderEvidence  []string               `json:"provider_evidence,omitempty"`
	PlannedTarget     string                 `json:"planned_target,omitempty"`
}
