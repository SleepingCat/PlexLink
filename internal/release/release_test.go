package release

import (
	"github.com/SleepingCat/PlexLink/internal/model"
	"testing"
)

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
func TestAnimeAbsolute(t *testing.T) {
	_, f := Parse(model.Torrent{Name: "[VARYG] Pluto", ContentPath: "Pluto"}, []model.TorrentFile{{Name: "[VARYG] Pluto - 03 [1080p].mkv", Priority: 1, Progress: 1}}, model.KindAnime)
	if len(f) != 1 || !f[0].Ref.Absolute || f[0].Ref.Episode != 3 {
		t.Fatalf("unexpected %+v", f)
	}
}
