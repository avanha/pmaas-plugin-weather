# PMAAS Weather Plugin

Shows the weather forecast for the places an assembly gives it, a card for each, and puts the alerts of the
[National Weather Service](https://www.weather.gov/documentation/services-web-api) for those places (watches, warnings and
advisories, including the ones that are coming) in the alert console. It is for the United States: that is where the service is.
It needs no key.

```go
conf := weather.NewPluginConfig()
// The service asks to be told who is asking, and how to reach them. There is no default, and the plugin asks for nothing without one.
conf.UserAgent = "pmaas (you@example.com)"
home := conf.AddLocation("Home", 39.9967, -105.0944) // by coordinates...
conf.AddZip("Office", "80302")                       // ...or by zip code, which is looked up when the plugin starts
coreConfig.AddPlugin(weather.NewPlugin(conf), config.PluginConfig{})
```

`AddLocation` and `AddZip` return a handle, an `entity.Ref`, so that a place's card can go in a group (the groups plugin): `cougar.Add(home)`.
Cards are listed under the kind **Weather**, after the thermometers and before the fans.

## The card

A place's card is its name, the town the service puts it in, and when the forecast was last got; the alerts in effect or coming; the
current stretch of the forecast; and a column for each of the next seven days (the most the service forecasts, and what fits a card):
the day, a sky icon, the high, the low and the chance of rain if it is 10% or more. Today is **Tonight** once the day is over, and has no high.

A forecast that could not be updated stays on the card, marked old once it is three times older than it should be, with what went wrong
(the service is often busy). A place that cannot be found (an unknown zip, or a place outside the United States) says so.

## Alerts

Every five minutes the plugin asks what is in effect at each place, and raises one alert in the alert console for each event (a
"Flood Watch" at "Home"), from the source `weather` with the key `weather/<place>/<event>`. A newer message about the same event updates
the alert in place, and it is cleared when the service no longer lists it. Tests and cancellations are not shown. If the service cannot be
asked the alerts stay as they were: not knowing is not the same as it being over.

The severity in the console is for you to choose, with `PluginConfig.AlertSeverity`. The default (`DefaultAlertSeverity`) keeps **critical**,
which is chimed on every open page, for what means get somewhere safe now: Tornado, Severe Thunderstorm, Flash Flood, Extreme Wind,
Hurricane, Storm Surge, Tsunami, Blizzard, Ice Storm, Dust Storm and Avalanche Warnings, and a few others, and anything the service itself
rates Extreme. The other warnings and the watches are **warnings**, and the advisories and statements are **info**.

## A forecast freeze

The service only issues frost and freeze alerts in the growing season of a place, so a night at 25°F after it is over (when pipes are in the
most danger) gets none. A place can have an alert of the plugin's own, from the forecast:

```go
cougar := conf.AddZip("Cougar", "80026").AlertOnFreeze(weather.FreezeAlert{Note: "sprinkler pipes"})
```

It is raised, from the same source (`weather`), with the key `weather/<place>/freeze-forecast`, when the date at the place is in the season
and the lowest temperature of the forecast for the next 24 hours is at or below the limit, and it is on the card of the place as well. The
zero `FreezeAlert` is a low of **30°F** or less, from **September 1 to November 15**, looked for in the next **24 hours**, as a **warning**:
`Below`, `From`, `To`, `Within`, `Severity` and `Note` change them. A season may go over the new year (`From` November 15, `To` March 1).

The forecast gives one low for a night, so a night that begins within the next 24 hours counts, whole. Once raised, the alert is left alone
until the forecast low is more than 2°F above the limit (so that a forecast that wavers around it does not raise and clear it over and over),
or the season is over, and it is said again only if the low is two degrees or more different, or is another night.

## Settings

| | default |
|---|---|
| `UserAgent` | none: required |
| `Name` (menu) | `Weather` |
| `Days` | 7 |
| `ForecastEvery` | 30 minutes (the service makes a new forecast about every hour, and says when it last changed, so an unchanged one is a short answer) |
| `AlertsEvery` | 5 minutes |
| `AlertSeverity` | `DefaultAlertSeverity` |

A failed request is tried again after 30 seconds, then 1 minute, 2, 4, and so on up to 10 minutes (or the interval, if that is shorter).

## Old browsers

The page is used on an iPad that stopped at iOS 9 (Safari 9), so it has no script, spaces its items with margins and not `gap`, and writes
colours with commas. A test in `internal/http` (`oldbrowsers_test.go`) looks for what that browser does not have.
