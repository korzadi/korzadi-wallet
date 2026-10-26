package auth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
)

type authMemory struct {
	users    map[uuid.UUID]*domain.User
	sessions map[string]*domain.Session
	wallets  map[uuid.UUID]*domain.Wallet
	accounts int
}

type authUserRepo struct{ *authMemory }
type authSessionRepo struct{ *authMemory }
type authWalletRepo struct{ *authMemory }

func newAuthMemory() *authMemory {
	return &authMemory{users: map[uuid.UUID]*domain.User{}, sessions: map[string]*domain.Session{}, wallets: map[uuid.UUID]*domain.Wallet{}}
}
func (m *authMemory) Create(_ context.Context, u *domain.User) error {
	cp := *u
	m.users[u.ID] = &cp
	return nil
}
func (m *authMemory) FindByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	u := m.users[id]
	if u == nil {
		return nil, nil
	}
	cp := *u
	return &cp, nil
}
func (m *authMemory) FindByEmail(_ context.Context, email string) (*domain.User, error) {
	for _, u := range m.users {
		if u.Email == email {
			cp := *u
			return &cp, nil
		}
	}
	return nil, nil
}
func (m *authMemory) Update(_ context.Context, u *domain.User) error {
	cp := *u
	m.users[u.ID] = &cp
	return nil
}
func (m *authMemory) IncrementFailedLogin(_ context.Context, id uuid.UUID) error {
	m.users[id].FailedLoginAttempts++
	return nil
}
func (m *authMemory) ResetFailedLogin(_ context.Context, id uuid.UUID) error {
	m.users[id].FailedLoginAttempts = 0
	m.users[id].LockedUntil = nil
	return nil
}
func (m *authMemory) CreateSession(_ context.Context, s *domain.Session) error {
	cp := *s
	m.sessions[s.RefreshTokenHash] = &cp
	return nil
}
func (m *authSessionRepo) Create(_ context.Context, s *domain.Session) error {
	cp := *s
	m.sessions[s.RefreshTokenHash] = &cp
	return nil
}
func (m *authMemory) FindByRefreshHash(_ context.Context, hash string) (*domain.Session, error) {
	s := m.sessions[hash]
	if s == nil {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}
func (m *authMemory) RevokeFamily(_ context.Context, family uuid.UUID) error {
	for _, s := range m.sessions {
		if s.FamilyID == family {
			s.Revoked = true
		}
	}
	return nil
}
func (m *authMemory) Revoke(_ context.Context, id uuid.UUID) error {
	for _, s := range m.sessions {
		if s.ID == id {
			s.Revoked = true
		}
	}
	return nil
}
func (m *authMemory) CreateWallet(_ context.Context, w *domain.Wallet) error {
	cp := *w
	m.wallets[w.UserID] = &cp
	return nil
}
func (m *authMemory) FindWalletByUserID(_ context.Context, id uuid.UUID) (*domain.Wallet, error) {
	w := m.wallets[id]
	if w == nil {
		return nil, nil
	}
	cp := *w
	return &cp, nil
}
func (m *authMemory) GetOrCreateAccount(_ context.Context, wallet uuid.UUID, asset string) (*domain.WalletAccount, error) {
	m.accounts++
	return &domain.WalletAccount{ID: uuid.New(), WalletID: wallet, AssetCode: asset}, nil
}
func (m *authMemory) ListAccounts(context.Context, uuid.UUID) ([]*domain.WalletAccount, error) {
	return nil, nil
}

var _ domain.UserRepository = (*authUserRepo)(nil)
var _ domain.SessionRepository = (*authSessionRepo)(nil)
var _ domain.WalletRepository = (*authWalletRepo)(nil)

func TestRegisterLoginRefreshAndLogout(t *testing.T) {
	ctx := context.Background()
	repos := newAuthMemory()
	manager := authjwt.NewManager("test-only-secret-with-sufficient-length", time.Minute, time.Hour)
	svc := NewService(&authUserRepo{repos}, &authSessionRepo{repos}, &authWalletRepo{repos}, manager, nil, nil, time.Hour).
		WithTwoFactor(&fakeTwoFactor{}).WithLoginChallenges(newMemChallenges())
	registered, err := svc.Register(ctx, RegisterInput{Email: "unit@example.test", Password: "a-strong-test-password", FirstName: "Unit", LastName: "Test"}, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if registered.User.Status != domain.UserStatusPendingVerification || repos.wallets[registered.User.ID] == nil || repos.accounts != 1 {
		t.Fatalf("registration did not provision pending user/wallet/account")
	}
	claims, err := manager.ValidateAccessToken(registered.AccessToken)
	if err != nil || claims.UserID != registered.User.ID.String() {
		t.Fatalf("invalid access token claims=%+v err=%v", claims, err)
	}

	loggedIn, err := svc.Login(ctx, registered.User.Email, "a-strong-test-password", "127.0.0.1", "test")
	if err != nil || loggedIn.RefreshToken == "" {
		t.Fatalf("login failed: result=%v err=%v", loggedIn, err)
	}
	rotated, err := svc.Refresh(ctx, loggedIn.RefreshToken, "127.0.0.1", "test")
	if err != nil || rotated.RefreshToken == loggedIn.RefreshToken {
		t.Fatalf("refresh did not rotate token: err=%v", err)
	}
	if err := svc.Logout(ctx, rotated.RefreshToken); err != nil {
		t.Fatal(err)
	}
	session, err := repos.FindByRefreshHash(ctx, authjwt.HashRefreshToken(rotated.RefreshToken))
	if err != nil || session == nil || !session.Revoked {
		t.Fatalf("logout did not revoke token family: session=%+v err=%v", session, err)
	}
}

func TestLoginFailureDoesNotIssueSession(t *testing.T) {
	ctx := context.Background()
	repos := newAuthMemory()
	hash, err := authjwt.HashPassword("correct-test-password")
	if err != nil {
		t.Fatal(err)
	}
	u := &domain.User{ID: uuid.New(), Email: "login@example.test", PasswordHash: hash, Status: domain.UserStatusActive}
	if err := (&authUserRepo{repos}).Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	svc := NewService(&authUserRepo{repos}, &authSessionRepo{repos}, &authWalletRepo{repos}, authjwt.NewManager("test-only-secret", time.Minute, time.Hour), nil, nil, time.Hour).
		WithTwoFactor(&fakeTwoFactor{}).WithLoginChallenges(newMemChallenges())
	if _, err := svc.Login(ctx, u.Email, "wrong-password", "", ""); err != domain.ErrInvalidCredentials {
		t.Fatalf("wrong password err=%v", err)
	}
	if len(repos.sessions) != 0 || repos.users[u.ID].FailedLoginAttempts != 1 {
		t.Fatalf("failed login created session or failed to count attempt")
	}
}
