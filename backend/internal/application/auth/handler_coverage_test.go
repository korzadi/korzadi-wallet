package auth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
)

func TestService_Register_CreatesWalletAndAccount(t *testing.T) {
	ctx := context.Background()
	repos := newAuthMemory()
	manager := authjwt.NewManager("test-secret-with-sufficient-length-123456", time.Minute, time.Hour)
	svc := NewService(&authUserRepo{repos}, &authSessionRepo{repos}, &authWalletRepo{repos}, manager, nil, nil, time.Hour)
	result, err := svc.Register(ctx, RegisterInput{
		Email: "new@example.com", Password: "strongpassword123", FirstName: "New", LastName: "User",
	}, "127.0.0.1", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if result.User.Email != "new@example.com" {
		t.Fatalf("bad email")
	}
	if result.AccessToken != "" || result.RefreshToken != "" {
		t.Fatal("register must not issue a session before email verification")
	}
	if result.User.Status != domain.UserStatusPendingVerification {
		t.Fatalf("status = %s, want pending verification", result.User.Status)
	}
	if repos.accounts == 0 {
		t.Fatal("account not created")
	}
}

func TestService_Refresh_RotatesToken(t *testing.T) {
	ctx := context.Background()
	repos := newAuthMemory()
	manager := authjwt.NewManager("test-secret-with-sufficient-length-123456", time.Minute, time.Hour)
	svc := NewService(&authUserRepo{repos}, &authSessionRepo{repos}, &authWalletRepo{repos}, manager, nil, nil, time.Hour).
		WithTwoFactor(&fakeTwoFactor{}).WithLoginChallenges(newMemChallenges())
	res, _ := svc.Register(ctx, RegisterInput{Email: "r@example.com", Password: "strongpassword123", FirstName: "R", LastName: "U"}, "1.1.1.1", "a")
	repos.users[res.User.ID].Status = domain.UserStatusActive
	logged, err := svc.Login(ctx, "r@example.com", "strongpassword123", "1.1.1.1", "a")
	if err != nil {
		t.Fatal(err)
	}
	old := logged.RefreshToken
	rotated, err := svc.Refresh(ctx, old, "1.1.1.1", "a")
	if err != nil {
		t.Fatal(err)
	}
	if rotated.RefreshToken == old {
		t.Fatal("token not rotated")
	}
}

func TestService_Logout_RevokesSession(t *testing.T) {
	ctx := context.Background()
	repos := newAuthMemory()
	manager := authjwt.NewManager("test-secret-with-sufficient-length-123456", time.Minute, time.Hour)
	svc := NewService(&authUserRepo{repos}, &authSessionRepo{repos}, &authWalletRepo{repos}, manager, nil, nil, time.Hour).
		WithTwoFactor(&fakeTwoFactor{}).WithLoginChallenges(newMemChallenges())
	res, _ := svc.Register(ctx, RegisterInput{Email: "l@example.com", Password: "strongpassword123", FirstName: "L", LastName: "U"}, "1.1.1.1", "a")
	repos.users[res.User.ID].Status = domain.UserStatusActive
	logged, err := svc.Login(ctx, "l@example.com", "strongpassword123", "1.1.1.1", "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Logout(ctx, logged.RefreshToken); err != nil {
		t.Fatal(err)
	}
}

func TestService_Login_ActiveUser(t *testing.T) {
	ctx := context.Background()
	repos := newAuthMemory()
	hash, _ := authjwt.HashPassword("correctpass123")
	u := &domain.User{ID: uuid.New(), Email: "active@example.com", PasswordHash: hash, Status: domain.UserStatusActive}
	(&authUserRepo{repos}).Create(ctx, u)
	svc := NewService(&authUserRepo{repos}, &authSessionRepo{repos}, &authWalletRepo{repos}, authjwt.NewManager("test-secret-with-sufficient-length-123456", time.Minute, time.Hour), nil, nil, time.Hour).
		WithTwoFactor(&fakeTwoFactor{}).WithLoginChallenges(newMemChallenges())
	res, err := svc.Login(ctx, u.Email, "correctpass123", "1.1.1.1", "a")
	if err != nil {
		t.Fatal(err)
	}
	if res.AccessToken == "" {
		t.Fatal("no token")
	}
}
