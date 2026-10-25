// Package nws is the National Weather Service's forecast and alert service (https://www.weather.gov/documentation/services-web-api): free,
// with no key, for the United States. It asks only that a request says who is asking, in a User-Agent.
package nws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/avanha/pmaas-plugin-weather/data"
)

// DefaultBaseURL is where the service is.
const DefaultBaseURL = "https://api.weather.gov"

// maxBody is the most that is read of an answer: a forecast is about 15 KB.
const maxBody = 4 << 20

// ErrUnsupportedLocation is that the service does not cover the place: it is outside the United States.
var ErrUnsupportedLocation = errors.New("the National Weather Service has no forecast for that place (it covers the United States)")

// HTTPError is an answer that is not what was asked for.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("the weather service answered %d: %s", e.Status, e.Body)
}

// Temporary is whether asking again later may work: the service is struggling, or asked to be left alone for a while. It often is.
func (e *HTTPError) Temporary() bool {
	return e.Status >= 500 || e.Status == http.StatusTooManyRequests || e.Status == http.StatusRequestTimeout
}

// Doer is what makes a request: an *http.Client.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client asks the service.
type Client struct {
	HTTP      Doer
	BaseURL   string
	UserAgent string
}

// Point is what the service knows of a place: where its forecast is.
type Point struct {
	ForecastURL string
	TimeZone    string

	// Place is the town it is in, "Lafayette, CO".
	Place string
}

func (c *Client) get(ctx context.Context, rawURL string, since time.Time) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	request.Header.Set("User-Agent", c.UserAgent)
	request.Header.Set("Accept", "application/geo+json")

	if !since.IsZero() {
		request.Header.Set("If-Modified-Since", since.UTC().Format(http.TimeFormat))
	}

	doer := c.HTTP
	if doer == nil {
		doer = &http.Client{Timeout: 30 * time.Second}
	}

	return doer.Do(request)
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}

	return DefaultBaseURL
}

// getJSON asks, and decodes what comes. It says notModified for a 304.
func (c *Client) getJSON(ctx context.Context, rawURL string, since time.Time, into any) (notModified bool, lastModified time.Time, err error) {
	response, err := c.get(ctx, rawURL, since)
	if err != nil {
		return false, time.Time{}, err
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode == http.StatusNotModified {
		return true, since, nil
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody))
	if err != nil {
		return false, time.Time{}, fmt.Errorf("unable to read the answer: %w", err)
	}

	if response.StatusCode != http.StatusOK {
		text := strings.TrimSpace(string(body))
		if len(text) > 200 {
			text = text[:200]
		}

		return false, time.Time{}, &HTTPError{Status: response.StatusCode, Body: text}
	}

	if err := json.Unmarshal(body, into); err != nil {
		return false, time.Time{}, fmt.Errorf("the weather service's answer was not understood: %w", err)
	}

	if modified, err := http.ParseTime(response.Header.Get("Last-Modified")); err == nil {
		lastModified = modified
	}

	return false, lastModified, nil
}

type pointsResponse struct {
	Properties struct {
		Forecast         string `json:"forecast"`
		TimeZone         string `json:"timeZone"`
		RelativeLocation struct {
			Properties struct {
				City  string `json:"city"`
				State string `json:"state"`
			} `json:"properties"`
		} `json:"relativeLocation"`
	} `json:"properties"`
}

// Point finds the place's forecast. A place outside the United States is ErrUnsupportedLocation.
func (c *Client) Point(ctx context.Context, latitude float64, longitude float64) (Point, error) {
	var response pointsResponse

	_, _, err := c.getJSON(ctx, fmt.Sprintf("%s/points/%.4f,%.4f", c.base(), latitude, longitude), time.Time{}, &response)

	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.Status == http.StatusNotFound {
		return Point{}, ErrUnsupportedLocation
	}

	if err != nil {
		return Point{}, err
	}

	if response.Properties.Forecast == "" {
		return Point{}, errors.New("the weather service gave no forecast address for the place")
	}

	place := response.Properties.RelativeLocation.Properties

	return Point{
		ForecastURL: response.Properties.Forecast,
		TimeZone:    response.Properties.TimeZone,
		Place:       strings.TrimSpace(place.City + ", " + place.State),
	}, nil
}

type forecastResponse struct {
	Properties struct {
		UpdateTime time.Time `json:"updateTime"`
		Periods    []struct {
			Name             string    `json:"name"`
			StartTime        time.Time `json:"startTime"`
			EndTime          time.Time `json:"endTime"`
			IsDaytime        bool      `json:"isDaytime"`
			Temperature      float64   `json:"temperature"`
			TemperatureUnit  string    `json:"temperatureUnit"`
			ShortForecast    string    `json:"shortForecast"`
			DetailedForecast string    `json:"detailedForecast"`
			WindSpeed        string    `json:"windSpeed"`
			WindDirection    string    `json:"windDirection"`
			Icon             string    `json:"icon"`
			Precipitation    struct {
				Value *float64 `json:"value"`
			} `json:"probabilityOfPrecipitation"`
		} `json:"periods"`
	} `json:"properties"`
}

// Forecast gets the forecast at the address Point gave. If it has not changed since the time given (what an earlier answer said it was
// last modified), notModified is true and nothing else is. lastModified is when the service says this answer was.
func (c *Client) Forecast(ctx context.Context, forecastURL string, since time.Time) (forecast data.Forecast, notModified bool, lastModified time.Time, err error) {
	var response forecastResponse

	notModified, lastModified, err = c.getJSON(ctx, forecastURL, since, &response)
	if err != nil || notModified {
		return data.Forecast{}, notModified, lastModified, err
	}

	forecast.UpdatedAt = response.Properties.UpdateTime

	for _, p := range response.Properties.Periods {
		precip := -1
		if p.Precipitation.Value != nil {
			precip = int(math.Round(*p.Precipitation.Value))
		}

		forecast.Periods = append(forecast.Periods, data.Period{
			Name:          p.Name,
			Start:         p.StartTime,
			End:           p.EndTime,
			Daytime:       p.IsDaytime,
			Temperature:   int(math.Round(p.Temperature)),
			Unit:          p.TemperatureUnit,
			PrecipChance:  precip,
			ShortForecast: p.ShortForecast,
			Detailed:      p.DetailedForecast,
			WindSpeed:     p.WindSpeed,
			WindDirection: p.WindDirection,
			Icon:          IconCode(p.Icon),
		})
	}

	if len(forecast.Periods) == 0 {
		return data.Forecast{}, false, time.Time{}, errors.New("the weather service's forecast has no periods")
	}

	return forecast, false, lastModified, nil
}

// IconCode is the code for the sky in the address of an icon, such as "rain_showers" in
// https://api.weather.gov/icons/land/day/rain_showers,20?size=medium. Where there are two (tsra,40/rain_showers,30) it is the first, which is
// the likelier.
func IconCode(iconURL string) string {
	parsed, err := url.Parse(iconURL)
	if err != nil {
		return ""
	}

	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")

	for i, segment := range segments {
		if (segment == "day" || segment == "night") && i+1 < len(segments) {
			code, _, _ := strings.Cut(segments[i+1], ",")

			return code
		}
	}

	return ""
}

type alertsResponse struct {
	Features []struct {
		Properties struct {
			ID          string    `json:"id"`
			AreaDesc    string    `json:"areaDesc"`
			Sent        time.Time `json:"sent"`
			Effective   time.Time `json:"effective"`
			Onset       time.Time `json:"onset"`
			Expires     time.Time `json:"expires"`
			Ends        time.Time `json:"ends"`
			Status      string    `json:"status"`
			MessageType string    `json:"messageType"`
			Severity    string    `json:"severity"`
			Certainty   string    `json:"certainty"`
			Urgency     string    `json:"urgency"`
			Event       string    `json:"event"`
			SenderName  string    `json:"senderName"`
			Headline    string    `json:"headline"`
			Description string    `json:"description"`
			Instruction string    `json:"instruction"`
		} `json:"properties"`
	} `json:"features"`
}

// Alerts is what is in effect at the place now, and what is coming (a watch is listed ahead of when it begins).
func (c *Client) Alerts(ctx context.Context, latitude float64, longitude float64) ([]data.Alert, error) {
	var response alertsResponse

	_, _, err := c.getJSON(ctx, fmt.Sprintf("%s/alerts/active?point=%.4f,%.4f", c.base(), latitude, longitude), time.Time{}, &response)
	if err != nil {
		return nil, err
	}

	alerts := make([]data.Alert, 0, len(response.Features))

	for _, f := range response.Features {
		p := f.Properties

		alerts = append(alerts, data.Alert{
			ID: p.ID, Event: p.Event, Severity: p.Severity, Urgency: p.Urgency, Certainty: p.Certainty, Status: p.Status,
			MessageType: p.MessageType, Headline: p.Headline, Description: p.Description, Instruction: p.Instruction,
			AreaDesc: p.AreaDesc, SenderName: p.SenderName,
			Sent: p.Sent, Effective: p.Effective, Onset: p.Onset, Expires: p.Expires, Ends: p.Ends,
		})
	}

	return alerts, nil
}
