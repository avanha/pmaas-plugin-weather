// Package data is what the weather plugin is made of that others see: what the forecast service says, and what a card of it is drawn from.
package data

import "time"

// Period is one stretch of a forecast, as the service gives it: a day or a night, 12 hours.
type Period struct {
	// Name is what the service calls it: "This Afternoon", "Tonight", "Monday".
	Name       string
	Start, End time.Time

	// Daytime is whether it is a day period, as opposed to a night.
	Daytime bool

	// Temperature is in Unit, "F" or "C".
	Temperature int
	Unit        string

	// PrecipChance is the chance of precipitation in percent, or -1 if the service does not say.
	PrecipChance int

	// ShortForecast is a few words ("Slight Chance Rain Showers"), and Detailed a sentence or more.
	ShortForecast string
	Detailed      string
	WindSpeed     string
	WindDirection string

	// Icon is the service's code for the sky ("rain_showers", "sct", "tsra").
	Icon string
}

// Forecast is what the service said about a place.
type Forecast struct {
	// UpdatedAt is when the forecast was made, by the service.
	UpdatedAt time.Time
	Periods   []Period
}

// Alert is a warning, watch or advisory that is in effect or coming, as the service gives it.
type Alert struct {
	ID          string
	Event       string // "Flood Warning"
	Severity    string // Extreme, Severe, Moderate, Minor, Unknown
	Urgency     string
	Certainty   string
	Status      string // Actual, Test, ...
	MessageType string // Alert, Update, Cancel
	Headline    string
	Description string
	Instruction string
	AreaDesc    string
	SenderName  string

	Sent      time.Time
	Effective time.Time
	Onset     time.Time
	Expires   time.Time
	Ends      time.Time
}

// Until is when the alert is over: when it ends, or else when it expires.
func (a Alert) Until() time.Time {
	if !a.Ends.IsZero() {
		return a.Ends
	}

	return a.Expires
}

// Day is one day of a forecast, made of its day period and the night after it.
type Day struct {
	// Label is "Today" (or "Tonight" when only the night is left), and else the day of the week, "Mon".
	Label string

	// Date is the day, as "2006-01-02" in the place's own time.
	Date string

	// High is the day period's temperature and Low the night's, if there is one.
	High, Low       int
	HasHigh, HasLow bool

	// Precip is the greater of the chances of precipitation, or -1 if there is none known.
	Precip int

	// Icon is the name of a bootstrap icon for the sky, and Short is in words what the day's period says.
	Icon  string
	Short string
}

// AlertView is an alert as the card shows it.
type AlertView struct {
	Event string

	// Class is "critical", "warning" or "info", for the colour.
	Class string

	// When says in words when it is: "until Mon 3:00 PM", "Mon 6:00 AM to Tue 12:00 PM".
	When string
}

// Now is the current stretch of a forecast, for the line under the heading.
type Now struct {
	Icon        string
	Temperature int
	Unit        string
	Short       string
}

// LocationView is what a card of a place's weather is drawn from.
type LocationView struct {
	Name string

	// Place is the town the service puts the coordinates in: "Lafayette, CO".
	Place string

	// AsOf is when the forecast was last fetched, and Stale is that it is older than it should be.
	AsOf  time.Time
	Stale bool

	// Problem is what is wrong, if anything: the place cannot be found, or the service is not answering.
	Problem string

	Now    *Now
	Alerts []AlertView
	Days   []Day
}
