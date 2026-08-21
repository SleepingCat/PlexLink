package app

import (
	"context"
	"errors"

	"github.com/SleepingCat/PlexLink/internal/model"
	"github.com/SleepingCat/PlexLink/internal/resolvers/tvmaze"
)

type EpisodeCatalog interface {
	MapEpisode(context.Context, int, model.MediaFile) (season, episode int, evidence string, ok bool, err error)
}

type tvExternalIDs interface {
	GetTVExternalIDs(context.Context, int) (model.TVExternalIDs, error)
}

type TVMazeEpisodeCatalog struct {
	tmdb   tvExternalIDs
	client *tvmaze.Client
}

func NewTVMazeEpisodeCatalog(tmdb tvExternalIDs, client *tvmaze.Client) *TVMazeEpisodeCatalog {
	return &TVMazeEpisodeCatalog{tmdb: tmdb, client: client}
}

func (c *TVMazeEpisodeCatalog) MapEpisode(ctx context.Context, tmdbID int, file model.MediaFile) (int, int, string, bool, error) {
	if c == nil || c.tmdb == nil || c.client == nil || file.EpisodeTitle == "" {
		return 0, 0, "", false, nil
	}
	ids, err := c.tmdb.GetTVExternalIDs(ctx, tmdbID)
	if err != nil || ids.IMDbID == "" {
		return 0, 0, "", false, err
	}
	show, err := c.client.LookupShowByIMDb(ctx, ids.IMDbID)
	if errors.Is(err, tvmaze.ErrNotFound) {
		return 0, 0, "", false, nil
	}
	if err != nil {
		return 0, 0, "", false, err
	}
	episodes, err := c.client.EpisodesWithSpecials(ctx, show.ID)
	if err == nil {
		if season, episode, ok := matchTVMazeEpisode(file.EpisodeTitle, episodes); ok {
			return season, episode, "tvmaze_episode_title_exact", true, nil
		}
	} else if !errors.Is(err, tvmaze.ErrNotFound) {
		return 0, 0, "", false, err
	}
	lists, err := c.client.AlternateLists(ctx, show.ID)
	if errors.Is(err, tvmaze.ErrNotFound) {
		return 0, 0, "", false, nil
	}
	if err != nil {
		return 0, 0, "", false, err
	}
	if len(lists) > 5 {
		lists = lists[:5]
	}
	for _, list := range lists {
		alternate, err := c.client.AlternateEpisodes(ctx, list.ID)
		if err != nil {
			continue
		}
		for _, item := range alternate {
			if specialTitleScore(file.EpisodeTitle, item.Name) < 90 || len(item.Embedded.Episodes) != 1 {
				continue
			}
			canonical := item.Embedded.Episodes[0]
			if canonical.Number != nil {
				return canonical.Season, *canonical.Number, "tvmaze_alternate_episode_title_exact", true, nil
			}
		}
	}
	return 0, 0, "", false, nil
}

func matchTVMazeEpisode(title string, episodes []tvmaze.Episode) (int, int, bool) {
	bestScore, bestSeason, bestEpisode, matches := 0, 0, 0, 0
	for _, episode := range episodes {
		if episode.Number == nil {
			continue
		}
		score := specialTitleScore(title, episode.Name)
		if score > bestScore {
			bestScore, bestSeason, bestEpisode, matches = score, episode.Season, *episode.Number, 1
		} else if score == bestScore {
			matches++
		}
	}
	return bestSeason, bestEpisode, bestScore >= 90 && matches == 1
}
