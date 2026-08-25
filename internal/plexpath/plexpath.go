package plexpath

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/SleepingCat/PlexLink/internal/model"
	"github.com/SleepingCat/PlexLink/internal/pathutil"
)

func Build(root string, kind model.Kind, match model.Match, file model.MediaFile) (string, error) {
	base := mediaBase(match)
	ext := filepath.Ext(file.Name)
	if kind == model.KindMovie {
		dir := fmt.Sprintf("%s {tmdb-%d}", base, match.ID)
		return pathutil.JoinInside(root, dir, dir+ext)
	}
	dir := base
	season := fmt.Sprintf("Season %02d", file.Ref.Season)
	episode := fmt.Sprintf("%s - S%02dE%02d", base, file.Ref.Season, file.Ref.Episode)
	if file.Ref.EpisodeEnd > file.Ref.Episode {
		episode += fmt.Sprintf("-E%02d", file.Ref.EpisodeEnd)
	}
	return pathutil.JoinInside(root, dir, season, episode+ext)
}

func BuildMatchFile(root string, kind model.Kind, match model.Match) (string, string, bool, error) {
	if kind != model.KindTV && kind != model.KindAnime {
		return "", "", false, nil
	}
	target, err := pathutil.JoinInside(root, mediaBase(match), ".plexmatch")
	if err != nil {
		return "", "", false, err
	}
	title := canonicalTitle(match.Name)
	content := fmt.Sprintf("Title: %s\nYear: %d\nTmdbId: %d\n", title, match.Year, match.ID)
	return target, content, true, nil
}

func mediaBase(match model.Match) string {
	name := pathutil.SanitizeWindowsName(canonicalTitle(match.Name))
	return fmt.Sprintf("%s (%d)", name, match.Year)
}

func canonicalTitle(title string) string {
	return strings.Join(strings.Fields(title), " ")
}
