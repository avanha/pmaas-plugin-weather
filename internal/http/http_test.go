package http

import (
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/avanha/pmaas-plugin-weather/data"
)

func render(t *testing.T, view *data.LocationView) string {
	t.Helper()

	compiled, err := template.New("t").Funcs(locationTemplate.FuncMap).ParseFS(contentFS, "content/templates/weather_location.htmlt")
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := compiled.ExecuteTemplate(&out, "weather_location.htmlt", view); err != nil {
		t.Fatal(err)
	}

	return out.String()
}

func fullView() *data.LocationView {
	return &data.LocationView{
		Name: "Home", Place: "Lafayette, CO", AsOf: time.Date(2026, 10, 25, 14, 5, 0, 0, time.UTC),
		Now:    &data.Now{Icon: "cloud-sun", Temperature: 54, Unit: "F", Short: "Partly Sunny"},
		Alerts: []data.AlertView{{Event: "Flood Watch", Class: "warning", When: "until Mon 3:00 PM"}},
		Days: []data.Day{
			{Label: "Today", High: 54, HasHigh: true, Low: 43, HasLow: true, Precip: 52, Icon: "cloud-drizzle", Short: "Rain Showers"},
			{Label: "Tonight", Low: 40, HasLow: true, Precip: 5, Icon: "moon-stars", Short: "Clear"},
		},
	}
}

func TestTemplate_ShowsTheNameTheNowTheAlertsAndEachDay(t *testing.T) {
	out := render(t, fullView())

	for _, want := range []string{
		"Home", "Lafayette, CO", "as of 2:05 PM", "54&deg;F", "Partly Sunny", "bi-cloud-sun",
		`weather-alert warning`, "Flood Watch", "until Mon 3:00 PM",
		"Today", "54&deg;", "43&deg;", "52%", "bi-cloud-drizzle", `title="Rain Showers"`,
		"Tonight", "&ndash;", "40&deg;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	// A small chance of rain is not worth the room.
	if strings.Contains(out, "5%") && !strings.Contains(out, "52%") {
		t.Errorf("a 5%% chance was shown:\n%s", out)
	}

	if strings.Contains(out, "weather-problem") || strings.Contains(out, "(old)") || strings.Contains(out, "Waiting for the forecast") {
		t.Errorf("a problem, an old mark or a wait with a good forecast:\n%s", out)
	}
}

func TestTemplate_NothingYetSaysItIsWaitingAndAProblemIsSaid(t *testing.T) {
	out := render(t, &data.LocationView{Name: "Home"})

	if !strings.Contains(out, "Waiting for the forecast") || strings.Contains(out, "as of") {
		t.Errorf("%s", out)
	}

	out = render(t, &data.LocationView{Name: "Home", Problem: "There is no zip code 99999."})

	if !strings.Contains(out, "weather-problem") || !strings.Contains(out, "no zip code 99999") || strings.Contains(out, "Waiting for the forecast") {
		t.Errorf("%s", out)
	}

	view := fullView()
	view.Stale = true

	if out := render(t, view); !strings.Contains(out, "(old)") || !strings.Contains(out, "stale") {
		t.Errorf("%s", out)
	}
}

func TestTemplate_WhatTheServiceSaysCannotBecomeMarkup(t *testing.T) {
	view := fullView()
	view.Name = "<b>Home</b>"
	view.Problem = "<script>alert(1)</script>"
	view.Alerts[0].Event = "<img src=x>"
	view.Days[0].Short = `"><script>x</script>`
	view.Now.Short = "<i>"

	out := render(t, view)

	for _, bad := range []string{"<b>Home</b>", "<script>", "<img src=x>", "<i>\n"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q got through:\n%s", bad, out)
		}
	}
}
