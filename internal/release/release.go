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
	absAnime                = regexp.MustCompile(`(?i)(?:^|[\s._-])-?\s*(\d{1,3})(?:\s|\[|$)`)
	explicitSeason          = regexp.MustCompile(`(?i)S\d{1,2}E\d{1,3}`)
	ignored                 = regexp.MustCompile(`(?i)(^|[\\/._ -])(sample|trailer|proof|screens?|extras?)([\\/._ -]|$)`)
	trailingNumber          = regexp.MustCompile(`^(.*?)[\s._-]+(\d+)$`)
	episodeTitleAfterNumber = regexp.MustCompile(`(?i)S\d{1,2}E\d{1,3}(?:-E\d{1,3})?[\s._-]+(.+)$`)
)

var mediaExt = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".webm": true, ".ts": true, ".m2ts": true}

var reverseTransliterationSequences = []struct {
	latin    string
	cyrillic string
}{
	{"shch", "щ"}, {"sch", "щ"}, {"shh", "щ"},
	{"yo", "ё"}, {"jo", "ё"}, {"zh", "ж"}, {"kh", "х"}, {"ts", "ц"},
	{"ch", "ч"}, {"sh", "ш"}, {"yu", "ю"}, {"ju", "ю"}, {"ya", "я"},
	{"ja", "я"}, {"ye", "е"}, {"je", "е"},
}

var reverseTransliterationLetters = map[byte]string{
	'a': "а", 'b': "б", 'c': "к", 'd': "д", 'e': "е", 'f': "ф", 'g': "г",
	'h': "х", 'i': "и", 'j': "й", 'k': "к", 'l': "л", 'm': "м", 'n': "н",
	'o': "о", 'p': "п", 'q': "к", 'r': "р", 's': "с", 't': "т", 'u': "у",
	'v': "в", 'w': "в", 'x': "кс", 'y': "ы", 'z': "з",
}

func IsMedia(name string) bool {
	return mediaExt[strings.ToLower(filepath.Ext(name))] && !ignored.MatchString(name)
}

func IsIgnored(name string) bool {
	return mediaExt[strings.ToLower(filepath.Ext(name))] && ignored.MatchString(name)
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
		out = append(out, model.MediaFile{Name: f.Name, EpisodeTitle: extractEpisodeTitle(f.Name), Ref: ref})
	}
	e.Titles = uniqueTitles(e.Titles)
	e.Titles = addSeasonSuffixFallback(e.Titles, e.Episodes)
	return e, out
}

func extractEpisodeTitle(name string) string {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	match := episodeTitleAfterNumber.FindStringSubmatch(base)
	if len(match) != 2 {
		return ""
	}
	parsed, err := torrentname.Parse(match[1])
	if err != nil || parsed == nil {
		return ""
	}
	return strings.TrimSpace(parsed.Title)
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

// ReverseTransliterationHypotheses returns conservative Russian title guesses
// for Latin text that has common Russian transliteration markers. The original
// title remains authoritative input and is deliberately not returned here.
func ReverseTransliterationHypotheses(title string) []string {
	if !looksLikeRussianTransliteration(title) {
		return nil
	}
	converted := reverseTransliterate(title)
	if converted == "" || NormalizeTitle(converted) == NormalizeTitle(title) {
		return nil
	}
	return []string{converted}
}

// TitleHypotheses derives a bounded, normalized-deduplicated set of additional
// title queries without replacing any parsed title.
func TitleHypotheses(titles []model.WeightedTitle, limit int) []string {
	if limit <= 0 {
		return nil
	}
	seen := make(map[string]bool)
	for _, title := range titles {
		if key := NormalizeTitle(title.Title); key != "" {
			seen[key] = true
		}
	}
	result := make([]string, 0, limit)
	for _, title := range titles {
		for _, hypothesis := range ReverseTransliterationHypotheses(title.Title) {
			key := NormalizeTitle(hypothesis)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, hypothesis)
			if len(result) == limit {
				return result
			}
		}
	}
	return result
}

func looksLikeRussianTransliteration(value string) bool {
	words := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(value)), func(r rune) bool {
		return r == ' ' || r == '.' || r == '_' || r == '-'
	})
	if len(words) < 2 {
		return false
	}
	for _, word := range words {
		for _, r := range word {
			if r < 'a' || r > 'z' {
				return false
			}
		}
	}
	lower := strings.Join(words, " ")
	for _, marker := range []string{"shch", "sch", "shh", "zh", "kh", "ts", "ya", "yu", "yo"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	for _, word := range words {
		for _, suffix := range []string{"ennoe", "ennaya", "ennyi", "enie", "stvo", "ovek", "auk", "skiy", "skaya", "aya", "oye", "oe", "ova", "eva", "ov", "ev", "ina", "iy", "yy"} {
			if len(word) > len(suffix) && strings.HasSuffix(word, suffix) {
				return true
			}
		}
	}
	return false
}

func reverseTransliterate(value string) string {
	words := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(value)), func(r rune) bool {
		return r == ' ' || r == '.' || r == '_' || r == '-'
	})
	converted := make([]string, 0, len(words))
	for _, word := range words {
		var out strings.Builder
		for i := 0; i < len(word); {
			matched := false
			for _, sequence := range reverseTransliterationSequences {
				if strings.HasPrefix(word[i:], sequence.latin) {
					out.WriteString(sequence.cyrillic)
					i += len(sequence.latin)
					matched = true
					break
				}
			}
			if matched {
				continue
			}
			letter, ok := reverseTransliterationLetters[word[i]]
			if !ok {
				return ""
			}
			out.WriteString(letter)
			i++
		}
		converted = append(converted, out.String())
	}
	result := []rune(strings.Join(converted, " "))
	if len(result) == 0 {
		return ""
	}
	result[0] = unicode.ToUpper(result[0])
	return string(result)
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
