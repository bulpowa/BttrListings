package repository

import (
	"OlxScraper/internal/db"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	User           UserRepository
	Listing        ListingRepository
	ComponentPrice ComponentPriceRepository
}

func New(pool *pgxpool.Pool) *Repository {
	q := db.New(pool)
	return &Repository{
		User:           NewUserRepository(q),
		Listing:        NewListingRepository(q),
		ComponentPrice: NewComponentPriceRepository(q),
	}
}
