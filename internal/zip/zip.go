// Package zip finds where a US zip code is, so that a place can be given by its zip. It asks zippopotam.us, which is free and has no key.
// The point it gives is the middle of the zip's area, which is as close as the forecast needs.
package zip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is where the service is.
const DefaultBaseURL = "https://api.zippopotam.us"

// ErrUnknown is that there is no such zip code.
var ErrUnknown = errors.New("there is no such zip code")

var zipPattern = regexp.MustCompile(`^\d{5}$`)

// Doer is what makes a request: an *http.Client.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Geocoder finds zip codes.
type Geocoder struct {
	HTTP    Doer
	BaseURL string
}

// Lookup is the latitude and longitude of a five digit zip code.
func (g *Geocoder) Lookup(ctx context.Context, zip string) (latitude float64, longitude float64, err error) {
	zip = strings.TrimSpace(zip)

	if !zipPattern.MatchString(zip) {
		return 0, 0, fmt.Errorf("%q is not a five digit zip code", zip)
	}

	base := g.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/us/"+zip, nil)
	if err != nil {
		return 0, 0, err
	}

	doer := g.HTTP
	if doer == nil {
		doer = &http.Client{Timeout: 20 * time.Second}
	}

	response, err := doer.Do(request)
	if err != nil {
		return 0, 0, err
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode == http.StatusNotFound {
		return 0, 0, ErrUnknown
	}

	if response.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("the zip code service answered %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, 0, err
	}

	var found struct {
		Places []struct {
			Latitude  string `json:"latitude"`
			Longitude string `json:"longitude"`
		} `json:"places"`
	}

	if err := json.Unmarshal(body, &found); err != nil || len(found.Places) == 0 {
		return 0, 0, errors.New("the zip code service's answer was not understood")
	}

	latitude, latErr := strconv.ParseFloat(found.Places[0].Latitude, 64)
	longitude, lonErr := strconv.ParseFloat(found.Places[0].Longitude, 64)

	if latErr != nil || lonErr != nil {
		return 0, 0, errors.New("the zip code service's answer was not understood")
	}

	return latitude, longitude, nil
}
