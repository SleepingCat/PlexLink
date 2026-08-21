package plexpath

import (
	"github.com/SleepingCat/PlexLink/internal/model"
	"path/filepath"
	"testing"
)

func TestBuildTV(t *testing.T) {
	root := t.TempDir()
	got, err := Build(root, model.KindTV, model.Match{ID: 123, Name: "Show: Name", Year: 2022}, model.MediaFile{Name: "x.mkv", Ref: model.EpisodeRef{Season: 2, Episode: 18, EpisodeEnd: 19}})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "Show_ Name (2022) {tmdb-123}", "Season 02", "Show_ Name (2022) - S02E18-E19.mkv")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
func TestBuildMovie(t *testing.T) {
	root := t.TempDir()
	got, err := Build(root, model.KindMovie, model.Match{ID: 5, Name: "Film", Year: 2024}, model.MediaFile{Name: "source.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "Film (2024) {tmdb-5}", "Film (2024) {tmdb-5}.mp4")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
