package model

import "strings"

// icon is the bootstrap icon for a sky, by day and by night.
type icon struct{ day, night string }

// icons are the service's codes for the sky (https://www.weather.gov/forecast-icons) and the icon of each. The wind codes are the sky with
// wind, and use the sky's icon: the line of words says it.
var icons = map[string]icon{
	"skc":             {"sun", "moon-stars"},
	"hot":             {"thermometer-sun", "thermometer-sun"},
	"cold":            {"thermometer-snow", "thermometer-snow"},
	"few":             {"cloud-sun", "cloud-moon"},
	"sct":             {"cloud-sun", "cloud-moon"},
	"bkn":             {"clouds", "clouds"},
	"ovc":             {"cloud", "cloud"},
	"fog":             {"cloud-fog2", "cloud-fog2"},
	"haze":            {"cloud-haze2", "cloud-haze2"},
	"smoke":           {"cloud-haze", "cloud-haze"},
	"dust":            {"cloud-haze", "cloud-haze"},
	"rain":            {"cloud-rain-heavy", "cloud-rain-heavy"},
	"rain_showers":    {"cloud-drizzle", "cloud-drizzle"},
	"rain_showers_hi": {"cloud-drizzle", "cloud-drizzle"},
	"tsra":            {"cloud-lightning-rain", "cloud-lightning-rain"},
	"tsra_sct":        {"cloud-lightning-rain", "cloud-lightning-rain"},
	"tsra_hi":         {"cloud-lightning-rain", "cloud-lightning-rain"},
	"snow":            {"cloud-snow", "cloud-snow"},
	"blizzard":        {"snow2", "snow2"},
	"sleet":           {"cloud-sleet", "cloud-sleet"},
	"rain_sleet":      {"cloud-sleet", "cloud-sleet"},
	"snow_sleet":      {"cloud-sleet", "cloud-sleet"},
	"fzra":            {"cloud-hail", "cloud-hail"},
	"rain_fzra":       {"cloud-hail", "cloud-hail"},
	"snow_fzra":       {"cloud-hail", "cloud-hail"},
	"rain_snow":       {"cloud-sleet", "cloud-sleet"},
	"tornado":         {"tornado", "tornado"},
	"hurricane":       {"hurricane", "hurricane"},
	"tropical_storm":  {"hurricane", "hurricane"},
}

// defaultIcon is for a code that is not known, or none: a cloud is never very wrong.
const defaultIcon = "cloud"

// IconFor is the name of the bootstrap icon (without "bi-") for the service's code for a sky, by day or by night.
func IconFor(code string, daytime bool) string {
	code = strings.TrimPrefix(strings.ToLower(code), "wind_")

	found, ok := icons[code]
	if !ok {
		return defaultIcon
	}

	if daytime {
		return found.day
	}

	return found.night
}
