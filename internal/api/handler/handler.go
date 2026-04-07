package handler

import (
	"OlxScraper/internal/llm"
	"OlxScraper/internal/response"
	"OlxScraper/internal/service"
	"OlxScraper/internal/validation"
	"github.com/labstack/echo/v4"
)

type Handler struct {
	service *service.Service
	ollama  *llm.OllamaClient
}

func New(service *service.Service, ollama *llm.OllamaClient) *Handler {
	return &Handler{service: service, ollama: ollama}
}

func (h *Handler) ValidateRequest(c echo.Context, i interface{}) *response.Response {
	if err := c.Bind(i); err != nil {
		errorResponse := response.Error("Invalid request", nil)
		return &errorResponse
	}

	if err := c.Validate(i); err != nil {
		validationErrors := validation.HandleValidationErrors(err)
		errorResponse := response.Error("Invalid request", validationErrors)
		return &errorResponse
	}

	return nil
}
