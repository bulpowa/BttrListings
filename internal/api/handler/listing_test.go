package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"OlxScraper/internal/api/handler"
	"OlxScraper/internal/auth"
	"OlxScraper/internal/middleware"
	"OlxScraper/internal/model"
	"OlxScraper/internal/service"
	"OlxScraper/internal/validation"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockListingService is a test double for service.ListingService.
type mockListingService struct {
	service.ListingService

	fnGetListings    func(ctx context.Context, limit, offset int) ([]*model.Listing, error)
	fnGetListingByID func(ctx context.Context, id int64) (*model.Listing, error)
	fnReEnrich       func(ctx context.Context, id int64) error
}

func (m *mockListingService) GetListings(ctx context.Context, limit, offset int) ([]*model.Listing, error) {
	return m.fnGetListings(ctx, limit, offset)
}

func (m *mockListingService) GetListingByID(ctx context.Context, id int64) (*model.Listing, error) {
	return m.fnGetListingByID(ctx, id)
}

func (m *mockListingService) ReEnrich(ctx context.Context, id int64) error {
	return m.fnReEnrich(ctx, id)
}

// newTestEcho sets up a minimal Echo instance with the listing handler and admin guard.
func newTestEcho(listingSvc service.ListingService, jwtService auth.JWTService) *echo.Echo {
	e := echo.New()
	e.Validator = validation.NewValidator()

	svc := &service.Service{Listing: listingSvc}
	h := handler.New(svc, nil) // ollamaClient not used by listing handlers

	mw := middleware.NewMiddleware(jwtService)

	e.GET("/listings", h.GetListings)
	e.GET("/listings/:id", h.GetListingByID)

	adminGroup := e.Group("/admin")
	adminGroup.Use(mw.AdminGuard)
	adminGroup.POST("/listings/:id/re-enrich", h.ReEnrichListing)

	return e
}

func TestGetListings_OK(t *testing.T) {
	listings := []*model.Listing{{ID: 1, Title: "GPU"}}
	mock := &mockListingService{
		fnGetListings: func(_ context.Context, limit, offset int) ([]*model.Listing, error) {
			return listings, nil
		},
	}

	e := newTestEcho(mock, auth.NewJWTService("secret"))
	req := httptest.NewRequest(http.MethodGet, "/listings", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var got []model.Listing
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Len(t, got, 1)
	assert.Equal(t, "GPU", got[0].Title)
}

func TestGetListingByID_OK(t *testing.T) {
	listing := &model.Listing{ID: 7, Title: "RTX 3080"}
	mock := &mockListingService{
		fnGetListingByID: func(_ context.Context, id int64) (*model.Listing, error) {
			assert.Equal(t, int64(7), id)
			return listing, nil
		},
	}

	e := newTestEcho(mock, auth.NewJWTService("secret"))
	req := httptest.NewRequest(http.MethodGet, "/listings/7", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetListingByID_InvalidID(t *testing.T) {
	e := newTestEcho(&mockListingService{}, auth.NewJWTService("secret"))
	req := httptest.NewRequest(http.MethodGet, "/listings/notanumber", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetListingByID_NotFound(t *testing.T) {
	mock := &mockListingService{
		fnGetListingByID: func(_ context.Context, id int64) (*model.Listing, error) {
			return nil, nil
		},
	}

	e := newTestEcho(mock, auth.NewJWTService("secret"))
	req := httptest.NewRequest(http.MethodGet, "/listings/999", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// --- Admin guard tests ---

func TestReEnrich_NoToken_Unauthorized(t *testing.T) {
	e := newTestEcho(&mockListingService{}, auth.NewJWTService("secret"))
	req := httptest.NewRequest(http.MethodPost, "/admin/listings/1/re-enrich", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	// No Authorization header → AdminGuard should reject.
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestReEnrich_UserToken_Forbidden(t *testing.T) {
	jwtService := auth.NewJWTService("secret")
	token, err := jwtService.CreateToken("user") // not admin
	require.NoError(t, err)

	e := newTestEcho(&mockListingService{}, jwtService)
	req := httptest.NewRequest(http.MethodPost, "/admin/listings/1/re-enrich", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestReEnrich_AdminToken_OK(t *testing.T) {
	jwtService := auth.NewJWTService("secret")
	token, err := jwtService.CreateToken("admin")
	require.NoError(t, err)

	mock := &mockListingService{
		fnReEnrich: func(_ context.Context, id int64) error {
			assert.Equal(t, int64(1), id)
			return nil
		},
	}

	e := newTestEcho(mock, jwtService)
	req := httptest.NewRequest(http.MethodPost, "/admin/listings/1/re-enrich", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
}

func TestReEnrich_AdminToken_ListingNotFound(t *testing.T) {
	jwtService := auth.NewJWTService("secret")
	token, err := jwtService.CreateToken("admin")
	require.NoError(t, err)

	mock := &mockListingService{
		fnReEnrich: func(_ context.Context, id int64) error {
			return service.ErrListingNotFound
		},
	}

	e := newTestEcho(mock, jwtService)
	req := httptest.NewRequest(http.MethodPost, "/admin/listings/999/re-enrich", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestReEnrich_AdminToken_ServiceError(t *testing.T) {
	jwtService := auth.NewJWTService("secret")
	token, err := jwtService.CreateToken("admin")
	require.NoError(t, err)

	mock := &mockListingService{
		fnReEnrich: func(_ context.Context, id int64) error {
			return errors.New("river unavailable")
		},
	}

	e := newTestEcho(mock, jwtService)
	req := httptest.NewRequest(http.MethodPost, "/admin/listings/1/re-enrich", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
