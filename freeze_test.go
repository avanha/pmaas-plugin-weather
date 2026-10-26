package weather

import (
	"testing"
	"time"

	"github.com/avanha/pmaas-plugin-weather/data"
	"github.com/avanha/pmaas-spi/alert"
)

// night is the night of the day from 6 PM, with the low, and day the day from 6 AM with the high.
func night(day int, low int) data.Period  { return period(day, 18, false, low, 0, "skc") }
func dayOf(day int, high int) data.Period { return period(day, 6, true, high, 0, "skc") }

func autumn(day int, hour int) time.Time { return time.Date(2026, 10, day, hour, 0, 0, 0, mdt) }

func TestFreezeAlert_DefaultsAreThirtyDegreesSeptemberFirstToNovemberFifteenthAndADay(t *testing.T) {
	f := FreezeAlert{}.withDefaults()

	if f.Below != 30 || f.From != (MonthDay{time.September, 1}) || f.To != (MonthDay{time.November, 15}) || f.Within != 24*time.Hour ||
		f.Severity != alert.SeverityWarning {
		t.Errorf("%+v", f)
	}

	kept := FreezeAlert{Below: 28, From: MonthDay{time.October, 1}, To: MonthDay{time.April, 1}, Within: 12 * time.Hour, Severity: alert.SeverityCritical}.withDefaults()
	if kept.Below != 28 || kept.Within != 12*time.Hour || kept.Severity != alert.SeverityCritical || kept.From.Month != time.October {
		t.Errorf("%+v", kept)
	}
}

func TestFreezeAlert_TheSeasonIncludesItsFirstAndLastDaysAndMayGoOverTheNewYear(t *testing.T) {
	f := FreezeAlert{}.withDefaults()

	for day, want := range map[MonthDay]bool{
		{time.August, 31}: false, {time.September, 1}: true, {time.October, 25}: true, {time.November, 15}: true, {time.November, 16}: false,
		{time.January, 10}: false,
	} {
		if got := f.inSeason(day); got != want {
			t.Errorf("%v: %v, want %v", day, got, want)
		}
	}

	winter := FreezeAlert{From: MonthDay{time.November, 15}, To: MonthDay{time.March, 1}}.withDefaults()

	for day, want := range map[MonthDay]bool{
		{time.November, 14}: false, {time.November, 15}: true, {time.January, 10}: true, {time.March, 1}: true, {time.March, 2}: false, {time.July, 4}: false,
	} {
		if got := winter.inSeason(day); got != want {
			t.Errorf("winter %v: %v, want %v", day, got, want)
		}
	}
}

func TestFreezeLow_IsTheLowestTemperatureOfTheStretchesInTheNextDayInFahrenheit(t *testing.T) {
	periods := []data.Period{dayOf(25, 55), night(25, 31), dayOf(26, 60), night(26, 20)}

	// 7 AM on the 25th: the day, tonight, and (up to 7 AM the 26th) not the day after or its night.
	low, p, found := freezeLow(periods, autumn(25, 7), 24*time.Hour)
	if !found || low != 31 || p.Daytime {
		t.Errorf("%d %+v %v", low, p, found)
	}

	// The next day's night is in the 24 hours from 8 PM on the 25th.
	if low, _, _ := freezeLow(periods, autumn(25, 20), 24*time.Hour); low != 20 {
		t.Errorf("%d", low)
	}

	// A period that is over is not looked at, nor one that is after the time.
	if _, _, found := freezeLow(periods, autumn(28, 7), 24*time.Hour); found {
		t.Error("a low from a forecast that is over")
	}

	celsius := []data.Period{{Start: autumn(25, 18), End: autumn(26, 6), Temperature: -3, Unit: "C"}}
	if low, _, _ := freezeLow(celsius, autumn(25, 19), 24*time.Hour); low != 27 {
		t.Errorf("-3 C is %d F", low)
	}
}

func TestDecideFreeze_RaisesAtTheLimitAndNotAboveItAndNotOutOfSeason(t *testing.T) {
	f := FreezeAlert{Note: "sprinkler pipes"}.withDefaults()
	now := autumn(25, 7)

	d := decideFreeze(f, []data.Period{dayOf(25, 55), night(25, 30)}, now, freezeState{})
	if !d.raise || d.low != 30 || d.period != "p" {
		t.Fatalf("%+v", d)
	}

	for _, want := range []string{"Forecast low of 30°F", "at or below 30°F", "For: sprinkler pipes."} {
		if !contains(d.text, want) {
			t.Errorf("missing %q in %q", want, d.text)
		}
	}

	if d := decideFreeze(f, []data.Period{dayOf(25, 55), night(25, 31)}, now, freezeState{}); d.raise || d.clear {
		t.Errorf("%+v", d)
	}

	// October 25 is in the season; November 20 and August are not.
	if d := decideFreeze(f, []data.Period{night(25, 10)}, time.Date(2026, 11, 20, 7, 0, 0, 0, mdt), freezeState{}); d.raise {
		t.Errorf("raised out of season: %+v", d)
	}

	// A forecast that does not reach says nothing, and leaves what was said.
	if d := decideFreeze(f, nil, now, freezeState{raised: true}); d.raise || d.clear {
		t.Errorf("%+v", d)
	}
}

func TestDecideFreeze_OnceRaisedItIsClearedOnlyWhenTheLowIsClearOfTheLimitOrTheSeasonIsOver(t *testing.T) {
	f := FreezeAlert{}.withDefaults()
	now := autumn(25, 7)
	raised := freezeState{raised: true, low: 29, period: "p"}

	// 31 and 32 are within the margin: it stays, and says nothing new.
	for _, low := range []int{31, 32} {
		if d := decideFreeze(f, []data.Period{night(25, low)}, now, raised); d.raise || d.clear {
			t.Errorf("%d: %+v", low, d)
		}
	}

	// 33 is clear of it.
	if d := decideFreeze(f, []data.Period{night(25, 33)}, now, raised); !d.clear {
		t.Errorf("%+v", d)
	}

	// The season is over, whatever the forecast.
	if d := decideFreeze(f, []data.Period{night(25, 10)}, time.Date(2026, 11, 20, 7, 0, 0, 0, mdt), raised); !d.clear {
		t.Errorf("%+v", d)
	}
}

func TestDecideFreeze_AChangeOfADegreeIsNotSaidAgainButTwoIsAndSoIsAnotherPeriod(t *testing.T) {
	f := FreezeAlert{}.withDefaults()
	now := autumn(25, 7)
	raised := freezeState{raised: true, low: 28, period: "p"}

	if d := decideFreeze(f, []data.Period{night(25, 27)}, now, raised); d.raise {
		t.Errorf("said again for a degree: %+v", d)
	}

	if d := decideFreeze(f, []data.Period{night(25, 26)}, now, raised); !d.raise || d.low != 26 {
		t.Errorf("%+v", d)
	}

	other := night(25, 28)
	other.Name = "Tonight"

	if d := decideFreeze(f, []data.Period{other}, now, raised); !d.raise || d.period != "Tonight" {
		t.Errorf("%+v", d)
	}
}

func TestFreezeAlert_IsRaisedClearedAndShownOnTheCardAsThePluginHearsTheForecast(t *testing.T) {
	var handle *LocationHandle

	h := newHarness(t, func(c PluginConfig) { handle = c.AddZip("Cougar", "80026") })
	handle.AlertOnFreeze(FreezeAlert{Note: "sprinkler pipes"})
	h.clock = autumn(25, 7)
	l := h.plugin.locations[0]

	// A place with no freeze alert asked for does nothing.
	other := newHarness(t, func(c PluginConfig) { c.AddZip("Elsewhere", "80026") })
	other.clock = autumn(25, 7)
	other.plugin.onForecast(other.plugin.locations[0], data.Forecast{Periods: []data.Period{night(25, 10)}}, false, nil)

	if len(other.container.raised()) != 0 {
		t.Errorf("an alert for a place that did not ask: %+v", other.container.raised())
	}

	cold := data.Forecast{Periods: []data.Period{dayOf(25, 50), night(25, 27)}}
	h.plugin.onForecast(l, cold, false, nil)

	raised := h.container.raised()
	if len(raised) != 1 || raised[0].Key != "weather/cougar/freeze-forecast" || raised[0].Severity != alert.SeverityWarning ||
		raised[0].Title != "Cougar: freeze forecast" || !contains(raised[0].Message, "27°F") || !contains(raised[0].Message, "sprinkler pipes") {
		t.Fatalf("%+v", raised)
	}

	// The card says so too.
	view := h.plugin.viewOf(l)
	if len(view.Alerts) == 0 || view.Alerts[0].Event != "Freeze forecast: 27°F p" || view.Alerts[0].Class != "warning" {
		t.Errorf("%+v", view.Alerts)
	}

	// The same forecast again, and one a degree off, say nothing more.
	h.plugin.onForecast(l, cold, true, nil)
	h.plugin.onForecast(l, data.Forecast{Periods: []data.Period{dayOf(25, 50), night(25, 28)}}, false, nil)

	if len(h.container.raised()) != 1 {
		t.Errorf("%d raised", len(h.container.raised()))
	}

	// A warmer forecast clears it, and the card loses the line.
	h.plugin.onForecast(l, data.Forecast{Periods: []data.Period{dayOf(25, 50), night(25, 40)}}, false, nil)

	if cleared := h.container.cleared(); len(cleared) != 1 || cleared[0].Key != "weather/cougar/freeze-forecast" {
		t.Errorf("%+v", cleared)
	}

	for _, a := range h.plugin.viewOf(l).Alerts {
		if contains(a.Event, "Freeze forecast") {
			t.Errorf("still on the card: %+v", a)
		}
	}
}
