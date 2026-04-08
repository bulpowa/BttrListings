package service

import (
	"OlxScraper/internal/model"
	"OlxScraper/internal/repository"
	"context"
)

type AdminService interface {
	VerifyUser(ctx context.Context, req *model.VerifyRequest) (*model.VerifyResponse, error)
	GetUnverifiedUsers(ctx context.Context) ([]model.User, error)
}

type adminService struct {
	repo *repository.Repository
}

func NewAdminService(repo *repository.Repository) AdminService {
	return &adminService{repo: repo}
}

func (s *adminService) VerifyUser(ctx context.Context, req *model.VerifyRequest) (*model.VerifyResponse, error) {
	userId := req.UserID
	isVerified, err := s.repo.User.Verify(ctx, *userId)

	if err != nil {
		return nil, err
	}

	return &model.VerifyResponse{
		UserID:     *req.UserID,
		IsVerified: *isVerified,
	}, nil
}

func (s *adminService) GetUnverifiedUsers(ctx context.Context) ([]model.User, error) {
	dbUsers, err := s.repo.User.GetUnverifiedUsers(ctx)
	if err != nil {
		return nil, err
	}
	users := make([]model.User, len(dbUsers))
	for i, dbUser := range dbUsers {
		isVerified := dbUser.IsVerified != nil && *dbUser.IsVerified
		createdAt := ""
		if dbUser.CreatedAt != nil {
			createdAt = dbUser.CreatedAt.String()
		}
		users[i] = model.User{
			ID:         dbUser.ID,
			Username:   dbUser.Username,
			CreatedAt:  createdAt,
			IsVerified: isVerified,
		}
	}
	return users, nil
}
