package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
)

type fakeTwoFactor struct {
	mu        sync.Mutex
	enabled   bool
	statusErr error
	verifyErr error
	verifyOK  bool
	valid     map[string]bool
	used      map[string]bool
}

func (f *fakeTwoFactor) Status(context.Context, uuid.UUID) (bool, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statusErr != nil {
		return false, 0, f.statusErr
	}
	return f.enabled, 0, nil
}

func (f *fakeTwoFactor) Verify(_ context.Context, _ uuid.UUID, code string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.verifyErr != nil {
		return false, f.verifyErr
	}
	if f.verifyOK {
		return true, nil
	}
	if f.valid[code] && !f.used[code] {
		f.used[code] = true
		return true, nil
	}
	return false, nil
}

func (f *fakeTwoFactor) setEnabled(v bool) {
	f.mu.Lock()
	f.enabled = v
	f.mu.Unlock()
}

func (f *fakeTwoFactor) setStatusErr(err error) {
	f.mu.Lock()
	f.statusErr = err
	f.mu.Unlock()
}

type memChallenges struct {
	mu     sync.Mutex
	byHash map[string]*LoginChallenge
	byID   map[uuid.UUID]*LoginChallenge
}

func newMemChallenges() *memChallenges {
	return &memChallenges{byHash: map[string]*LoginChallenge{}, byID: map[uuid.UUID]*LoginChallenge{}}
}

func (m *memChallenges) Create(_ context.Context, c *LoginChallenge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *c
	m.byHash[c.TokenHash] = &cp
	m.byID[c.ID] = &cp
	return nil
}

func (m *memChallenges) FindByTokenHash(_ context.Context, hash string) (*LoginChallenge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.byHash[hash]
	if c == nil {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

func (m *memChallenges) RegisterFailure(_ context.Context, id uuid.UUID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.byID[id]
	if c == nil {
		return 0, ErrInvalidLoginChallenge
	}
	c.Attempts++
	if c.Attempts >= c.MaxAttempts {
		c.Consumed = true
	}
	return c.Attempts, nil
}

func (m *memChallenges) Consume(_ context.Context, id uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.byID[id]
	if c == nil || c.Consumed || c.Attempts >= c.MaxAttempts || !time.Now().Before(c.ExpiresAt) {
		return false, nil
	}
	c.Consumed = true
	return true, nil
}

func (m *memChallenges) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byID)
}

func newActiveAuthUser(t *testing.T, email string) (*Service, *authMemory, *fakeTwoFactor, *domain.User) {
	t.Helper()
	ctx := context.Background()
	repos := newAuthMemory()
	hash, err := authjwt.HashPassword("correct-test-password")
	if err != nil {
		t.Fatal(err)
	}
	u := &domain.User{ID: uuid.New(), Email: email, PasswordHash: hash, Status: domain.UserStatusActive}
	if err := (&authUserRepo{repos}).Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	gate := &fakeTwoFactor{enabled: true, valid: map[string]bool{}, used: map[string]bool{}}
	manager := authjwt.NewManager("test-only-secret-with-sufficient-length-123456", time.Minute, time.Hour)
	svc := NewService(&authUserRepo{repos}, &authSessionRepo{repos}, &authWalletRepo{repos}, manager, nil, nil, time.Hour).
		WithTwoFactor(gate).WithLoginChallenges(newMemChallenges())
	return svc, repos, gate, u
}

func TestLogin_With2FA_ReturnsOnlyChallenge(t *testing.T) {
	ctx := context.Background()
	svc, repos, _, u := newActiveAuthUser(t, "2fa-login@example.test")

	res, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !res.TwoFactorRequired || res.TwoFactorToken == "" {
		t.Fatalf("expected a 2FA challenge, got %+v", res)
	}
	if res.AccessToken != "" || res.RefreshToken != "" {
		t.Fatalf("login with 2FA leaked tokens: %+v", res)
	}
	if !res.TwoFactorExpiresAt.After(time.Now()) {
		t.Fatalf("challenge already expired: %v", res.TwoFactorExpiresAt)
	}
	if len(repos.sessions) != 0 {
		t.Fatalf("login with 2FA created %d session(s)", len(repos.sessions))
	}
	store := svc.challenges.(*memChallenges)
	if store.count() != 1 {
		t.Fatalf("expected one challenge, got %d", store.count())
	}
	ch, _ := store.FindByTokenHash(ctx, hashLoginToken(res.TwoFactorToken))
	if ch == nil || ch.UserID != u.ID || ch.Purpose != LoginChallengePurpose {
		t.Fatalf("challenge not persisted correctly: %+v", ch)
	}
	if ch.TokenHash == res.TwoFactorToken {
		t.Fatal("plain challenge token must not be persisted")
	}
}

func TestLogin_AdminWith2FA_RequiresChallenge(t *testing.T) {
	ctx := context.Background()
	svc, repos, _, u := newActiveAuthUser(t, "admin@example.test")

	res, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !res.TwoFactorRequired || res.AccessToken != "" || res.RefreshToken != "" || len(repos.sessions) != 0 {
		t.Fatalf("admin login bypassed 2FA: %+v sessions=%d", res, len(repos.sessions))
	}
}

func TestLogin_2FAStatusUnavailable_FailsClosed(t *testing.T) {
	ctx := context.Background()
	svc, repos, gate, u := newActiveAuthUser(t, "status-fail@example.test")
	gate.setStatusErr(errors.New("twofactor db down"))

	res, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if !errors.Is(err, ErrTwoFactorUnavailable) {
		t.Fatalf("expected fail-closed error, got res=%+v err=%v", res, err)
	}
	if res != nil || len(repos.sessions) != 0 {
		t.Fatalf("fail-closed login leaked a session: %+v", res)
	}
}

func TestComplete2FA_ValidCodeIssuesVerifiedSession(t *testing.T) {
	ctx := context.Background()
	svc, repos, gate, u := newActiveAuthUser(t, "complete@example.test")
	gate.valid["123456"] = true

	pending, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.CompleteTwoFactorLogin(ctx, pending.TwoFactorToken, "123456", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if res.AccessToken == "" || res.RefreshToken == "" {
		t.Fatalf("valid 2FA did not issue tokens: %+v", res)
	}
	session := repos.sessions[authjwt.HashRefreshToken(res.RefreshToken)]
	if session == nil || !session.TwoFactorVerified || session.UserID != u.ID {
		t.Fatalf("session not marked as 2FA-verified: %+v", session)
	}
	if _, err := svc.jwtMgr.ValidateAccessToken(res.AccessToken); err != nil {
		t.Fatalf("issued access token is invalid: %v", err)
	}
}

func TestComplete2FA_WrongCodeDoesNotCreateSessionAndAllowsRetry(t *testing.T) {
	ctx := context.Background()
	svc, repos, gate, u := newActiveAuthUser(t, "retry@example.test")
	gate.valid["654321"] = true

	pending, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteTwoFactorLogin(ctx, pending.TwoFactorToken, "000000", "127.0.0.1", "test"); !errors.Is(err, domain.ErrInvalid2FACode) {
		t.Fatalf("expected invalid code, got %v", err)
	}
	if len(repos.sessions) != 0 {
		t.Fatal("wrong 2FA code created a session")
	}
	if _, err := svc.CompleteTwoFactorLogin(ctx, pending.TwoFactorToken, "654321", "127.0.0.1", "test"); err != nil {
		t.Fatalf("retry with the right code failed: %v", err)
	}
	if len(repos.sessions) != 1 {
		t.Fatalf("expected exactly one session, got %d", len(repos.sessions))
	}
}

func TestComplete2FA_RecoveryCodeAcceptedOnce(t *testing.T) {
	ctx := context.Background()
	svc, repos, gate, u := newActiveAuthUser(t, "recovery@example.test")
	gate.valid["RECOVERY-CODE"] = true

	pending, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteTwoFactorLogin(ctx, pending.TwoFactorToken, "RECOVERY-CODE", "127.0.0.1", "test"); err != nil {
		t.Fatalf("recovery code rejected: %v", err)
	}
	if len(repos.sessions) != 1 {
		t.Fatalf("expected one session, got %d", len(repos.sessions))
	}

	second, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteTwoFactorLogin(ctx, second.TwoFactorToken, "RECOVERY-CODE", "127.0.0.1", "test"); !errors.Is(err, domain.ErrInvalid2FACode) {
		t.Fatalf("recovery code should be single-use, got %v", err)
	}
}

func TestComplete2FA_ExpiredChallenge(t *testing.T) {
	ctx := context.Background()
	svc, repos, gate, u := newActiveAuthUser(t, "expired@example.test")
	gate.valid["123456"] = true

	pending, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	store := svc.challenges.(*memChallenges)
	ch, _ := store.FindByTokenHash(ctx, hashLoginToken(pending.TwoFactorToken))
	if ch == nil {
		t.Fatal("challenge not found")
	}
	store.mu.Lock()
	store.byID[ch.ID].ExpiresAt = time.Now().Add(-time.Second)
	store.mu.Unlock()
	if _, err := svc.CompleteTwoFactorLogin(ctx, pending.TwoFactorToken, "123456", "127.0.0.1", "test"); !errors.Is(err, ErrLoginChallengeExpired) {
		t.Fatalf("expected expired challenge, got %v", err)
	}
	if len(repos.sessions) != 0 {
		t.Fatal("expired challenge created a session")
	}
}

func TestComplete2FA_ChallengeSingleUse(t *testing.T) {
	ctx := context.Background()
	svc, repos, gate, u := newActiveAuthUser(t, "single-use@example.test")
	gate.valid["123456"] = true

	pending, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteTwoFactorLogin(ctx, pending.TwoFactorToken, "123456", "127.0.0.1", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteTwoFactorLogin(ctx, pending.TwoFactorToken, "123456", "127.0.0.1", "test"); !errors.Is(err, ErrInvalidLoginChallenge) {
		t.Fatalf("expected reused challenge to fail, got %v", err)
	}
	if len(repos.sessions) != 1 {
		t.Fatalf("reused challenge minted more sessions: %d", len(repos.sessions))
	}
}

func TestComplete2FA_ConcurrentCompletionMintsOneSession(t *testing.T) {
	ctx := context.Background()
	svc, repos, gate, u := newActiveAuthUser(t, "concurrent@example.test")
	gate.verifyOK = true

	pending, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}

	const workers = 8
	var wg sync.WaitGroup
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.CompleteTwoFactorLogin(ctx, pending.TwoFactorToken, "123456", "127.0.0.1", "test")
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one successful completion, got %d", successes)
	}
	if len(repos.sessions) != 1 {
		t.Fatalf("expected exactly one session, got %d", len(repos.sessions))
	}
}

func TestComplete2FA_LocksAfterMaxWrongAttempts(t *testing.T) {
	ctx := context.Background()
	svc, repos, gate, u := newActiveAuthUser(t, "attempts@example.test")
	gate.valid["123456"] = true

	pending, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxLoginChallengeAttempts; i++ {
		if _, err := svc.CompleteTwoFactorLogin(ctx, pending.TwoFactorToken, "000000", "127.0.0.1", "test"); !errors.Is(err, domain.ErrInvalid2FACode) {
			t.Fatalf("attempt %d: expected invalid code, got %v", i+1, err)
		}
	}
	if _, err := svc.CompleteTwoFactorLogin(ctx, pending.TwoFactorToken, "123456", "127.0.0.1", "test"); !errors.Is(err, ErrInvalidLoginChallenge) {
		t.Fatalf("expected exhausted challenge, got %v", err)
	}
	if len(repos.sessions) != 0 {
		t.Fatal("exhausted challenge created a session")
	}
}

func TestRefresh_RefusesUnverifiedSessionWhen2FAEnabled(t *testing.T) {
	ctx := context.Background()
	svc, repos, gate, u := newActiveAuthUser(t, "refresh-gate@example.test")
	gate.setEnabled(false)

	loggedIn, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	session := repos.sessions[authjwt.HashRefreshToken(loggedIn.RefreshToken)]
	if session == nil || session.TwoFactorVerified {
		t.Fatalf("expected an unverified session, got %+v", session)
	}

	gate.setEnabled(true)
	if _, err := svc.Refresh(ctx, loggedIn.RefreshToken, "127.0.0.1", "test"); !errors.Is(err, domain.Err2FARequired) {
		t.Fatalf("expected 2fa_required, got %v", err)
	}
	if !session.Revoked {
		t.Fatal("unverified refresh family was not revoked")
	}
}

func TestRefresh_2FAStatusUnavailableFailsClosed(t *testing.T) {
	ctx := context.Background()
	svc, _, gate, u := newActiveAuthUser(t, "refresh-status@example.test")
	gate.setEnabled(false)

	loggedIn, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	gate.setStatusErr(errors.New("twofactor down"))
	if _, err := svc.Refresh(ctx, loggedIn.RefreshToken, "127.0.0.1", "test"); !errors.Is(err, ErrTwoFactorUnavailable) {
		t.Fatalf("expected fail-closed refresh, got %v", err)
	}
}

func TestRefresh_AllowsVerifiedSession(t *testing.T) {
	ctx := context.Background()
	svc, _, gate, u := newActiveAuthUser(t, "refresh-ok@example.test")
	gate.valid["123456"] = true

	pending, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	verified, err := svc.CompleteTwoFactorLogin(ctx, pending.TwoFactorToken, "123456", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := svc.Refresh(ctx, verified.RefreshToken, "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("verified session could not refresh: %v", err)
	}
	if rotated.RefreshToken == verified.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
}

func TestLoginChallengeCannotBeUsedAsRefreshToken(t *testing.T) {
	ctx := context.Background()
	svc, repos, _, u := newActiveAuthUser(t, "challenge-refresh@example.test")

	pending, err := svc.Login(ctx, u.Email, "correct-test-password", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refresh(ctx, pending.TwoFactorToken, "127.0.0.1", "test"); err == nil {
		t.Fatal("the 2FA challenge token was accepted as a refresh token")
	}
	if len(repos.sessions) != 0 {
		t.Fatal("challenge token created a session through refresh")
	}
}
