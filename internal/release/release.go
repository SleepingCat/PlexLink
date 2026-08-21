package release

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/SleepingCat/PlexLink/internal/model"
	"github.com/chill-institute/torrentname"
)

var (
	absAnime       = regexp.MustCompile(`(?i)(?:^|[\s._-])-?\s*(\d{1,3})(?:\s|\[|$)`)
	explicitSeason = regexp.MustCompile(`(?i)S\d{1,2}E\d{1,3}`)
	ignored        = regexp.MustCompile(`(?i)(^|[\\/._ -])(sample|trailer|proof|screens?|extras?)([\\/._ -]|$)`)
	trailingNumber = regexp.MustCompile(`^(.*?)[\s._-]+(\d+)$`)
)

var mediaExt = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".webm": true, ".ts": true, ".m2ts": true}

func IsMedia(name string) bool {
	return mediaExt[strings.ToLower(filepath.Ext(name))] && !ignored.MatchString(name)
}

func Parse(torrent model.Torrent, files []model.TorrentFile, kind model.Kind) (model.Evidence, []model.MediaFile) {
	e := model.Evidence{}
	add := func(name string, weight int) *torrentname.TorrentInfo {
		p, _ := torrentname.Parse(filepath.Base(name))
		if p == nil {
			return nil
		}
		if p.Title != "" {
			e.Titles = append(e.Titles, model.WeightedTitle{Title: p.Title, Weight: weight})
		}
		if e.Year == 0 && p.Year != 0 {
			e.Year = p.Year
		}
		return p
	}
	add(torrent.Name, 3)
	add(filepath.Base(filepath.Clean(torrent.ContentPath)), 2)
	var out []model.MediaFile
	for _, f := range files {
		if f.Priority == 0 || f.Progress < 1 || !IsMedia(f.Name) {
			continue
		}
		p := add(filepath.Base(f.Name), 1)
		add(filepath.Base(filepath.Dir(f.Name)), 1)
		ref := model.EpisodeRef{}
		if p != nil {
			ref.Season, ref.Episode, ref.EpisodeEnd = p.Season, p.Episode, p.EpisodeEnd
		}
		if kind == model.KindAnime && ref.Season == 0 && ref.Episode > 0 && !explicitSeason.MatchString(f.Name) {
			ref.Absolute = true
		}
		if kind == model.KindAnime && ref.Season == 0 && ref.Episode == 0 {
			if m := absAnime.FindStringSubmatch(strings.TrimSuffix(filepath.Base(f.Name), filepath.Ext(f.Name))); len(m) > 1 {
				for _, r := range m[1] {
					ref.Episode = ref.Episode*10 + int(r-'0')
				}
				ref.Absolute = true
			}
		}
		if ref.Episode > 0 {
			e.Episodes = append(e.Episodes, ref)
		}
		out = append(out, model.MediaFile{Name: f.Name, Ref: ref})
	}
	e.Titles = uniqueTitles(e.Titles)
	e.Titles = addSeasonSuffixFallback(e.Titles, e.Episodes)
	return e, out
}

func NormalizeTitle(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if isApostrophe(r) {
			return -1
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, s)), " ")
}

func isApostrophe(r rune) bool {
	switch r {
	case '\'', '’', '‘', '‛', 'ʼ', '＇':
		return true
	default:
		return false
	}
}

func uniqueTitles(in []model.WeightedTitle) []model.WeightedTitle {
	positions := map[string]int{}
	out := make([]model.WeightedTitle, 0, len(in))
	for _, t := range in {
		k := NormalizeTitle(t.Title)
		if k == "" {
			continue
		}
		if i, ok := positions[k]; ok {
			if t.Weight > out[i].Weight {
				out[i] = t
			}
			continue
		}
		positions[k] = len(out)
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Weight > out[j].Weight })
	return out
}

func addSeasonSuffixFallback(titles []model.WeightedTitle, episodes []model.EpisodeRef) []model.WeightedTitle {
	season := 0
	for _, episode := range episodes {
		if episode.Season <= 0 {
			continue
		}
		if season != 0 && season != episode.Season {
			return titles
		}
		season = episode.Season
	}
	if season == 0 {
		return titles
	}
	additional := make([]model.WeightedTitle, 0)
	for _, title := range titles {
		match := trailingNumber.FindStringSubmatch(strings.TrimSpace(title.Title))
		if len(match) != 3 || atoi(match[2]) != season {
			continue
		}
		trimmed := strings.TrimSpace(match[1])
		if trimmed != "" {
			additional = append(additional, model.WeightedTitle{Title: trimmed, Weight: title.Weight})
		}
	}
	return uniqueTitles(append(titles, additional...))
}

func atoi(value string) int {
	n := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
