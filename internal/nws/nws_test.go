package nws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// service is the National Weather Service as the fixtures captured from it, and what each request asked.
func service(t *testing.T) (*Client, *[]*http.Request) {
	t.Helper()

	var requests []*http.Request

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/geo+json")

		switch {
		case r.URL.Path == "/points/39.9967,-105.0944":
			_, _ = w.Write(fixture(t, "points_lafayette.json"))
		case r.URL.Path == "/points/51.5000,-0.1200":
			http.Error(w, `{"title":"Data Unavailable For Requested Point"}`, http.StatusNotFound)
		case r.URL.Path == "/gridpoints/BOU/60,73/forecast":
			if r.Header.Get("If-Modified-Since") != "" {
				w.WriteHeader(http.StatusNotModified)

				return
			}

			w.Header().Set("Last-Modified", "Sun, 25 Oct 2026 18:46:21 GMT")
			_, _ = w.Write(fixture(t, "forecast_lafayette.json"))
		case r.URL.Path == "/alerts/active":
			_, _ = w.Write(fixture(t, "alerts_co.json"))
		case r.URL.Path == "/busy":
			http.Error(w, "try later", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	return &Client{BaseURL: server.URL, UserAgent: "pmaas-test (nobody@example.com)"}, &requests
}

func TestPoint_FindsTheForecastOfAPlace(t *testing.T) {
	client, requests := service(t)

	point, err := client.Point(context.Background(), 39.9967, -105.0944)
	if err != nil {
		t.Fatal(err)
	}

	if point.ForecastURL != "https://api.weather.gov/gridpoints/BOU/60,73/forecast" || point.Place != "Lafayette, CO" || point.TimeZone != "America/Denver" {
		t.Errorf("%+v", point)
	}

	// The service wants to be told who is asking.
	if got := (*requests)[0].Header.Get("User-Agent"); got != "pmaas-test (nobody@example.com)" {
		t.Errorf("user agent %q", got)
	}
}

func TestPoint_APlaceOutsideTheCountryIsUnsupported(t *testing.T) {
	client, _ := service(t)

	if _, err := client.Point(context.Background(), 51.5, -0.12); !errors.Is(err, ErrUnsupportedLocation) {
		t.Errorf("%v", err)
	}
}

func TestForecast_ParsesThePeriods(t *testing.T) {
	client, _ := service(t)

	forecast, notModified, lastModified, err := client.Forecast(context.Background(), client.BaseURL+"/gridpoints/BOU/60,73/forecast", time.Time{})
	if err != nil || notModified {
		t.Fatalf("%v %v", err, notModified)
	}

	if len(forecast.Periods) != 14 || forecast.UpdatedAt.IsZero() || lastModified.IsZero() {
		t.Fatalf("%d periods, updated %v, modified %v", len(forecast.Periods), forecast.UpdatedAt, lastModified)
	}

	first := forecast.Periods[0]

	if first.Name != "This Afternoon" || !first.Daytime || first.Temperature != 54 || first.Unit != "F" || first.PrecipChance != 16 ||
		first.ShortForecast != "Slight Chance Rain Showers" || first.Icon != "rain_showers" || first.WindSpeed != "7 mph" {
		t.Errorf("%+v", first)
	}

	// The times carry the place's own offset, which is what puts a period on the right day without a time zone database.
	if _, offset := first.Start.Zone(); offset != -6*3600 {
		t.Errorf("offset %d", offset)
	}

	if forecast.Periods[1].Daytime {
		t.Error("tonight is a day")
	}
}

func TestForecast_AskedForAgainIsNotModifiedWhenTheServiceSaysSo(t *testing.T) {
	client, _ := service(t)

	_, notModified, lastModified, err := client.Forecast(context.Background(), client.BaseURL+"/gridpoints/BOU/60,73/forecast", time.Date(2026, 10, 25, 18, 46, 21, 0, time.UTC))

	if err != nil || !notModified || lastModified.IsZero() {
		t.Errorf("%v %v %v", err, notModified, lastModified)
	}
}

func TestAlerts_ParsesWhatIsInEffect(t *testing.T) {
	client, requests := service(t)

	alerts, err := client.Alerts(context.Background(), 39.9967, -105.0944)
	if err != nil || len(alerts) != 2 {
		t.Fatalf("%v %d", err, len(alerts))
	}

	a := alerts[0]

	if a.Event != "Flood Warning" || a.Severity != "Severe" || a.Status != "Actual" || a.Urgency != "Immediate" || a.AreaDesc != "Rio Grande, CO" ||
		a.Until().IsZero() || a.Onset.IsZero() || a.Instruction == "" || a.ID == "" {
		t.Errorf("%+v", a)
	}

	if got := (*requests)[0].URL.RawQuery; got != "point=39.9967%2C-105.0944" && got != "point=39.9967,-105.0944" {
		t.Errorf("query %q", got)
	}
}

func TestAnAnswerThatIsNotOKIsAnErrorThatSaysWhetherToTryAgain(t *testing.T) {
	client, _ := service(t)

	_, _, _, err := client.Forecast(context.Background(), client.BaseURL+"/busy", time.Time{})

	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 503 || !httpErr.Temporary() {
		t.Errorf("%v", err)
	}

	if (&HTTPError{Status: 404}).Temporary() || !(&HTTPError{Status: 429}).Temporary() {
		t.Error("wrong")
	}
}

func TestIconCode(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.weather.gov/icons/land/day/rain_showers,20?size=medium":      "rain_showers",
		"https://api.weather.gov/icons/land/night/tsra,40/rain_showers,30?size=x": "tsra",
		"https://api.weather.gov/icons/land/day/skc?size=medium":                  "skc",
		"https://example.com/nothing":                                             "",
		"":                                                                        "",
	} {
		if got := IconCode(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
