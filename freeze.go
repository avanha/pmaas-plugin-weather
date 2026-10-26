package weather

import (
	"fmt"
	"math"
	"time"

	"github.com/avanha/pmaas-plugin-weather/data"
	"github.com/avanha/pmaas-spi/alert"
)

// MonthDay is a day of the year, without the year.
type MonthDay struct {
	Month time.Month
	Day   int
}

// before is whether the day comes before the other in a year.
func (d MonthDay) before(other MonthDay) bool {
	return d.Month < other.Month || (d.Month == other.Month && d.Day < other.Day)
}

// FreezeAlert is the plugin's own alert for a place, raised from the forecast and not from the National Weather Service, which only
// issues freeze and frost products in the growing season of the place: a night at 25°F after the season gets none, which is when pipes
// are in the most danger. It is on while the date is in the season, and the forecast has a low at or below Below in the time ahead.
type FreezeAlert struct {
	// Below is the forecast low, in °F, that raises the alert. The default is 30.
	Below int

	// From and To are the first and last day of the season in which it is watched for, both included, by the date at the place. The default
	// is September 1 to November 15. A season that is the other way round (November 15 to March 1) goes over the new year.
	From, To MonthDay

	// Within is how far ahead the forecast is looked at: the periods that are on now or begin within this time. A night that begins within it
	// counts, whole, since the forecast gives one low for the night and not when it comes. The default is 24 hours.
	Within time.Duration

	// Severity is how severe the alert is in the alert console. The default is a warning.
	Severity alert.Severity

	// Note is added to the alert, to say what it is for: "sprinkler pipes".
	Note string
}

// freezeHysteresis is how far above Below the forecast has to rise before the alert is cleared, so that a forecast which wavers around the
// limit does not raise and clear it over and over.
const freezeHysteresis = 2

// withDefaults is the alert with what was left out filled in.
func (f FreezeAlert) withDefaults() FreezeAlert {
	if f.Below == 0 {
		f.Below = 30
	}

	if f.From == (MonthDay{}) {
		f.From = MonthDay{time.September, 1}
	}

	if f.To == (MonthDay{}) {
		f.To = MonthDay{time.November, 15}
	}

	if f.Within <= 0 {
		f.Within = 24 * time.Hour
	}

	if f.Severity == 0 {
		f.Severity = alert.SeverityWarning
	}

	return f
}

// inSeason is whether the day is in the season.
func (f FreezeAlert) inSeason(day MonthDay) bool {
	if !f.To.before(f.From) {
		return !day.before(f.From) && !f.To.before(day)
	}

	// Over the new year: in the season from the start to the end of the year, and from the start of the year to the end.
	return !day.before(f.From) || !f.To.before(day)
}

// freezeLow is the lowest temperature, in °F, of the forecast in the time from now to now plus within, and the period it is in. There is none
// if the forecast does not reach that far or is empty.
func freezeLow(periods []data.Period, now time.Time, within time.Duration) (low int, period data.Period, found bool) {
	end := now.Add(within)

	for _, p := range periods {
		if !p.End.After(now) || !p.Start.Before(end) {
			continue
		}

		fahrenheit := p.Temperature
		if p.Unit == "C" {
			fahrenheit = int(math.Round(float64(p.Temperature)*9/5 + 32))
		}

		if !found || fahrenheit < low {
			low, period, found = fahrenheit, p, true
		}
	}

	return low, period, found
}

// freezeState is what the freeze alert of a place has said.
type freezeState struct {
	raised bool
	low    int
	period string
}

// freezeDecision is what is to be done about the alert: raise it (or say it differently), clear it, or nothing.
type freezeDecision struct {
	raise  bool
	clear  bool
	low    int
	period string
	text   string
}

// decideFreeze works out what to do from the forecast and what has been said. The alert is raised when the low is at or below the limit and,
// once raised, is left alone until the low has risen clear of it or the season is over; it is said again if the low is a degree or more
// different or it is another period.
func decideFreeze(f FreezeAlert, periods []data.Period, now time.Time, state freezeState) freezeDecision {
	where := now
	if len(periods) > 0 {
		where = now.In(periods[0].Start.Location())
	}

	if !f.inSeason(MonthDay{where.Month(), where.Day()}) {
		return freezeDecision{clear: state.raised}
	}

	low, period, found := freezeLow(periods, now, f.Within)
	if !found {
		// Not known: what was said stays said.
		return freezeDecision{}
	}

	if low > f.Below && !(state.raised && low <= f.Below+freezeHysteresis) {
		return freezeDecision{clear: state.raised}
	}

	// In the margin above the limit, once raised, it stays as it is: it is no longer a low at or below the limit, so it is not said again.
	if low > f.Below {
		return freezeDecision{}
	}

	if state.raised && period.Name == state.period && abs(low-state.low) < 2 {
		return freezeDecision{}
	}

	text := fmt.Sprintf("Forecast low of %d°F (%s, %s to %s), at or below %d°F.", low, period.Name,
		period.Start.Format(whenLayout), period.End.Format(whenLayout), f.Below)

	if f.Note != "" {
		text += " For: " + f.Note + "."
	}

	return freezeDecision{raise: true, low: low, period: period.Name, text: text}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}

	return n
}
