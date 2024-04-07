package swagger_gin

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"

	"github.com/Adachi324/swagger_gin/router"
	"github.com/Adachi324/swagger_gin/swagger"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gin-gonic/gin"
)

//go:embed templates/*
var templates embed.FS

type SwaGin struct {
	*gin.Engine
	RouterGroup    *gin.RouterGroup
	Swagger        *swagger.Swagger
	Routers        map[*gin.RouterGroup]map[string]map[string]*router.Router
	subApps        map[string]*SwaGin
	rootPath       string
	ErrorHandler   router.ErrorHandlerFunc
	beforeInitFunc func()
	afterInitFunc  func()
}

func NewWithEngine(swagger *swagger.Swagger, g *gin.Engine) *SwaGin {
	g.Use(gin.Recovery(), gin.Logger())
	f := &SwaGin{
		Engine:      g,
		RouterGroup: g.Group(""),
		Swagger:     swagger,
		Routers:     make(map[*gin.RouterGroup]map[string]map[string]*router.Router),
		subApps:     make(map[string]*SwaGin),
	}

	f.SetHTMLTemplate(template.Must(template.ParseFS(templates, "templates/*.html")))
	if swagger != nil {
		swagger.Routers = f.Routers
	}
	return f
}

func New(swagger *swagger.Swagger) *SwaGin {
	engine := gin.New()
	engine.Use(gin.Recovery(), gin.Logger())
	f := &SwaGin{
		Engine:      engine,
		RouterGroup: engine.Group(""),
		Swagger:     swagger,
		Routers:     make(map[*gin.RouterGroup]map[string]map[string]*router.Router),
		subApps:     make(map[string]*SwaGin),
	}

	f.SetHTMLTemplate(template.Must(template.ParseFS(templates, "templates/*.html")))
	if swagger != nil {
		swagger.Routers = f.Routers
	}
	return f
}

func (g *SwaGin) WithErrorHandler(handler router.ErrorHandlerFunc) *SwaGin {
	g.ErrorHandler = handler
	return g
}

func (g *SwaGin) Mount(path string, app *SwaGin) {
	app.rootPath = path
	app.Engine = g.Engine
	if app.ErrorHandler == nil {
		app.ErrorHandler = g.ErrorHandler
	}
	app.Swagger.Servers = append(app.Swagger.Servers, &openapi3.Server{
		URL: path,
	})
	g.subApps[path] = app
}

func (g *SwaGin) Use(middleware ...gin.HandlerFunc) gin.IRoutes {
	return g.RouterGroup.Use(middleware...)
}

func (g *SwaGin) Group(path string, options ...Option) *Group {
	group := &Group{
		SwaGin:      g,
		RouterGroup: g.RouterGroup.Group(path),
		Path:        path,
	}

	for _, option := range options {
		option(group)
	}
	return group
}

func (g *SwaGin) Handle(group *gin.RouterGroup, path string, method string, r *router.Router) {

	r.Method = method
	r.Path = path

	if g.Routers[group] == nil {
		g.Routers[group] = make(map[string]map[string]*router.Router)
	}

	if g.Routers[group][path] == nil {
		g.Routers[group][path] = make(map[string]*router.Router)
	}

	g.Routers[group][path][method] = r
}

func (g *SwaGin) handle(path string, method string, r *router.Router) {
	g.Handle(g.RouterGroup, path, method, r)
}

func (g *SwaGin) GET(path string, router *router.Router) {
	g.handle(path, http.MethodGet, router)
}

func (g *SwaGin) POST(path string, router *router.Router) {
	g.handle(path, http.MethodPost, router)
}

func (g *SwaGin) HEAD(path string, router *router.Router) {
	g.handle(path, http.MethodHead, router)
}

func (g *SwaGin) PATCH(path string, router *router.Router) {
	g.handle(path, http.MethodPatch, router)
}

func (g *SwaGin) DELETE(path string, router *router.Router) {
	g.handle(path, http.MethodDelete, router)
}

func (g *SwaGin) PUT(path string, router *router.Router) {
	g.handle(path, http.MethodPut, router)
}

func (g *SwaGin) OPTIONS(path string, router *router.Router) {
	g.handle(path, http.MethodOptions, router)
}

func (g *SwaGin) init() {
	g.initRouters()
	if g.Swagger == nil {
		return
	}
	gin.DisableBindValidation()
	g.Engine.GET(g.fullPath(g.Swagger.OpenAPIUrl), func(c *gin.Context) {
		if strings.HasSuffix(g.Swagger.OpenAPIUrl, ".yml") ||
			strings.HasSuffix(g.Swagger.OpenAPIUrl, ".yaml") {
			y, err := g.Swagger.MarshalYAML()
			if err != nil {
				c.JSON(http.StatusInternalServerError, map[string]string{"status": err.Error()})
			}
			c.String(http.StatusOK, string(y))
		} else {
			c.JSON(http.StatusOK, g.Swagger)
		}
	})
	g.Engine.GET(g.fullPath(g.Swagger.DocsUrl), func(c *gin.Context) {
		options := `{}`
		if g.Swagger.SwaggerOptions != nil {
			data, err := json.Marshal(g.Swagger.SwaggerOptions)
			if err != nil {
				panic(err)
			}
			options = string(data)
		}
		c.HTML(http.StatusOK, "swagger.html", gin.H{
			"openapi_url":     g.fullPath(g.Swagger.OpenAPIUrl),
			"title":           g.Swagger.Title,
			"swagger_options": options,
		})
	})
	g.Engine.GET(g.fullPath(g.Swagger.RedocUrl), func(c *gin.Context) {
		options := `{}`
		if g.Swagger.RedocOptions != nil {
			data, err := json.Marshal(g.Swagger.RedocOptions)
			if err != nil {
				panic(err)
			}
			options = string(data)
		}
		c.HTML(http.StatusOK, "redoc.html", gin.H{
			"openapi_url":   g.fullPath(g.Swagger.OpenAPIUrl),
			"title":         g.Swagger.Title,
			"redoc_options": options,
		})
	})
	g.Swagger.BuildOpenAPI()
}

func (g *SwaGin) initRouters() {
	for group, routers := range g.Routers {
		for path, m := range routers {
			path = g.fullPath(path)
			for method, r := range m {
				handlers := r.GetHandlers()
				if method == http.MethodGet {
					group.GET(path, handlers...)
				} else if method == http.MethodPost {
					group.POST(path, handlers...)
				} else if method == http.MethodHead {
					group.HEAD(path, handlers...)
				} else if method == http.MethodPatch {
					group.PATCH(path, handlers...)
				} else if method == http.MethodDelete {
					group.DELETE(path, handlers...)
				} else if method == http.MethodPut {
					group.PUT(path, handlers...)
				} else if method == http.MethodOptions {
					group.OPTIONS(path, handlers...)
				} else {
					group.Any(path, handlers...)
				}
			}
		}
	}
}

func (g *SwaGin) Init() {
	g.init()
	for _, s := range g.subApps {
		s.init()
	}
}

func (g *SwaGin) fullPath(path string) string {
	return g.rootPath + path
}

func (g *SwaGin) BeforeInit(f func()) {
	g.beforeInitFunc = f
}

func (g *SwaGin) AfterInit(f func()) {
	g.afterInitFunc = f
}

func (g *SwaGin) Run(addr ...string) error {
	if g.beforeInitFunc != nil {
		g.beforeInitFunc()
	}
	g.init()
	if g.afterInitFunc != nil {
		g.afterInitFunc()
	}
	return g.Engine.Run(addr...)
}

func (g *SwaGin) StartGraceful(addr ...string) (*http.Server, error) {
	g.init()
	for _, s := range g.subApps {
		s.init()
	}
	var address string
	if len(addr) == 0 {
		address = ":" + os.Getenv("PORT")
		if address == ":" {
			address = ":8080"
		}
	} else {
		address = addr[0]
	}
	server := &http.Server{
		Addr:    address,
		Handler: g.Engine,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(fmt.Sprintf("ERROR starting server: %v", err))
		}
	}()
	return server, nil
}
