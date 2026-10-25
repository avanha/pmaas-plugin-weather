// Package http is the weather plugin's page: the route, and the renderer for a place's card.
package http

import (
	"embed"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"text/template"
	"time"

	"github.com/avanha/pmaas-plugin-weather/data"
	spi "github.com/avanha/pmaas-spi"
	"github.com/avanha/pmaas-spi/logging"
)

//go:embed content/static content/templates
var contentFS embed.FS

// The templates are rendered by PMAAS's template engine plugin, which uses text/template: it does no HTML escaping, so every string a
// template prints has to go through the html function itself.
var locationTemplate = spi.TemplateInfo{
	Name:    "weather_location",
	FuncMap: template.FuncMap{"Clock": clock},
	Paths:   []string{"templates/weather_location.htmlt"},
	Styles:  []string{"css/weather.css"},
}

func clock(t time.Time) string {
	return t.Format("3:04 PM")
}

// MenuPriority is where the plugin is in the menu: with the unpinned entries, in alphabetical order.
const MenuPriority = 0

// Views are the cards of the places.
type Views interface {
	Views() ([]*data.LocationView, error)
}

type Handler struct {
	container spi.IPMAASContainer
	views     Views
}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) Init(container spi.IPMAASContainer, views Views, menuLabel string) {
	h.container = container
	h.views = views

	container.ProvideContentFS(&contentFS, "content")
	container.EnableStaticContent("static")
	container.AddRouteWithOptions("", h.handleList, &spi.HttpHandlerOptions{MenuLabel: menuLabel, MenuPriority: MenuPriority})
	container.RegisterEntityRenderer(reflect.TypeFor[data.LocationView](), h.rendererFactory)
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	log := logging.For(h.container, "http")

	views, err := h.views.Views()
	if err != nil {
		log.Error("unable to get the places", "error", err)
	}

	items := make([]any, len(views))
	for i, view := range views {
		items[i] = view
	}

	h.container.RenderList(w, r, spi.RenderListOptions{Title: "Weather", Masonry: true}, items)
}

func (h *Handler) rendererFactory() (spi.EntityRenderer, error) {
	compiled, err := h.container.GetTemplate(&locationTemplate)
	if err != nil {
		return spi.EntityRenderer{}, fmt.Errorf("unable to load the weather_location template: %w", err)
	}

	render := func(w io.Writer, entity any) error {
		view, ok := entity.(*data.LocationView)
		if !ok {
			return fmt.Errorf("item is not a *LocationView: %T", entity)
		}

		if err := compiled.Instance.Execute(w, view); err != nil {
			return fmt.Errorf("unable to execute the weather_location template: %w", err)
		}

		return nil
	}

	return spi.EntityRenderer{StreamingRenderFunc: render, Styles: compiled.Styles, Scripts: compiled.Scripts}, nil
}
