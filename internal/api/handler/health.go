package handler

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

type HealthStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Ollama string `json:"ollama"`
}

func (h *Handler) HandleHealth(c echo.Context) error {
	ollamaStatus := "ok"
	if !h.ollama.IsReachable(c.Request().Context()) {
		ollamaStatus = "unreachable"
	}

	return c.JSON(http.StatusOK, &HealthStatus{
		ID:     "system",
		Status: "ok",
		Ollama: ollamaStatus,
	})
}
