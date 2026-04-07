package handler

import (
	"net/http"
	"strconv"

	"OlxScraper/internal/service"

	"github.com/labstack/echo/v4"
)

func (h *Handler) GetListings(c echo.Context) error {
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	offset, _ := strconv.Atoi(c.QueryParam("offset"))

	listings, err := h.service.Listing.GetListings(c.Request().Context(), limit, offset)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, listings)
}

func (h *Handler) GetListingByID(c echo.Context) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
	}

	listing, err := h.service.Listing.GetListingByID(c.Request().Context(), id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if listing == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "listing not found"})
	}
	return c.JSON(http.StatusOK, listing)
}

// ReEnrichListing is an admin endpoint that re-queues a listing for LLM enrichment.
func (h *Handler) ReEnrichListing(c echo.Context) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
	}

	if err := h.service.Listing.ReEnrich(c.Request().Context(), id); err != nil {
		if err == service.ErrListingNotFound {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "listing not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusAccepted, map[string]string{"status": "enqueued"})
}
