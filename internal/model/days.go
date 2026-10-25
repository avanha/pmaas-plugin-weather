// Package model is the logic of a forecast: its days, and the icons of its skies.
package model

import (
	"time"

	"github.com/avanha/pmaas-plugin-weather/data"
)

const dateLayout = "2006-01-02"

// dateOf is the date a period belongs to, in the place's own time (a period's times carry the place's offset). A night belongs to the
// day it began on, so the part of it after midnight belongs to the day before.
func dateOf(p data.Period) time.Time {
	start := p.Start

	if !p.Daytime && start.Hour() < 12 {
		start = start.AddDate(0, 0, -1)
	}

	return start
}

// Days puts a forecast's periods into days: the high from the day, the low from the night after it, the greater chance of
// precipitation of the two, and the sky of the day (or the night if that is all there is). Days before today are left out, as is
// anything after the n'th. now is the present time.
func Days(periods []data.Period, n int, now time.Time) []data.Day {
	if len(periods) == 0 || n <= 0 {
		return nil
	}

	today := now.In(periods[0].Start.Location()).Format(dateLayout)

	var days []data.Day

	index := map[string]int{}

	for _, p := range periods {
		date := dateOf(p).Format(dateLayout)

		if date < today {
			continue
		}

		i, ok := index[date]
		if !ok {
			i = len(days)
			index[date] = i
			days = append(days, data.Day{Date: date, Precip: -1, Label: dateOf(p).Format("Mon")})
		}

		day := &days[i]

		if p.PrecipChance > day.Precip {
			day.Precip = p.PrecipChance
		}

		if p.Daytime {
			day.High, day.HasHigh = p.Temperature, true
			day.Icon, day.Short = IconFor(p.Icon, true), p.ShortForecast
		} else {
			day.Low, day.HasLow = p.Temperature, true

			// The night's sky is only the day's if the day has none: today, once the day is over.
			if day.Icon == "" {
				day.Icon, day.Short = IconFor(p.Icon, false), p.ShortForecast
			}
		}
	}

	for i := range days {
		if days[i].Date == today {
			days[i].Label = "Today"

			if !days[i].HasHigh {
				days[i].Label = "Tonight"
			}
		}
	}

	if len(days) > n {
		days = days[:n]
	}

	return days
}

// Current is the stretch of the forecast that is going on now, or else the first, for the line under the heading.
func Current(periods []data.Period, now time.Time) *data.Now {
	if len(periods) == 0 {
		return nil
	}

	chosen := periods[0]

	for _, p := range periods {
		if !now.Before(p.Start) && now.Before(p.End) {
			chosen = p

			break
		}
	}

	return &data.Now{
		Icon:        IconFor(chosen.Icon, chosen.Daytime),
		Temperature: chosen.Temperature,
		Unit:        chosen.Unit,
		Short:       chosen.ShortForecast,
	}
}
