package repository

import (
	"OlxScraper/internal/db"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	queries *db.Queries
	pool    *pgxpool.Pool
	User    UserRepository
	Listing ListingRepository
}

func New(queries *db.Queries, pool *pgxpool.Pool) *Repository {
	return &Repository{
		queries: queries,
		pool:    pool,
		User:    NewUserRepository(queries),
		Listing: NewListingRepository(pool),
	}
}
