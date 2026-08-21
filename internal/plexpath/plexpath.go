package plexpath

import (
	"fmt"
	"path/filepath"

	"github.com/SleepingCat/PlexLink/internal/model"
	"github.com/SleepingCat/PlexLink/internal/pathutil"
)

func Build(root string, kind model.Kind, match model.Match, file model.MediaFile) (string, error) {
	name := pathutil.SanitizeWindowsName(match.Name)
	base := fmt.Sprintf("%s (%d)", name, match.Year)
	ext := filepath.Ext(file.Name)
	if kind == model.KindMovie {
		dir := fmt.Sprintf("%s {tmdb-%d}", base, match.ID)
		return pathutil.JoinInside(root, dir, dir+ext)
	}
	dir := fmt.Sprintf("%s {tmdb-%d}", base, match.ID)
	season := fmt.Sprintf("Season %02d", file.Ref.Season)
	episode := fmt.Sprintf("%s - S%02dE%02d", base, file.Ref.Season, file.Ref.Episode)
	if file.Ref.EpisodeEnd > file.Ref.Episode {
		episode += fmt.Sprintf("-E%02d", file.Ref.EpisodeEnd)
	}
	return pathutil.JoinInside(root, dir, season, episode+ext)
}
