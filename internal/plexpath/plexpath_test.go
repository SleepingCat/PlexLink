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
	want := filepath.Join(root, "Show_ Name (2022)", "Season 02", "Show_ Name (2022) - S02E18-E19.mkv")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	matchPath, content, ok, err := BuildMatchFile(root, model.KindTV, model.Match{ID: 123, Name: "Show:\nName", Year: 2022})
	if err != nil || !ok {
		t.Fatalf("match file path: ok=%t err=%v", ok, err)
	}
	if want := filepath.Join(root, "Show_ Name (2022)", ".plexmatch"); matchPath != want {
		t.Fatalf("match path=%q want %q", matchPath, want)
	}
	if want := "Title: Show: Name\nYear: 2022\nTmdbId: 123\n"; content != want {
		t.Fatalf("content=%q want %q", content, want)
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

func TestBuildMatchFileOnlyForSeriesLibraries(t *testing.T) {
	root := t.TempDir()
	path, content, ok, err := BuildMatchFile(root, model.KindAnime, model.Match{ID: 42, Name: "Anime", Year: 2023})
	if err != nil || !ok || path != filepath.Join(root, "Anime (2023)", ".plexmatch") || content != "Title: Anime\nYear: 2023\nTmdbId: 42\n" {
		t.Fatalf("anime path=%q content=%q ok=%t err=%v", path, content, ok, err)
	}
	path, content, ok, err = BuildMatchFile(root, model.KindMovie, model.Match{ID: 5, Name: "Film", Year: 2024})
	if err != nil || ok || path != "" || content != "" {
		t.Fatalf("movie path=%q content=%q ok=%t err=%v", path, content, ok, err)
	}
}
