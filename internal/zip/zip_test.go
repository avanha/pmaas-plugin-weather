package zip

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func geocoder(t *testing.T) *Geocoder {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/us/80026":
			_, _ = w.Write([]byte(`{"post code":"80026","places":[{"place name":"Lafayette","longitude":"-105.0963","latitude":"39.998","state":"Colorado"}]}`))
		case "/us/99999":
			http.Error(w, "{}", http.StatusNotFound)
		case "/us/11111":
			_, _ = w.Write([]byte(`{"places":[]}`))
		default:
			http.Error(w, "", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	return &Geocoder{BaseURL: server.URL}
}

func TestLookup_FindsAZip(t *testing.T) {
	latitude, longitude, err := geocoder(t).Lookup(context.Background(), " 80026 ")

	if err != nil || latitude != 39.998 || longitude != -105.0963 {
		t.Errorf("%v %v %v", latitude, longitude, err)
	}
}

func TestLookup_WhatIsNotAZipOrNotFoundOrNotUnderstoodIsAnError(t *testing.T) {
	g := geocoder(t)

	if _, _, err := g.Lookup(context.Background(), "8002"); err == nil {
		t.Error("a short zip was accepted")
	}

	if _, _, err := g.Lookup(context.Background(), "99999"); !errors.Is(err, ErrUnknown) {
		t.Errorf("%v", err)
	}

	for _, zip := range []string{"11111", "22222"} {
		if _, _, err := g.Lookup(context.Background(), zip); err == nil {
			t.Errorf("%s was accepted", zip)
		}
	}
}
