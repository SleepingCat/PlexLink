package release

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/model"
)

func TestRequiredLongTailFixturesAreRetained(t *testing.T) {
	data, err := os.ReadFile("../../testdata/releases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []string
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	present := make(map[string]bool, len(fixtures))
	for _, fixture := range fixtures {
		present[fixture] = true
	}
	for _, required := range []string{
		"Counterpart 2 - LostFilm.TV [MP4]",
		"The.Devils.Hour.S01.1080p.WEB-DL",
		"BoJack Horseman (1080p WEB-DL)",
		"V.for.Vendetta.2005.1080p.BluRay.RusEngDTS.HDCLUB.mkv",
		"Забавные Игры (2007) BDRip-AVC by NewAVC.mkv",
		"Ottochennoe.Lezvie.1996.RUS.HDRip.avi",
	} {
		if !present[required] {
			t.Errorf("required regression fixture %q is missing", required)
		}
	}
}

func TestParseRegressionNames(t *testing.T) {
	cases := []struct {
		name   string
		season int
	}{{"Game.of.Thrones.S01.1080p", 1}, {"The Knick (Season 02) Amedia", 2}, {"Rick.and.Morty.S09.AMZN.WEB-DL.1080p.by.AKTEP", 9}, {"Yellowstone 2 - LostFilm.TV [MP4]", 0}}
	for _, tc := range cases {
		e, _ := Parse(model.Torrent{Name: tc.name, ContentPath: tc.name}, nil, model.KindTV)
		if len(e.Titles) == 0 {
			t.Errorf("%q has no title", tc.name)
		}
		if tc.season > 0 {
			_, media := Parse(model.Torrent{Name: tc.name, ContentPath: tc.name}, []model.TorrentFile{{Name: tc.name + ".mkv", Priority: 1, Progress: 1}}, model.KindTV)
			if len(media) == 0 || media[0].Ref.Season != tc.season {
				t.Errorf("%q season=%v", tc.name, media)
			}
		}
	}
}
func TestCyrillicNormalization(t *testing.T) {
	if got := NormalizeTitle(" Мышь! "); got != "мышь" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeTitleRemovesApostrophesInsideWords(t *testing.T) {
	cases := []string{
		"The Devils Hour",
		"The Devil's Hour",
		"The Devil’s Hour",
		"The Devil‘s Hour",
		"The Devilʼs Hour",
		"“The Devil’s Hour”",
	}
	for _, input := range cases {
		if got := NormalizeTitle(input); got != "the devils hour" {
			t.Errorf("NormalizeTitle(%q) = %q", input, got)
		}
	}
}

func TestNormalizeTitleGeneralPunctuation(t *testing.T) {
	if left, right := NormalizeTitle("Show: The Hour"), NormalizeTitle("Show - The Hour"); left != right {
		t.Fatalf("punctuation variants differ: %q != %q", left, right)
	}
}

func TestReverseTransliterationHypotheses(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "Ottochennoe Lezvie", want: "Отточенное лезвие"},
		{input: "Igra Prestolov", want: "Игра престолов"},
		{input: "Chelovek Pauk", want: "Человек паук"},
	} {
		got := ReverseTransliterationHypotheses(test.input)
		if len(got) != 1 || got[0] != test.want {
			t.Errorf("ReverseTransliterationHypotheses(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestReverseTransliterationAvoidsObviousEnglishTitles(t *testing.T) {
	for _, title := range []string{"Sling Blade", "Game of Thrones", "Charlie Chaplin"} {
		if got := ReverseTransliterationHypotheses(title); len(got) != 0 {
			t.Errorf("English title %q produced hypotheses %q", title, got)
		}
	}
}

func TestTitleHypothesesAreBoundedAndDeduplicated(t *testing.T) {
	titles := []model.WeightedTitle{
		{Title: "Ottochennoe Lezvie", Weight: 3},
		{Title: "ottochennoe.lezvie", Weight: 2},
		{Title: "Igra Prestolov", Weight: 1},
	}
	got := TitleHypotheses(titles, 1)
	if len(got) != 1 || got[0] != "Отточенное лезвие" {
		t.Fatalf("hypotheses=%q", got)
	}
}

func TestAnimeAbsolute(t *testing.T) {
	_, f := Parse(model.Torrent{Name: "[VARYG] Pluto", ContentPath: "Pluto"}, []model.TorrentFile{{Name: "[VARYG] Pluto - 03 [1080p].mkv", Priority: 1, Progress: 1}}, model.KindAnime)
	if len(f) != 1 || !f[0].Ref.Absolute || f[0].Ref.Episode != 3 {
		t.Fatalf("unexpected %+v", f)
	}
}

func TestExtractEpisodeTitle(t *testing.T) {
	got := extractEpisodeTitle("BoJack Horseman s1e13 - Sabrina's Christmas Wish.mkv")
	if got != "Sabrina's Christmas Wish" {
		t.Fatalf("episode title=%q", got)
	}
	if got := extractEpisodeTitle("BoJack.Horseman.S01E12.1080p.WEB-DL.mkv"); got != "" {
		t.Fatalf("technical metadata treated as episode title: %q", got)
	}
}

func TestSeasonSuffixFallback(t *testing.T) {
	titles := []model.WeightedTitle{{Title: "Example Show 2", Weight: 3}}
	episodes := []model.EpisodeRef{{Season: 2, Episode: 1}, {Season: 2, Episode: 10}}
	got := addSeasonSuffixFallback(titles, episodes)
	found := false
	for _, title := range got {
		if title.Title == "Example Show" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing title without season suffix: %+v", got)
	}
}

func TestSeasonSuffixFallbackRequiresUnambiguousMatchingSeason(t *testing.T) {
	titles := []model.WeightedTitle{{Title: "Example Show 2", Weight: 3}}
	episodes := []model.EpisodeRef{{Season: 1, Episode: 1}, {Season: 2, Episode: 1}}
	got := addSeasonSuffixFallback(titles, episodes)
	if len(got) != 1 {
		t.Fatalf("fallback created for ambiguous seasons: %+v", got)
	}
}
