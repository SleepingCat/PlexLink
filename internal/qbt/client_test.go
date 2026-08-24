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

func TestShutdownIfIdle(t *testing.T) {
	for _, test := range []struct {
		name         string
		torrents     string
		wantShutdown bool
	}{
		{
			name:         "all downloads complete",
			torrents:     `[{"hash":"one","progress":1,"amount_left":0,"state":"uploading"},{"hash":"two","progress":1,"amount_left":0,"state":"pausedUP"},{"hash":"three","progress":1,"amount_left":0,"state":"queuedUP"}]`,
			wantShutdown: true,
		},
		{
			name:         "complete stalled upload does not block shutdown",
			torrents:     `[{"hash":"one","progress":1,"amount_left":0,"state":"pausedUP"},{"hash":"two","progress":1,"amount_left":0,"state":"stalledUP"}]`,
			wantShutdown: true,
		},
		{
			name:     "download state remains despite complete counters",
			torrents: `[{"hash":"one","progress":1,"amount_left":0,"state":"pausedUP"},{"hash":"two","progress":1,"amount_left":0,"state":"downloading"}]`,
		},
		{
			name:     "forced download state remains despite complete counters",
			torrents: `[{"hash":"one","progress":1,"amount_left":0,"state":"pausedUP"},{"hash":"two","progress":1,"amount_left":0,"state":"forcedDL"}]`,
		},
		{
			name:     "first complete while queued and paused downloads remain",
			torrents: `[{"hash":"one","progress":1,"amount_left":0,"state":"uploading"},{"hash":"two","progress":0.25,"amount_left":750,"state":"queuedDL"},{"hash":"three","progress":0,"amount_left":1000,"state":"pausedDL"}]`,
		},
		{
			name:     "stalled download remains",
			torrents: `[{"hash":"one","progress":1,"amount_left":0,"state":"uploading"},{"hash":"two","progress":0.75,"amount_left":250,"state":"stalledDL"}]`,
		},
		{
			name:     "metadata download remains",
			torrents: `[{"hash":"one","progress":1,"amount_left":0,"state":"uploading"},{"hash":"two","progress":0,"amount_left":0,"state":"metaDL"}]`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			shutdownCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					fmt.Fprint(w, "Ok.")
				case "/api/v2/torrents/info":
					if r.URL.RawQuery != "" {
						t.Errorf("query=%q", r.URL.RawQuery)
					}
					fmt.Fprint(w, test.torrents)
				case "/api/v2/app/shutdown":
					shutdownCalls++
					if r.Method != http.MethodPost {
						t.Errorf("method=%s", r.Method)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, _ := New(server.URL, "user", "secret", server.Client())
			shutdown, err := client.ShutdownIfIdle(context.Background())
			if err != nil || shutdown != test.wantShutdown {
				t.Fatalf("shutdown=%t err=%v", shutdown, err)
			}
			wantCalls := 0
			if test.wantShutdown {
				wantCalls = 1
			}
			if shutdownCalls != wantCalls {
				t.Fatalf("shutdown calls=%d, want %d", shutdownCalls, wantCalls)
			}
		})
	}
}

func TestShutdownIfIdleDoesNotShutdownWithIncompleteStatusData(t *testing.T) {
	for _, torrents := range []string{
		`[{"hash":"missing-progress","amount_left":0,"state":"pausedUP"}]`,
		`[{"hash":"missing-amount-left","progress":1,"state":"pausedUP"}]`,
		`[{"hash":"missing-state","progress":1,"amount_left":0}]`,
		`[{"hash":"invalid-progress","progress":1.1,"amount_left":0,"state":"pausedUP"}]`,
	} {
		t.Run(torrents, func(t *testing.T) {
			shutdownCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					fmt.Fprint(w, "Ok.")
				case "/api/v2/torrents/info":
					fmt.Fprint(w, torrents)
				case "/api/v2/app/shutdown":
					shutdownCalls++
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, _ := New(server.URL, "user", "secret", server.Client())
			if shutdown, err := client.ShutdownIfIdle(context.Background()); err == nil || shutdown {
				t.Fatalf("shutdown=%t err=%v", shutdown, err)
			}
			if shutdownCalls != 0 {
				t.Fatalf("shutdown calls=%d", shutdownCalls)
			}
		})
	}
}

func TestShutdownIfIdleDoesNotShutdownWhenCheckFails(t *testing.T) {
	shutdownCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			fmt.Fprint(w, "Ok.")
		case "/api/v2/torrents/info":
			http.Error(w, "unavailable", http.StatusForbidden)
		case "/api/v2/app/shutdown":
			shutdownCalls++
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := New(server.URL, "user", "secret", server.Client())
	if shutdown, err := client.ShutdownIfIdle(context.Background()); err == nil || shutdown {
		t.Fatalf("shutdown=%t err=%v", shutdown, err)
	}
	if shutdownCalls != 0 {
		t.Fatalf("shutdown calls=%d", shutdownCalls)
	}
}
