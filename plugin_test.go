package weather

import (
	"context"
	"errors"
	iofs "io/fs"
	nethttp "net/http"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/avanha/pmaas-plugin-weather/data"
	"github.com/avanha/pmaas-plugin-weather/internal/nws"
	"github.com/avanha/pmaas-plugin-weather/internal/zip"
	spi "github.com/avanha/pmaas-spi"
	"github.com/avanha/pmaas-spi/alert"
	"github.com/avanha/pmaas-spi/group"
)

var mdt = time.FixedZone("MDT", -6*3600)

// fakeContainer runs what is enqueued for the plugin's goroutine at once, under a lock, and keeps what the plugin registers and broadcasts.
type fakeContainer struct {
	spi.IPMAASContainer

	mu         sync.Mutex
	entities   map[string]string // id -> name
	views      map[string]spi.EntityViewFunc
	deregister []string
	events     []any
	nextId     int
}

func newContainer() *fakeContainer {
	return &fakeContainer{entities: map[string]string{}, views: map[string]spi.EntityViewFunc{}}
}

// What Init does to the server, which the tests of the plugin's logic have no use for.
func (c *fakeContainer) ProvideContentFS(iofs.FS, string) {}
func (c *fakeContainer) EnableStaticContent(string)       {}
func (c *fakeContainer) AddRouteWithOptions(string, nethttp.HandlerFunc, *spi.HttpHandlerOptions) {
}
func (c *fakeContainer) RegisterEntityRenderer(reflect.Type, spi.EntityRendererFactory) {}

func (c *fakeContainer) EnqueueOnPluginGoRoutine(f func()) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	f()

	return nil
}

func (c *fakeContainer) RegisterEntity(unique string, _ reflect.Type, name string, _ spi.EntityStubFactoryFunc) (string, error) {
	c.nextId++
	id := "weather_" + unique
	c.entities[id] = name

	return id, nil
}

func (c *fakeContainer) RegisterEntityView(id string, view spi.EntityViewFunc) error {
	c.views[id] = view

	return nil
}

func (c *fakeContainer) DeregisterEntity(id string) error {
	c.deregister = append(c.deregister, id)
	delete(c.entities, id)

	return nil
}

func (c *fakeContainer) BroadcastEvent(_ string, event any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.events = append(c.events, event)

	return nil
}

func (c *fakeContainer) raised() []alert.RaisedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()

	var raised []alert.RaisedEvent

	for _, e := range c.events {
		if r, ok := e.(alert.RaisedEvent); ok {
			raised = append(raised, r)
		}
	}

	return raised
}

func (c *fakeContainer) cleared() []alert.ClearedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()

	var cleared []alert.ClearedEvent

	for _, e := range c.events {
		if r, ok := e.(alert.ClearedEvent); ok {
			cleared = append(cleared, r)
		}
	}

	return cleared
}

// fakeSource is a weather service that answers from what the test has set, and counts what it was asked.
type fakeSource struct {
	mu sync.Mutex

	pointErr    error
	forecastErr error
	alertsErr   error
	forecast    data.Forecast
	alerts      []data.Alert
	notModified bool

	points    int
	forecasts []time.Time // when each was asked, in the virtual time of the test
	alertAsks []time.Time
	sinces    []time.Time
	asked     [][2]float64
}

func (s *fakeSource) Point(_ context.Context, latitude float64, longitude float64) (nws.Point, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.points++
	s.asked = append(s.asked, [2]float64{latitude, longitude})

	if s.pointErr != nil {
		return nws.Point{}, s.pointErr
	}

	return nws.Point{ForecastURL: "https://example.test/forecast", Place: "Lafayette, CO", TimeZone: "America/Denver"}, nil
}

func (s *fakeSource) Forecast(_ context.Context, _ string, since time.Time) (data.Forecast, bool, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.forecasts = append(s.forecasts, time.Now())
	s.sinces = append(s.sinces, since)

	if s.forecastErr != nil {
		return data.Forecast{}, false, time.Time{}, s.forecastErr
	}

	return s.forecast, s.notModified, time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC), nil
}

func (s *fakeSource) Alerts(_ context.Context, _ float64, _ float64) ([]data.Alert, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.alertAsks = append(s.alertAsks, time.Now())

	return s.alerts, s.alertsErr
}

// What the fake was asked, read under its lock, because the plugin's worker asks from its own goroutine.
func (s *fakeSource) numPoints() int    { s.mu.Lock(); defer s.mu.Unlock(); return s.points }
func (s *fakeSource) numAlerts() int    { s.mu.Lock(); defer s.mu.Unlock(); return len(s.alertAsks) }
func (s *fakeSource) numForecasts() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.forecasts) }
func (s *fakeSource) forecastTimes() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time{}, s.forecasts...)
}
func (s *fakeSource) sincesCopy() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time{}, s.sinces...)
}
func (s *fakeSource) askedCopy() [][2]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][2]float64{}, s.asked...)
}

type fakeGeo struct {
	latitude, longitude float64
	err                 error
	asked               []string
}

func (g *fakeGeo) Lookup(_ context.Context, zip string) (float64, float64, error) {
	g.asked = append(g.asked, zip)

	return g.latitude, g.longitude, g.err
}

// period is a 12 hour period starting at the hour of the day in October 2026.
func period(day int, hour int, daytime bool, temp int, precip int, icon string) data.Period {
	start := time.Date(2026, 10, day, hour, 0, 0, 0, mdt)

	return data.Period{
		Name: "p", Start: start, End: start.Add(12 * time.Hour), Daytime: daytime, Temperature: temp, Unit: "F",
		PrecipChance: precip, Icon: icon, ShortForecast: icon + " words",
	}
}

func threeDays() data.Forecast {
	return data.Forecast{Periods: []data.Period{
		period(25, 6, true, 60, 10, "sct"), period(25, 18, false, 35, 30, "ovc"),
		period(26, 6, true, 61, 0, "skc"), period(26, 18, false, 36, 0, "skc"),
		period(27, 6, true, 62, 0, "skc"), period(27, 18, false, 37, 0, "skc"),
	}}
}

func floodWarning() data.Alert {
	return data.Alert{
		ID: "urn:1", Event: "Flood Warning", Severity: "Severe", Status: "Actual", MessageType: "Alert", Headline: "Flood Warning issued",
		AreaDesc: "Boulder, CO", Sent: time.Date(2026, 10, 25, 9, 0, 0, 0, mdt),
		Onset: time.Date(2026, 10, 25, 10, 0, 0, 0, mdt), Ends: time.Date(2026, 10, 25, 20, 0, 0, 0, mdt),
	}
}

type harness struct {
	t         *testing.T
	container *fakeContainer
	plugin    *plugin
	source    *fakeSource
	geo       *fakeGeo
	clock     time.Time
}

// newHarness is a plugin with the place and a clock the test sets, started or not.
func newHarness(t *testing.T, configure func(c PluginConfig)) *harness {
	t.Helper()

	config := NewPluginConfig()
	config.UserAgent = "pmaas-test (nobody@example.com)"

	if configure != nil {
		configure(config)
	}

	h := &harness{t: t, container: newContainer(), source: &fakeSource{forecast: threeDays()}, geo: &fakeGeo{latitude: 39.998, longitude: -105.0963},
		clock: time.Date(2026, 10, 25, 7, 30, 0, 0, mdt)}

	h.plugin = NewPlugin(config).(*plugin)
	h.plugin.source = h.source
	h.plugin.geo = h.geo
	h.plugin.now = func() time.Time { return h.clock }
	h.plugin.Init(h.container)

	return h
}

// stop stops the plugin the way the server does: it takes the function the plugin hands back, and runs it.
func (h *harness) stop() {
	h.t.Helper()

	(<-h.plugin.Stop())()
}

func TestConfig_PlacesAreCheckedAndOfferedAsHandles(t *testing.T) {
	config := NewPluginConfig()
	home := config.AddLocation("Home", 39.9967, -105.0944)
	office := config.AddZip("Office", "80026")

	if home.Name() != "Home" || office.Name() != "Office" {
		t.Error("names")
	}

	if _, ok := home.RegisteredId(); ok {
		t.Error("an id before the plugin started")
	}

	for name, f := range map[string]func(){
		"a duplicate name": func() { config.AddLocation("home", 1, 1) },
		"no name":          func() { config.AddLocation("", 1, 1) },
		"bad latitude":     func() { config.AddLocation("A", 91, 0) },
		"bad longitude":    func() { config.AddLocation("B", 0, 181) },
		"short zip":        func() { config.AddZip("C", "8002") },
		"letters in a zip": func() { config.AddZip("D", "8002a") },
		"no config":        func() { PluginConfig{}.AddLocation("E", 1, 1) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s was accepted", name)
				}
			}()

			f()
		}()
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"Home": "home", "Big Horn": "big-horn", "  Dorie's  Place!": "dorie-s-place", "A/B": "a-b", "": ""} {
		if got := slug(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestDefaultAlertSeverity(t *testing.T) {
	for _, c := range []struct {
		event, severity, status, messageType string
		want                                 alert.Severity
		shown                                bool
	}{
		{"Tornado Warning", "Extreme", "Actual", "Alert", alert.SeverityCritical, true},
		{"Severe Thunderstorm Warning", "Severe", "Actual", "Alert", alert.SeverityCritical, true},
		{"Flash Flood Warning", "Severe", "Actual", "Update", alert.SeverityCritical, true},
		{"Blizzard Warning", "Severe", "Actual", "Alert", alert.SeverityCritical, true},
		{"Something Unheard Of", "Extreme", "Actual", "Alert", alert.SeverityCritical, true},
		{"Flood Warning", "Severe", "Actual", "Update", alert.SeverityWarning, true},
		{"Winter Storm Warning", "Severe", "Actual", "Alert", alert.SeverityWarning, true},
		{"Flood Watch", "Severe", "Actual", "Update", alert.SeverityWarning, true},
		{"Tornado Watch", "Severe", "Actual", "Alert", alert.SeverityWarning, true},
		{"Wind Advisory", "Moderate", "Actual", "Alert", alert.SeverityInfo, true},
		{"Special Weather Statement", "Moderate", "Actual", "Alert", alert.SeverityInfo, true},
		{"Tornado Warning", "Extreme", "Test", "Alert", alert.SeverityInfo, false},
		{"Tornado Warning", "Extreme", "Actual", "Cancel", alert.SeverityInfo, false},
	} {
		got, shown := DefaultAlertSeverity(data.Alert{Event: c.event, Severity: c.severity, Status: c.status, MessageType: c.messageType})
		if got != c.want || shown != c.shown {
			t.Errorf("%s (%s, %s, %s): %v shown %v, want %v shown %v", c.event, c.severity, c.status, c.messageType, got, shown, c.want, c.shown)
		}
	}
}

func TestAlerts_AreRaisedOnceChangedInPlaceAndClearedWhenTheyAreOver(t *testing.T) {
	config := func(c PluginConfig) { c.AddLocation("Home", 39.9967, -105.0944) }
	h := newHarness(t, config)
	l := h.plugin.locations[0]

	// A warning and an advisory.
	advisory := data.Alert{Event: "Wind Advisory", Severity: "Moderate", Status: "Actual", MessageType: "Alert", Headline: "Wind Advisory issued"}
	h.plugin.onAlerts(l, []data.Alert{floodWarning(), advisory}, nil)

	raised := h.container.raised()
	if len(raised) != 2 || raised[0].Key != "weather/home/flood-warning" || raised[0].Severity != alert.SeverityWarning ||
		raised[0].Source != "weather" || raised[1].Key != "weather/home/wind-advisory" || raised[1].Severity != alert.SeverityInfo {
		t.Fatalf("%+v", raised)
	}

	if raised[0].Title != "Home: Flood Warning" {
		t.Errorf("title %q", raised[0].Title)
	}

	for _, want := range []string{"Flood Warning issued", "When: Sun 10:00 AM to Sun 8:00 PM", "Where: Boulder, CO"} {
		if !contains(raised[0].Message, want) {
			t.Errorf("missing %q in %q", want, raised[0].Message)
		}
	}

	// The same again says nothing.
	h.plugin.onAlerts(l, []data.Alert{floodWarning(), advisory}, nil)

	if len(h.container.raised()) != 2 {
		t.Errorf("raised again: %d", len(h.container.raised()))
	}

	// A message that says it differently updates the alert, under the same key.
	worse := floodWarning()
	worse.Headline = "Flood Warning extended"
	h.plugin.onAlerts(l, []data.Alert{worse, advisory}, nil)

	if raised = h.container.raised(); len(raised) != 3 || raised[2].Key != "weather/home/flood-warning" {
		t.Fatalf("%+v", raised)
	}

	// The advisory is over: it is cleared, and the warning is not.
	h.plugin.onAlerts(l, []data.Alert{worse}, nil)

	if cleared := h.container.cleared(); len(cleared) != 1 || cleared[0].Key != "weather/home/wind-advisory" || cleared[0].Source != "weather" {
		t.Errorf("%+v", cleared)
	}

	// Not being able to ask does not clear anything: it is not known to be over.
	h.plugin.onAlerts(l, nil, errors.New("503"))

	if len(h.container.cleared()) != 1 || l.alertsProblem != "503" {
		t.Errorf("%d cleared, problem %q", len(h.container.cleared()), l.alertsProblem)
	}

	h.plugin.onAlerts(l, nil, nil)

	if len(h.container.cleared()) != 2 || l.alertsProblem != "" {
		t.Errorf("%d cleared, problem %q", len(h.container.cleared()), l.alertsProblem)
	}
}

func contains(s string, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}

	return false
}

func TestAlerts_SeveralOfTheSameEventAreOneAlertForTheNewestAndTestsAreNotShown(t *testing.T) {
	h := newHarness(t, func(c PluginConfig) { c.AddLocation("Home", 1, 1) })
	l := h.plugin.locations[0]

	older, newer := floodWarning(), floodWarning()
	older.ID, older.Sent, older.AreaDesc = "a", older.Sent.Add(-time.Hour), "Weld, CO"
	newer.ID, newer.AreaDesc = "b", "Larimer, CO"
	test := floodWarning()
	test.Event, test.Status = "Tornado Warning", "Test"

	h.plugin.onAlerts(l, []data.Alert{older, newer, test}, nil)

	raised := h.container.raised()
	if len(raised) != 1 || !contains(raised[0].Message, "Larimer, CO (and 1 more)") {
		t.Fatalf("%+v", raised)
	}
}

func TestAlerts_TheSeverityIsTheAssembliesToChoose(t *testing.T) {
	h := newHarness(t, func(c PluginConfig) {
		c.AddLocation("Home", 1, 1)
		c.AlertSeverity = func(a data.Alert) (alert.Severity, bool) {
			return alert.SeverityCritical, a.Event == "Flood Warning"
		}
	})
	l := h.plugin.locations[0]

	// NewPlugin takes the config by value, but the places are shared: take the function the harness's plugin has.
	h.plugin.config.AlertSeverity = func(a data.Alert) (alert.Severity, bool) { return alert.SeverityCritical, a.Event == "Flood Warning" }

	h.plugin.onAlerts(l, []data.Alert{floodWarning(), {Event: "Wind Advisory", Status: "Actual"}}, nil)

	if raised := h.container.raised(); len(raised) != 1 || raised[0].Severity != alert.SeverityCritical {
		t.Errorf("%+v", raised)
	}
}

func TestView_HasTheDaysTheCurrentStretchAndTheAlertsAndSaysWhenItIsOld(t *testing.T) {
	h := newHarness(t, func(c PluginConfig) { c.AddLocation("Home", 39.9967, -105.0944) })
	l := h.plugin.locations[0]

	h.plugin.onSetup(l, nws.Point{Place: "Lafayette, CO"}, "")
	h.plugin.onForecast(l, threeDays(), false, nil)
	h.plugin.onAlerts(l, []data.Alert{floodWarning()}, nil)

	view := h.plugin.viewOf(l)

	if view.Name != "Home" || view.Place != "Lafayette, CO" || view.Stale || view.Problem != "" || len(view.Days) != 3 ||
		view.Days[0].Label != "Today" || view.Days[0].High != 60 || view.Now == nil || view.Now.Temperature != 60 {
		t.Fatalf("%+v", view)
	}

	if len(view.Alerts) != 1 || view.Alerts[0].Class != "warning" || view.Alerts[0].Event != "Flood Warning" || view.Alerts[0].When == "" {
		t.Errorf("%+v", view.Alerts)
	}

	// An hour and a half later, and still no news: old, and the card says what went wrong.
	h.clock = h.clock.Add(95 * time.Minute)
	h.plugin.onForecast(l, data.Forecast{}, false, errors.New("the weather service answered 503"))

	view = h.plugin.viewOf(l)

	if !view.Stale || len(view.Days) == 0 || !contains(view.Problem, "The forecast could not be updated: the weather service answered 503") {
		t.Errorf("%+v", view)
	}
}

func TestView_ThePluginsDaysSettingLimitsTheDays(t *testing.T) {
	h := newHarness(t, func(c PluginConfig) {
		c.AddLocation("Home", 1, 1)
		c.Days = 2
	})
	h.plugin.config.Days = 2
	l := h.plugin.locations[0]
	h.plugin.onForecast(l, threeDays(), false, nil)

	if got := len(h.plugin.viewOf(l).Days); got != 2 {
		t.Errorf("%d days", got)
	}
}

func TestStart_RegistersAnEntityWithACardInTheWeatherKindAndGivesTheHandleItsId(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var handle *LocationHandle

		h := newHarness(t, func(c PluginConfig) { handle = c.AddLocation("Big Horn", 44.6, -107.4) })
		h.plugin.Start()

		id, ok := handle.RegisteredId()
		if !ok || id != "weather_location/big-horn" || h.container.entities[id] != "Big Horn" {
			t.Fatalf("%q %v %v", id, ok, h.container.entities)
		}

		view, err := h.container.views[id]()
		if err != nil || view.Key != "weather/big-horn" || view.Category != group.CategoryWeather {
			t.Fatalf("%+v %v", view, err)
		}

		if card, ok := view.Value.(*data.LocationView); !ok || card.Name != "Big Horn" {
			t.Errorf("%#v", view.Value)
		}

		callbacks := h.plugin.Stop()
		(<-callbacks)()

		if _, ok := handle.RegisteredId(); ok || len(h.container.deregister) != 1 {
			t.Errorf("still registered: %v", h.container.deregister)
		}
	})
}

func TestWork_AsksForTheAlertsAndTheForecastAtTheirOwnIntervalsAndNotModifiedIsFine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, func(c PluginConfig) { c.AddLocation("Home", 39.9967, -105.0944) })
		h.plugin.Start()

		start := time.Now()

		time.Sleep(time.Second)
		synctest.Wait()

		if h.source.numPoints() != 1 || h.source.numAlerts() != 1 || h.source.numForecasts() != 1 {
			t.Fatalf("points %d alerts %d forecasts %d", h.source.numPoints(), h.source.numAlerts(), h.source.numForecasts())
		}

		if h.source.askedCopy()[0] != [2]float64{39.9967, -105.0944} {
			t.Errorf("asked about %v", h.source.askedCopy())
		}

		// Alerts every 5 minutes, the forecast every 30.
		time.Sleep(31 * time.Minute)
		synctest.Wait()

		if got := h.source.numAlerts(); got != 7 {
			t.Errorf("%d alert requests in 31 minutes", got)
		}

		if got := h.source.numForecasts(); got != 2 {
			t.Errorf("%d forecast requests in 31 minutes", got)
		}

		// The second asked if it was modified since what the first said.
		if !h.source.sincesCopy()[0].IsZero() || h.source.sincesCopy()[1].IsZero() {
			t.Errorf("since %v", h.source.sincesCopy())
		}

		if gap := h.source.forecastTimes()[1].Sub(start); gap < 30*time.Minute || gap > 31*time.Minute {
			t.Errorf("second forecast at %v", gap)
		}

		h.stop()
	})
}

func TestWork_AFailureIsRetriedSoonerAndLongerAndLongerAndTheOldForecastStays(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, func(c PluginConfig) { c.AddLocation("Home", 1, 1) })
		h.plugin.Start()

		time.Sleep(time.Second)
		synctest.Wait()

		h.source.mu.Lock()
		h.source.forecastErr = &nws.HTTPError{Status: 503, Body: "busy"}
		h.source.mu.Unlock()

		// The next forecast is due in 30 minutes; it fails, and is tried again after 30 seconds, then 60, then 120.
		time.Sleep(30*time.Minute + time.Second)
		synctest.Wait()

		before := h.source.numForecasts()

		time.Sleep(30 * time.Second)
		synctest.Wait()

		time.Sleep(60 * time.Second)
		synctest.Wait()

		time.Sleep(120 * time.Second)
		synctest.Wait()

		if got := h.source.numForecasts() - before; got != 3 {
			t.Errorf("%d retries in 3.5 minutes after the first failure", got)
		}

		l := h.plugin.locations[0]

		if l.forecast == nil || l.forecastProblem == "" {
			t.Errorf("forecast kept: %v, problem %q", l.forecast != nil, l.forecastProblem)
		}

		// It recovers.
		h.source.mu.Lock()
		h.source.forecastErr = nil
		h.source.mu.Unlock()

		time.Sleep(5 * time.Minute)
		synctest.Wait()

		if l.forecastProblem != "" {
			t.Errorf("problem %q", l.forecastProblem)
		}

		h.stop()
	})
}

func TestWork_AZipIsLookedUpFirstAndAPlaceTheServiceDoesNotKnowIsSaidOnTheCardAndLeftAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, func(c PluginConfig) { c.AddZip("Office", "80026") })
		h.plugin.Start()

		time.Sleep(time.Second)
		synctest.Wait()

		if len(h.geo.asked) != 1 || h.geo.asked[0] != "80026" || h.source.askedCopy()[0] != [2]float64{39.998, -105.0963} || h.source.numForecasts() != 1 {
			t.Fatalf("%v %v", h.geo.asked, h.source.askedCopy())
		}

		h.stop()
	})

	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, func(c PluginConfig) { c.AddZip("Nowhere", "99999") })
		h.geo.err = zip.ErrUnknown
		h.plugin.Start()

		time.Sleep(time.Hour)
		synctest.Wait()

		l := h.plugin.locations[0]

		if len(h.geo.asked) != 1 || !contains(l.setupProblem, "There is no zip code 99999") || h.source.numPoints() != 0 {
			t.Errorf("%v %q %d", h.geo.asked, l.setupProblem, h.source.numPoints())
		}

		h.stop()
	})

	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, func(c PluginConfig) { c.AddLocation("London", 51.5, -0.12) })
		h.source.pointErr = nws.ErrUnsupportedLocation
		h.plugin.Start()

		time.Sleep(time.Hour)
		synctest.Wait()

		if h.source.numPoints() != 1 || !contains(h.plugin.locations[0].setupProblem, "covers the United States") || h.source.numForecasts() != 0 {
			t.Errorf("%d %q", h.source.numPoints(), h.plugin.locations[0].setupProblem)
		}

		h.stop()
	})
}

func TestWork_ASetupThatFailsForNowIsTriedAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, func(c PluginConfig) { c.AddLocation("Home", 1, 1) })
		h.source.pointErr = &nws.HTTPError{Status: 503, Body: "busy"}
		h.plugin.Start()

		time.Sleep(time.Minute)
		synctest.Wait()

		if h.source.numPoints() != 2 || h.plugin.locations[0].setupProblem == "" {
			t.Fatalf("%d %q", h.source.numPoints(), h.plugin.locations[0].setupProblem)
		}

		h.source.mu.Lock()
		h.source.pointErr = nil
		h.source.mu.Unlock()

		time.Sleep(10 * time.Minute)
		synctest.Wait()

		if l := h.plugin.locations[0]; l.setupProblem != "" || l.place != "Lafayette, CO" || h.source.numForecasts() == 0 {
			t.Errorf("%q %q %d", l.setupProblem, l.place, h.source.numForecasts())
		}

		h.stop()
	})
}

func TestStart_WithoutAUserAgentAsksForNothingAndSaysSo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, func(c PluginConfig) {
			c.AddLocation("Home", 1, 1)
			c.UserAgent = "  "
		})
		h.plugin.config.UserAgent = "  "
		h.plugin.Start()

		time.Sleep(time.Hour)
		synctest.Wait()

		if h.source.numPoints() != 0 || !contains(h.plugin.viewOf(h.plugin.locations[0]).Problem, "UserAgent") {
			t.Errorf("%d %q", h.source.numPoints(), h.plugin.viewOf(h.plugin.locations[0]).Problem)
		}

		h.stop()
	})
}
