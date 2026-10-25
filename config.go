package weather

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/avanha/pmaas-plugin-weather/data"
	"github.com/avanha/pmaas-spi/alert"
	"github.com/avanha/pmaas-spi/entity"
)

// PluginConfig configures the plugin: who is asking the weather service, and the places. Use NewPluginConfig.
type PluginConfig struct {
	// UserAgent says who is asking, as the National Weather Service requires: the program and a way to reach you, such as
	// "pmaas (you@example.com)". There is no default: the plugin does not start without one.
	UserAgent string

	// Name is what the plugin is called in the menu. The default is "Weather".
	Name string

	// Days is how many days a card shows, up to 7, which is all the service forecasts. The default is 7, which is what fits a card.
	Days int

	// ForecastEvery is how often a forecast is asked for again (the service makes a new one about every hour), and AlertsEvery how often
	// the alerts are. The defaults are 30 minutes and 5 minutes.
	ForecastEvery time.Duration
	AlertsEvery   time.Duration

	// AlertSeverity says how severe an alert is in the alert console, and whether it is to be shown at all. The default is
	// DefaultAlertSeverity.
	AlertSeverity func(data.Alert) (alert.Severity, bool)

	// NWSBaseURL and ZipBaseURL replace the services' addresses, for a test.
	NWSBaseURL string
	ZipBaseURL string

	locations *[]*locationConfig
}

// locationConfig is a place the assembly configured.
type locationConfig struct {
	name      string
	latitude  float64
	longitude float64
	zip       string
	handle    *LocationHandle
}

// NewPluginConfig is a configuration with no places in it.
func NewPluginConfig() PluginConfig {
	return PluginConfig{locations: &[]*locationConfig{}}
}

// AddLocation adds a place by its coordinates (decimal degrees, south and west negative). The handle it returns is a reference to the
// place's entity, to put its card in a group (see the groups plugin).
func (c PluginConfig) AddLocation(name string, latitude float64, longitude float64) *LocationHandle {
	if c.locations == nil {
		panic("weather: the config must come from NewPluginConfig")
	}

	if latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
		panic(fmt.Sprintf("weather: the coordinates of %q (%v, %v) are not on the Earth", name, latitude, longitude))
	}

	return c.add(&locationConfig{name: name, latitude: latitude, longitude: longitude})
}

// AddZip adds a place by its five digit US zip code, which is looked up when the plugin starts (the middle of the zip is used).
func (c PluginConfig) AddZip(name string, zip string) *LocationHandle {
	if c.locations == nil {
		panic("weather: the config must come from NewPluginConfig")
	}

	zip = strings.TrimSpace(zip)

	if len(zip) != 5 || strings.Trim(zip, "0123456789") != "" {
		panic(fmt.Sprintf("weather: the zip code of %q (%q) is not five digits", name, zip))
	}

	return c.add(&locationConfig{name: name, zip: zip})
}

func (c PluginConfig) add(location *locationConfig) *LocationHandle {
	if location.name == "" {
		panic("weather: a place needs a name")
	}

	for _, existing := range *c.locations {
		if strings.EqualFold(existing.name, location.name) {
			panic(fmt.Sprintf("weather: there is already a place called %q", location.name))
		}
	}

	location.handle = &LocationHandle{name: location.name}
	*c.locations = append(*c.locations, location)

	return location.handle
}

// LocationHandle is the assembly's handle on a place: its entity, once the plugin has registered it.
type LocationHandle struct {
	name     string
	entityId atomic.Pointer[string]
}

// A handle is an entity.Ref, so that the assembly can put the place's card in a group.
var _ entity.Ref = (*LocationHandle)(nil)

// Name is the place's name.
func (h *LocationHandle) Name() string {
	return h.name
}

// RegisteredId is the id of the place's entity, or false until the plugin has started and after it has stopped.
func (h *LocationHandle) RegisteredId() (string, bool) {
	if id := h.entityId.Load(); id != nil && *id != "" {
		return *id, true
	}

	return "", false
}

func (h *LocationHandle) setEntityId(id string) {
	h.entityId.Store(&id)
}
