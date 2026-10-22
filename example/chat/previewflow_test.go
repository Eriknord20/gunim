package main

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

// site serves a page with the given head, and a 400 by 200 picture at /pic.png.
func site(t *testing.T, head string) *httptest.Server {
	t.Helper()
	var pic bytes.Buffer
	if err := png.Encode(&pic, image.NewRGBA(image.Rect(0, 0, 400, 200))); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><head>" + head + "</head><body><h1>Hi</h1></body></html>"))
	})
	mux.HandleFunc("/pic.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pic.Bytes())
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestALinkSentGetsItsCard(t *testing.T) {
	s := site(t, `<meta property="og:title" content="Release 5.3"><meta property="og:image" content="/pic.png">`)
	h := newHarness(t)
	h.a.web = s.Client()
	h.typeAndSend("See " + s.URL + "/page for the notes.")
	body := "See " + s.URL + "/page for the notes."
	h.until("the card arrives", func() bool {
		it, ok := h.item(body)
		return ok && it.Preview.Title == "Release 5.3"
	})
	it, _ := h.item(body)
	if it.Preview.URL != s.URL+"/page" || it.Preview.Picture == "" || h.a.images[it.Preview.Picture] == nil {
		t.Fatalf("preview %+v, want the page's address and picture", it.Preview)
	}
}

func TestNoCardIsFetchedOffline(t *testing.T) {
	s := site(t, `<title>Anything</title>`)
	h := newHarness(t)
	h.a.web = s.Client()
	h.a.handle(LinkToggled{})
	h.typeAndSend("Offline " + s.URL + "/page")
	h.frames(30)
	if it, _ := h.item("Offline " + s.URL + "/page"); it.Preview.URL != "" {
		t.Fatalf("a card came offline: %+v", it.Preview)
	}
}
