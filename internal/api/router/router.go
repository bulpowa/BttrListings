package router

import (
	"strings"

	"OlxScraper/internal/api/handler"
	"OlxScraper/internal/auth"
	"OlxScraper/internal/llm"
	"OlxScraper/internal/middleware"
	"OlxScraper/internal/service"
	"OlxScraper/internal/validation"
	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"
	"riverqueue.com/riverui"
)

func New(svc *service.Service, jwtService auth.JWTService, ollamaClient *llm.OllamaClient, riverUI *riverui.Handler) *echo.Echo {
	e := echo.New()

	e.Use(echomw.RemoveTrailingSlashWithConfig(echomw.TrailingSlashConfig{
		Skipper: func(c echo.Context) bool {
			return strings.HasPrefix(c.Request().URL.Path, "/riverui")
		},
	}))
	e.Use(echomw.Recover())
	e.Use(echomw.CORS())
	e.Use(echomw.Gzip())
	e.Use(middleware.EnhancedLogger())
	e.Validator = validation.NewValidator()

	adminMiddleware := middleware.NewMiddleware(jwtService)

	h := handler.New(svc, ollamaClient)

	e.GET("/health", h.HandleHealth)
	e.POST("/register", h.HandleRegister)
	e.POST("/login", h.HandleLogin)

	e.GET("/listings", h.GetListings)
	e.GET("/listings/:id", h.GetListingByID)

	adminGroup := e.Group("/admin")
	adminGroup.Use(adminMiddleware.AdminGuard)
	adminGroup.POST("/verify", h.VerifyUser)
	adminGroup.GET("/getUnverified", h.GetUnverifiedUsers)
	adminGroup.POST("/listings/:id/re-enrich", h.ReEnrichListing)

	// River UI dashboard at /riverui/
	e.Any("/riverui*", echo.WrapHandler(riverUI))

	return e
}
