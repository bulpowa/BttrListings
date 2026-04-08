package service

import (
	"context"

	"OlxScraper/internal/model"
	"OlxScraper/internal/repository"
)

type ComponentPriceService interface {
	GetAll(ctx context.Context) ([]*model.ComponentPrice, error)
}

type componentPriceService struct {
	repo *repository.Repository
}

func NewComponentPriceService(repo *repository.Repository) ComponentPriceService {
	return &componentPriceService{repo: repo}
}

func (s *componentPriceService) GetAll(ctx context.Context) ([]*model.ComponentPrice, error) {
	return s.repo.ComponentPrice.GetAll(ctx)
}
