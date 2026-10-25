package model

import (
	"testing"
	"time"

	"github.com/avanha/pmaas-plugin-weather/data"
)

var mdt = time.FixedZone("MDT", -6*3600)

// period is a 12 hour period starting at the hour on the day (October 2026).
func period(day int, hour int, daytime bool, temp int, precip int, icon string) data.Period {
	start := time.Date(2026, 10, day, hour, 0, 0, 0, mdt)

	return data.Period{
		Name: "p", Start: start, End: start.Add(12 * time.Hour), Daytime: daytime, Temperature: temp, Unit: "F",
		PrecipChance: precip, Icon: icon, ShortForecast: icon + " words",
	}
}

func weekFrom(day int, startsWithNight bool) []data.Period {
	var periods []data.Period

	if startsWithNight {
		periods = append(periods, period(day, 18, false, 40, 50, "rain"))
		day++
	}

	for d := day; d < day+3; d++ {
		periods = append(periods, period(d, 6, true, 60+d-day, 10, "sct"), period(d, 18, false, 35+d-day, 30, "ovc"))
	}

	return periods
}

func TestDays_OneDayHasTheDaysHighAndTheNightsLow(t *testing.T) {
	now := time.Date(2026, 10, 25, 7, 0, 0, 0, mdt)

	days := Days(weekFrom(25, false), 7, now)

	if len(days) != 3 {
		t.Fatalf("%d days: %+v", len(days), days)
	}

	today := days[0]

	if today.Label != "Today" || today.High != 60 || !today.HasHigh || today.Low != 35 || !today.HasLow || today.Precip != 30 ||
		today.Icon != "cloud-sun" || today.Short != "sct words" || today.Date != "2026-10-25" {
		t.Errorf("%+v", today)
	}

	if days[1].Label != "Mon" || days[2].Label != "Tue" {
		t.Errorf("%q %q", days[1].Label, days[2].Label)
	}
}

func TestDays_AfterTheDayIsOverTodayIsTonightWithALowAndNoHigh(t *testing.T) {
	now := time.Date(2026, 10, 25, 19, 0, 0, 0, mdt)

	days := Days(weekFrom(25, true), 7, now)

	if days[0].Label != "Tonight" || days[0].HasHigh || !days[0].HasLow || days[0].Low != 40 || days[0].Icon != "cloud-rain-heavy" {
		t.Errorf("%+v", days[0])
	}

	if days[1].Label != "Mon" || !days[1].HasHigh {
		t.Errorf("%+v", days[1])
	}
}

func TestDays_TheHoursAfterMidnightBelongToTheDayBeforeAndThatDayIsOver(t *testing.T) {
	// 2 AM on the 26th: the first period is the rest of the night that began on the 25th. It is not the 26th's low.
	periods := []data.Period{period(26, 2, false, 33, 20, "few")}
	periods[0].End = time.Date(2026, 10, 26, 6, 0, 0, 0, mdt)
	periods = append(periods, weekFrom(26, false)...)

	days := Days(periods, 7, time.Date(2026, 10, 26, 2, 30, 0, 0, mdt))

	if days[0].Date != "2026-10-26" || days[0].Label != "Today" || !days[0].HasHigh || days[0].Low != 35 {
		t.Errorf("%+v", days[0])
	}
}

func TestDays_TheLimitAndNoPeriodsAndPlacesInOtherTimeZones(t *testing.T) {
	now := time.Date(2026, 10, 25, 7, 0, 0, 0, mdt)

	if got := Days(weekFrom(25, false), 2, now); len(got) != 2 {
		t.Errorf("%d", len(got))
	}

	if Days(nil, 7, now) != nil || Days(weekFrom(25, false), 0, now) != nil {
		t.Error("days from nothing")
	}

	// The server's clock may be in another zone than the place: "today" is the place's.
	utc := time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC) // 7 PM on the 25th at the place
	if got := Days(weekFrom(25, true), 7, utc); got[0].Label != "Tonight" {
		t.Errorf("%+v", got[0])
	}
}

func TestCurrent_IsTheStretchThatIsOnOrElseTheFirst(t *testing.T) {
	periods := weekFrom(25, false)

	if got := Current(periods, time.Date(2026, 10, 25, 20, 0, 0, 0, mdt)); got.Temperature != 35 || got.Icon != "cloud" {
		t.Errorf("%+v", got)
	}

	if got := Current(periods, time.Date(2026, 10, 1, 0, 0, 0, 0, mdt)); got.Temperature != 60 {
		t.Errorf("%+v", got)
	}

	if Current(nil, time.Now()) != nil {
		t.Error("a current from nothing")
	}
}

func TestIconFor(t *testing.T) {
	for _, c := range []struct {
		code    string
		daytime bool
		want    string
	}{
		{"skc", true, "sun"}, {"skc", false, "moon-stars"}, {"few", false, "cloud-moon"}, {"wind_sct", true, "cloud-sun"},
		{"tsra_hi", true, "cloud-lightning-rain"}, {"snow", false, "cloud-snow"}, {"tornado", true, "tornado"},
		{"SKC", true, "sun"}, {"nothing", true, "cloud"}, {"", false, "cloud"},
	} {
		if got := IconFor(c.code, c.daytime); got != c.want {
			t.Errorf("%q day %v: %q, want %q", c.code, c.daytime, got, c.want)
		}
	}
}
