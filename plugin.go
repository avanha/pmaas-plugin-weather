// Package weather is a plugin that shows the weather forecast of the places the assembly gives it, as a card for each with the days to
// come, and puts the alerts of the National Weather Service (watches, warnings, advisories) for those places in the alert console.
package weather

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/avanha/pmaas-plugin-weather/data"
	"github.com/avanha/pmaas-plugin-weather/internal/http"
	"github.com/avanha/pmaas-plugin-weather/internal/model"
	"github.com/avanha/pmaas-plugin-weather/internal/nws"
	"github.com/avanha/pmaas-plugin-weather/internal/zip"
	spi "github.com/avanha/pmaas-spi"
	"github.com/avanha/pmaas-spi/alert"
	"github.com/avanha/pmaas-spi/group"
	"github.com/avanha/pmaas-spi/logging"
)

const (
	defaultDays          = 7
	defaultForecastEvery = 30 * time.Minute
	defaultAlertsEvery   = 5 * time.Minute

	// staleAfter is how many times the forecast's interval it may be old before the card says so.
	staleAfter = 3
)

// locationEntityType is what a place is registered as. Nothing is done through it: the entity stands for the place, so that an assembly can
// name it (in a group, say) and a page of entities of many plugins can show its card. It has no stub.
var locationEntityType = reflect.TypeFor[*data.LocationView]()

type Plugin interface {
	spi.IPMAASPlugin
}

// source is what the forecast and the alerts come from: the National Weather Service, or a fake.
type source interface {
	Point(ctx context.Context, latitude float64, longitude float64) (nws.Point, error)
	Forecast(ctx context.Context, forecastURL string, since time.Time) (data.Forecast, bool, time.Time, error)
	Alerts(ctx context.Context, latitude float64, longitude float64) ([]data.Alert, error)
}

// geocoder finds where a zip code is.
type geocoder interface {
	Lookup(ctx context.Context, zip string) (float64, float64, error)
}

// location is a place at run time. What changes is only touched on the plugin's goroutine.
type location struct {
	id     string
	config *locationConfig

	entityId string

	place    string
	forecast *data.Forecast

	// forecastAt and alertsAt are when each was last got (or found unchanged), and the problems are what went wrong the last time.
	forecastAt      time.Time
	alertsAt        time.Time
	setupProblem    string
	forecastProblem string
	alertsProblem   string

	alerts []data.Alert

	// raised are the keys of the alerts raised in the console, with the message of each.
	raised map[string]string

	// freeze is what the freeze alert, if the place has one, has said.
	freeze freezeState
}

type plugin struct {
	log       *slog.Logger
	config    PluginConfig
	container spi.IPMAASContainer
	http      *http.Handler

	source source
	geo    geocoder
	now    func() time.Time

	locations []*location

	cancel  context.CancelFunc
	workers sync.WaitGroup
	stopped bool
}

var _ spi.IPMAASPlugin = (*plugin)(nil)

// NewPlugin makes the plugin. The config must come from NewPluginConfig.
func NewPlugin(config PluginConfig) Plugin {
	if config.locations == nil {
		panic("weather: the config must come from NewPluginConfig")
	}

	if config.Name == "" {
		config.Name = "Weather"
	}

	if config.Days <= 0 || config.Days > defaultDays {
		config.Days = defaultDays
	}

	if config.ForecastEvery <= 0 {
		config.ForecastEvery = defaultForecastEvery
	}

	if config.AlertsEvery <= 0 {
		config.AlertsEvery = defaultAlertsEvery
	}

	if config.AlertSeverity == nil {
		config.AlertSeverity = DefaultAlertSeverity
	}

	return &plugin{
		config: config,
		http:   http.NewHandler(),
		source: &nws.Client{BaseURL: config.NWSBaseURL, UserAgent: config.UserAgent},
		geo:    &zip.Geocoder{BaseURL: config.ZipBaseURL},
		now:    time.Now,
	}
}

func (p *plugin) ShortName() string {
	return "weather"
}

func (p *plugin) Init(container spi.IPMAASContainer) {
	p.container = container
	p.log = logging.For(container)

	for _, c := range *p.config.locations {
		p.locations = append(p.locations, &location{id: slug(c.name), config: c, raised: map[string]string{}})
	}

	p.http.Init(container, p, p.config.Name)
}

func slug(name string) string {
	var b strings.Builder

	dash := false

	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)

			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')

			dash = true
		}
	}

	return strings.TrimRight(b.String(), "-")
}

func (p *plugin) Start() {
	p.log.Info("starting", "places", len(p.locations))

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	for _, l := range p.locations {
		p.register(l)
	}

	if strings.TrimSpace(p.config.UserAgent) == "" {
		// The service refuses a request that does not say who it is from, so there is nothing to ask. Say so on the cards.
		p.log.Error("not asking for any weather: the plugin's UserAgent is empty (it is how the National Weather Service is told who is asking)")

		for _, l := range p.locations {
			l.setupProblem = "The plugin has no UserAgent, which the National Weather Service requires: set one in the assembly's config."
		}

		return
	}

	for _, l := range p.locations {
		l := l

		p.workers.Add(1)

		go func() {
			defer p.workers.Done()
			p.work(ctx, l)
		}()
	}
}

// register registers a place's entity and gives it its card.
func (p *plugin) register(l *location) {
	id, err := p.container.RegisterEntity("location/"+l.id, locationEntityType, l.config.name, nil)
	if err != nil {
		p.log.Error("unable to register a place", "name", l.config.name, "error", err)

		return
	}

	l.entityId = id
	l.config.handle.setEntityId(id)

	err = p.container.RegisterEntityView(id, func() (spi.EntityView, error) {
		return spi.EntityView{Key: "weather/" + l.id, Category: group.CategoryWeather, Value: p.viewOf(l)}, nil
	})
	if err != nil {
		p.log.Error("unable to give a place its view", "name", l.config.name, "error", err)
	}
}

func (p *plugin) Stop() chan func() {
	p.log.Info("stopping")

	p.stopped = true

	if p.cancel != nil {
		p.cancel()
	}

	callbackCh := make(chan func())

	go func() {
		p.workers.Wait()
		callbackCh <- func() { p.onWorkersStopped(callbackCh) }
	}()

	return callbackCh
}

func (p *plugin) onWorkersStopped(callbackCh chan func()) {
	for _, l := range p.locations {
		if l.entityId == "" {
			continue
		}

		if err := p.container.DeregisterEntity(l.entityId); err != nil {
			p.log.Warn("unable to deregister a place", "name", l.config.name, "error", err)
		}

		l.entityId = ""
		l.config.handle.setEntityId("")
	}

	close(callbackCh)
}

// Views are the cards of the places, in the order they were configured. It is called from the page's goroutine and runs on the plugin's.
func (p *plugin) Views() ([]*data.LocationView, error) {
	return spi.Exec(p.container, func() []*data.LocationView {
		views := make([]*data.LocationView, len(p.locations))

		for i, l := range p.locations {
			views[i] = p.viewOf(l)
		}

		return views
	})
}

// viewOf is what a place's card is drawn from. Runs on the plugin's goroutine.
func (p *plugin) viewOf(l *location) *data.LocationView {
	now := p.now()
	view := &data.LocationView{Name: l.config.name, Place: l.place, AsOf: l.forecastAt}

	problems := []string{}

	if l.setupProblem != "" {
		problems = append(problems, l.setupProblem)
	}

	if l.forecastProblem != "" {
		problems = append(problems, "The forecast could not be updated: "+l.forecastProblem)
	}

	if l.alertsProblem != "" {
		problems = append(problems, "The alerts could not be checked: "+l.alertsProblem)
	}

	view.Problem = strings.Join(problems, " ")

	if l.forecast != nil {
		view.Days = model.Days(l.forecast.Periods, p.config.Days, now)
		view.Now = model.Current(l.forecast.Periods, now)
		view.Stale = !l.forecastAt.IsZero() && now.Sub(l.forecastAt) > staleAfter*p.config.ForecastEvery
	}

	if l.freeze.raised {
		view.Alerts = append(view.Alerts, data.AlertView{
			Event: fmt.Sprintf("Freeze forecast: %d°F %s", l.freeze.low, strings.ToLower(l.freeze.period)),
			Class: severityClass(l.config.freeze.Severity),
		})
	}

	for _, s := range shown(l.alerts, p.config.AlertSeverity) {
		view.Alerts = append(view.Alerts, data.AlertView{
			Event: s.alert.Event,
			Class: severityClass(s.severity),
			When:  whenNow(s.alert, now),
		})
	}

	return view
}

// onSetup takes in what a place's setup came to: where it is, or why that could not be found.
func (p *plugin) onSetup(l *location, point nws.Point, problem string) {
	if p.stopped {
		return
	}

	l.setupProblem = problem

	if problem == "" {
		l.place = point.Place
	}
}

// onForecast takes in the answer to a request for a forecast: a new one, the news that it has not changed, or what went wrong, in which
// case the card keeps showing the last one, marked as old once it is.
func (p *plugin) onForecast(l *location, forecast data.Forecast, notModified bool, err error) {
	if p.stopped {
		return
	}

	if err != nil {
		l.forecastProblem = err.Error()

		return
	}

	l.forecastProblem = ""
	l.forecastAt = p.now()

	if !notModified {
		l.forecast = &forecast
	}

	p.checkFreeze(l)
}

// freezeKey is the key of a place's own alert in the console.
func freezeKey(l *location) string {
	return alertKey(l.id, "freeze forecast")
}

// checkFreeze raises or clears the place's own alert on a forecast freeze, from the forecast it has now. Runs on the plugin's goroutine.
func (p *plugin) checkFreeze(l *location) {
	options := l.config.freeze
	if options == nil || l.forecast == nil {
		return
	}

	decision := decideFreeze(*options, l.forecast.Periods, p.now(), l.freeze)

	switch {
	case decision.raise:
		title := l.config.name + ": freeze forecast"

		if err := alert.Raise(p.container, p.ShortName(), freezeKey(l), options.Severity, title, decision.text); err != nil {
			p.log.Error("unable to raise the freeze alert", "name", l.config.name, "error", err)

			return
		}

		l.freeze = freezeState{raised: true, low: decision.low, period: decision.period}
	case decision.clear:
		if err := alert.Clear(p.container, p.ShortName(), freezeKey(l)); err != nil {
			p.log.Error("unable to clear the freeze alert", "name", l.config.name, "error", err)

			return
		}

		l.freeze = freezeState{}
	}
}

// onAlerts takes in what is active and makes the alert console show it.
func (p *plugin) onAlerts(l *location, active []data.Alert, err error) {
	if p.stopped {
		return
	}

	if err != nil {
		// What was raised stays raised: not knowing is not the same as it being over.
		l.alertsProblem = err.Error()

		return
	}

	l.alertsProblem = ""
	l.alertsAt = p.now()
	l.alerts = active

	raise, clear := planAlerts(l.id, l.config.name, active, p.config.AlertSeverity, l.raised)

	for _, r := range raise {
		if err := alert.Raise(p.container, p.ShortName(), r.key, r.severity, r.title, r.message); err != nil {
			p.log.Error("unable to raise an alert", "key", r.key, "error", err)

			continue
		}

		l.raised[r.key] = r.message
	}

	for _, key := range clear {
		if err := alert.Clear(p.container, p.ShortName(), key); err != nil {
			p.log.Error("unable to clear an alert", "key", key, "error", err)

			continue
		}

		delete(l.raised, key)
	}
}

// deliver runs a function on the plugin's goroutine.
func (p *plugin) deliver(f func()) {
	if err := p.container.EnqueueOnPluginGoRoutine(f); err != nil {
		p.log.Debug("unable to hand an answer to the plugin", "error", err)
	}
}

// describe is an error in a few words for a card.
func (p *plugin) describe(err error) string {
	if err == nil {
		return ""
	}

	return strings.TrimSpace(err.Error())
}
