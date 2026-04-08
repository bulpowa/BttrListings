package service

import (
	"errors"

	"OlxScraper/internal/auth"
	"OlxScraper/internal/repository"
)

var ErrListingNotFound = errors.New("listing not found")

type Service struct {
	repo           *repository.Repository
	User           UserService
	Admin          AdminService
	Auth           AuthService
	Listing        ListingService
	ComponentPrice ComponentPriceService
}

func New(repo *repository.Repository, jwtService auth.JWTService, enqueue EnqueueFn) *Service {
	return &Service{
		repo:           repo,
		User:           NewUserService(repo),
		Admin:          NewAdminService(repo),
		Auth:           NewAuthService(repo, jwtService),
		Listing:        NewListingService(repo, enqueue),
		ComponentPrice: NewComponentPriceService(repo),
	}
}
