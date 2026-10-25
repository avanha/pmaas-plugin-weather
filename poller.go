package weather

import (
	"context"
	"errors"
	"time"

	"github.com/avanha/pmaas-plugin-weather/data"
	"github.com/avanha/pmaas-plugin-weather/internal/nws"
	"github.com/avanha/pmaas-plugin-weather/internal/zip"
)

const (
	// retryFirst is how long to wait after the first failure, and retryMost the most: a service that is struggling is left alone, and
	// it recovers by itself more often than not.
	retryFirst = 30 * time.Second
	retryMost  = 10 * time.Minute

	// lookupTimeout is how long one request is waited for.
	lookupTimeout = 45 * time.Second

	// startSpread is how long the places' first requests are spread over, so that they are not all at once.
	startSpread = 5 * time.Second
)

// retryDelay is how long to wait to ask again after the n'th failure in a row (the first is 1), at most the interval it would be asked at
// anyway.
func retryDelay(failures int, every time.Duration) time.Duration {
	delay := retryFirst

	for i := 1; i < failures && delay < retryMost; i++ {
		delay *= 2
	}

	return min(delay, retryMost, every)
}

// sleep waits for the duration and says whether it was not cut short: false if the plugin stopped meanwhile.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// work keeps one place up to date until the plugin stops: it finds out where the place is (a zip code is looked up, and the service says
// where the forecast of the coordinates is), and then asks for the alerts and the forecast, each at its own interval and with a pause
// after a failure that gets longer for as long as it keeps failing. The answers go to the plugin's goroutine.
func (p *plugin) work(ctx context.Context, l *location) {
	// Places are spread out: not all the requests at once.
	if !sleep(ctx, startSpread*time.Duration(indexOf(p.locations, l))/time.Duration(max(len(p.locations), 1))) {
		return
	}

	latitude, longitude := l.config.latitude, l.config.longitude
	known := l.config.zip == ""
	var point nws.Point
	havePoint := false
	failures := 0

	for !known || !havePoint {
		var problem string
		permanent := false

		switch {
		case !known:
			latitude, longitude, problem, permanent = p.findZip(ctx, l)
			known = problem == ""
		default:
			point, problem, permanent = p.findPoint(ctx, l, latitude, longitude)
			havePoint = problem == ""
		}

		if problem == "" {
			failures = 0

			continue
		}

		p.deliver(func() { p.onSetup(l, nws.Point{}, problem) })

		if permanent {
			return
		}

		failures++

		if !sleep(ctx, retryDelay(failures, retryMost)) {
			return
		}
	}

	p.deliver(func() { p.onSetup(l, point, "") })

	var since time.Time
	var nextForecast, nextAlerts time.Time
	forecastFailures, alertsFailures := 0, 0

	for ctx.Err() == nil {
		now := time.Now()

		if !now.Before(nextAlerts) {
			alerts, err := p.askAlerts(ctx, latitude, longitude)

			if ctx.Err() != nil {
				return
			}

			p.deliver(func() { p.onAlerts(l, alerts, err) })

			if err == nil {
				alertsFailures = 0
				nextAlerts = now.Add(p.config.AlertsEvery)
			} else {
				alertsFailures++
				nextAlerts = now.Add(retryDelay(alertsFailures, p.config.AlertsEvery))
			}
		}

		if !now.Before(nextForecast) {
			forecast, notModified, modified, err := p.askForecast(ctx, point, since)

			if ctx.Err() != nil {
				return
			}

			p.deliver(func() { p.onForecast(l, forecast, notModified, err) })

			if err == nil {
				forecastFailures = 0
				since = modified
				nextForecast = now.Add(p.config.ForecastEvery)
			} else {
				forecastFailures++
				nextForecast = now.Add(retryDelay(forecastFailures, p.config.ForecastEvery))
			}
		}

		if !sleep(ctx, time.Until(minTime(nextAlerts, nextForecast))) {
			return
		}
	}
}

func minTime(a time.Time, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}

	return b
}

func indexOf(locations []*location, l *location) int {
	for i, candidate := range locations {
		if candidate == l {
			return i
		}
	}

	return 0
}

// findZip looks a zip code up. A zip that is not one is a problem that retrying will not mend.
func (p *plugin) findZip(ctx context.Context, l *location) (latitude float64, longitude float64, problem string, permanent bool) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()

	latitude, longitude, err := p.geo.Lookup(ctx, l.config.zip)

	switch {
	case err == nil:
		return latitude, longitude, "", false
	case errors.Is(err, zip.ErrUnknown):
		return 0, 0, "There is no zip code " + l.config.zip + ".", true
	default:
		return 0, 0, "The zip code " + l.config.zip + " could not be looked up: " + p.describe(err), false
	}
}

// findPoint asks the service where a place's forecast is. A place it does not cover is a problem that retrying will not mend.
func (p *plugin) findPoint(ctx context.Context, l *location, latitude float64, longitude float64) (point nws.Point, problem string, permanent bool) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()

	point, err := p.source.Point(ctx, latitude, longitude)

	switch {
	case err == nil:
		return point, "", false
	case errors.Is(err, nws.ErrUnsupportedLocation):
		return nws.Point{}, p.describe(err) + ".", true
	default:
		return nws.Point{}, "The place could not be found by the weather service: " + p.describe(err), false
	}
}

func (p *plugin) askAlerts(ctx context.Context, latitude float64, longitude float64) ([]data.Alert, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()

	return p.source.Alerts(ctx, latitude, longitude)
}

func (p *plugin) askForecast(ctx context.Context, point nws.Point, since time.Time) (data.Forecast, bool, time.Time, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()

	return p.source.Forecast(ctx, point.ForecastURL, since)
}
