package weather

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/avanha/pmaas-plugin-weather/data"
	"github.com/avanha/pmaas-spi/alert"
)

// criticalEvents are the alerts that mean get somewhere safe now. Only these (and anything the service itself rates Extreme) are critical
// in the alert console, where a critical alert is chimed on every open page: the rest of the warnings, and the watches, are warnings.
var criticalEvents = map[string]bool{
	"tornado warning":             true,
	"tornado emergency":           true,
	"severe thunderstorm warning": true,
	"flash flood warning":         true,
	"flash flood emergency":       true,
	"extreme wind warning":        true,
	"hurricane warning":           true,
	"storm surge warning":         true,
	"tsunami warning":             true,
	"blizzard warning":            true,
	"ice storm warning":           true,
	"dust storm warning":          true,
	"avalanche warning":           true,
	"earthquake warning":          true,
	"volcano warning":             true,
	"civil danger warning":        true,
	"evacuation - immediate":      true,
	"shelter in place warning":    true,
	"nuclear power plant warning": true,
	"radiological hazard warning": true,
	"hazardous materials warning": true,
}

// DefaultAlertSeverity is how severe an alert is in the alert console, and whether it is shown. A test or an exercise is not shown, nor is
// the cancellation of an alert. The few that are dangerous now are critical; the other warnings and the watches (a watch is a warning
// of what may come) are warnings; and the advisories and statements are information.
func DefaultAlertSeverity(a data.Alert) (alert.Severity, bool) {
	if a.Status != "" && !strings.EqualFold(a.Status, "Actual") {
		return alert.SeverityInfo, false
	}

	if strings.EqualFold(a.MessageType, "Cancel") {
		return alert.SeverityInfo, false
	}

	event := strings.ToLower(strings.TrimSpace(a.Event))

	if criticalEvents[event] || strings.EqualFold(a.Severity, "Extreme") {
		return alert.SeverityCritical, true
	}

	switch {
	case strings.HasSuffix(event, " warning"), strings.HasSuffix(event, " watch"), strings.HasSuffix(event, " emergency"):
		return alert.SeverityWarning, true
	default:
		return alert.SeverityInfo, true
	}
}

// severityClass is the colour class of a card's line for an alert.
func severityClass(severity alert.Severity) string {
	switch severity {
	case alert.SeverityCritical:
		return "critical"
	case alert.SeverityWarning:
		return "warning"
	default:
		return "info"
	}
}

const whenLayout = "Mon 3:04 PM"

// whenText says when an alert is, from the alert alone: from when it begins and until when it lasts, so that it says the same all
// through (a message that changed as time passed would be raised again each time).
func whenText(a data.Alert) string {
	until := a.Until()

	begin := a.Onset
	if begin.IsZero() {
		begin = a.Effective
	}

	switch {
	case !begin.IsZero() && !until.IsZero():
		return fmt.Sprintf("%s to %s", begin.Format(whenLayout), until.Format(whenLayout))
	case !until.IsZero():
		return fmt.Sprintf("until %s", until.Format(whenLayout))
	case !begin.IsZero():
		return fmt.Sprintf("from %s", begin.Format(whenLayout))
	default:
		return ""
	}
}

// whenNow says when an alert is for its line on a card: until when, if it is on now, and else from when it comes.
func whenNow(a data.Alert, now time.Time) string {
	until := a.Until()

	switch {
	case !a.Onset.IsZero() && a.Onset.After(now) && !until.IsZero():
		return fmt.Sprintf("%s to %s", a.Onset.Format(whenLayout), until.Format(whenLayout))
	case !a.Onset.IsZero() && a.Onset.After(now):
		return fmt.Sprintf("from %s", a.Onset.Format(whenLayout))
	case !until.IsZero():
		return fmt.Sprintf("until %s", until.Format(whenLayout))
	default:
		return ""
	}
}

// shownAlert is an alert as it is to be shown: the one the service said last of its event, and how many others of the event there are (for
// other areas that the place is in).
type shownAlert struct {
	alert    data.Alert
	severity alert.Severity
	others   int
}

// shown is what is to be shown of what is active: each event once, in the order of the severity and then the event's name.
func shown(active []data.Alert, severity func(data.Alert) (alert.Severity, bool)) []shownAlert {
	byEvent := map[string]*shownAlert{}

	for _, a := range active {
		level, show := severity(a)
		if !show {
			continue
		}

		if existing, ok := byEvent[a.Event]; ok {
			existing.others++

			if a.Sent.After(existing.alert.Sent) {
				existing.alert = a
			}

			if level > existing.severity {
				existing.severity = level
			}

			continue
		}

		byEvent[a.Event] = &shownAlert{alert: a, severity: level}
	}

	result := make([]shownAlert, 0, len(byEvent))
	for _, s := range byEvent {
		result = append(result, *s)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].severity != result[j].severity {
			return result[i].severity > result[j].severity
		}

		return result[i].alert.Event < result[j].alert.Event
	})

	return result
}

// alertKey is the key of the alert in the console: one for each event at each place, so that a newer message about it replaces the
// earlier.
func alertKey(locationID string, event string) string {
	return "weather/" + locationID + "/" + strings.ToLower(strings.ReplaceAll(strings.TrimSpace(event), " ", "-"))
}

// alertText is the title and the message of an alert in the console.
func alertText(location string, s shownAlert) (title string, message string) {
	a := s.alert
	title = fmt.Sprintf("%s: %s", location, a.Event)

	lines := []string{}

	if a.Headline != "" {
		lines = append(lines, a.Headline)
	} else {
		lines = append(lines, a.Event)
	}

	if when := whenText(a); when != "" {
		lines = append(lines, "When: "+when)
	}

	if a.AreaDesc != "" {
		area := a.AreaDesc
		if s.others > 0 {
			area += fmt.Sprintf(" (and %d more)", s.others)
		}

		lines = append(lines, "Where: "+area)
	}

	// The console runs the lines together, so they are told apart by a bar.
	return title, strings.Join(lines, " | ")
}

// planAlerts works out what is to be done to the console to make it show what is active: what to raise (new, or said differently now)
// and which keys to clear. raised is what has been raised, by key, with its message.
type alertRaise struct {
	key      string
	title    string
	message  string
	severity alert.Severity
}

func planAlerts(locationID string, location string, active []data.Alert, severity func(data.Alert) (alert.Severity, bool),
	raised map[string]string) (raise []alertRaise, clear []string) {
	wanted := map[string]bool{}

	for _, s := range shown(active, severity) {
		key := alertKey(locationID, s.alert.Event)
		title, message := alertText(location, s)
		wanted[key] = true

		if raised[key] != message {
			raise = append(raise, alertRaise{key: key, title: title, message: message, severity: s.severity})
		}
	}

	for key := range raised {
		if !wanted[key] {
			clear = append(clear, key)
		}
	}

	sort.Strings(clear)

	return raise, clear
}
