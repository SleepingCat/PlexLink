package qbt

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientLoginTorrentAndFiles(t *testing.T) {
	logged := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			logged = true
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test"})
			fmt.Fprint(w, "Ok.")
		case "/api/v2/torrents/info":
			if !logged {
				http.Error(w, "no", 403)
				return
			}
			fmt.Fprint(w, `[{"hash":"abc","name":"Show","content_path":"/media/tv/Show","save_path":"/media/tv","progress":1}]`)
		case "/api/v2/torrents/files":
			fmt.Fprint(w, `[{"name":"Show/S01E01.mkv","size":12,"priority":1,"progress":1}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c, _ := New(s.URL, "u", "p", s.Client())
	got, err := c.GetTorrent(context.Background(), "abc")
	if err != nil || got.ContentPath != "/media/tv/Show" {
		t.Fatalf("torrent %+v %v", got, err)
	}
	files, err := c.GetFiles(context.Background(), "abc")
	if err != nil || len(files) != 1 || files[0].Name != "Show/S01E01.mkv" {
		t.Fatalf("files %+v %v", files, err)
	}
}
