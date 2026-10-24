package security

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// cdRepo implementa DestinationRepository + DestinationCooldownRepository.
type cdRepo struct {
	mu            sync.Mutex
	status        map[string]DestinationStatus
	firstSeen     map[string]time.Time
	registerErr   error
	registerCalls int
}

func newCDRepo() *cdRepo {
	return &cdRepo{status: map[string]DestinationStatus{}, firstSeen: map[string]time.Time{}}
}

func cdKey(u uuid.UUID, asset, network, hash string) string {
	return fmt.Sprintf("%s|%s|%s|%s", u, asset, network, hash)
}

func (r *cdRepo) Lookup(ctx context.Context, u uuid.UUID, asset, network, hash string) (DestinationStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.status[cdKey(u, asset, network, hash)]; ok {
		return s, nil
	}
	return DestinationNew, nil
}
func (r *cdRepo) RecordUse(ctx context.Context, u uuid.UUID, asset, network, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status[cdKey(u, asset, network, hash)] = DestinationKnown
	return nil
}
func (r *cdRepo) SetStatus(ctx context.Context, u uuid.UUID, asset, network, hash string, s DestinationStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status[cdKey(u, asset, network, hash)] = s
	return nil
}
func (r *cdRepo) RegisterPending(ctx context.Context, u uuid.UUID, asset, network, hash string) (time.Time, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registerCalls++
	if r.registerErr != nil {
		return time.Time{}, false, r.registerErr
	}
	k := cdKey(u, asset, network, hash)
	if t, ok := r.firstSeen[k]; ok {
		return t, false, nil
	}
	now := time.Now()
	r.firstSeen[k] = now
	return now, true, nil
}

func TestCheckWithCooldown_NewDestination_RegistersAndWaits(t *testing.T) {
	repo := newCDRepo()
	g := NewDestinationGuard(repo, nil)
	u := uuid.New()
	ctx := context.Background()

	_, err := g.CheckWithCooldown(ctx, u, "USDT", "BSC", "0xAbC", "ip", time.Hour)
	var cd *DestinationCooldownError
	if !errors.As(err, &cd) || !errors.Is(err, ErrDestinationCooldown) {
		t.Fatalf("esperaba DestinationCooldownError, obtuve %v", err)
	}
	if !cd.FirstRequest {
		t.Fatal("el primer intento debe marcar FirstRequest=true (para avisar al titular)")
	}
	if d := time.Until(cd.AvailableAt); d < 59*time.Minute || d > time.Hour+time.Second {
		t.Fatalf("AvailableAt debería ser ~1h en el futuro, es %v", d)
	}

	// Segundo intento: misma espera, ya NO es el primer intento (no se vuelve a avisar).
	_, err = g.CheckWithCooldown(ctx, u, "USDT", "BSC", "0xabc", "ip", time.Hour) // otra capitalización
	if !errors.As(err, &cd) || cd.FirstRequest {
		t.Fatalf("segundo intento: %v (FirstRequest debe ser false)", err)
	}
}

func TestCheckWithCooldown_AfterCooldown_Allows(t *testing.T) {
	repo := newCDRepo()
	g := NewDestinationGuard(repo, nil)
	u := uuid.New()
	repo.firstSeen[cdKey(u, "USDT", "BSC", HashDestination("0xabc"))] = time.Now().Add(-2 * time.Hour)

	status, err := g.CheckWithCooldown(context.Background(), u, "USDT", "BSC", "0xabc", "ip", time.Hour)
	if err != nil || status != DestinationNew {
		t.Fatalf("tras la espera debía permitirse: status=%s err=%v", status, err)
	}
}

func TestCheckWithCooldown_KnownAllowlistedAndBlocked(t *testing.T) {
	repo := newCDRepo()
	g := NewDestinationGuard(repo, nil)
	u := uuid.New()
	ctx := context.Background()
	h := HashDestination("0xabc")

	repo.status[cdKey(u, "USDT", "BSC", h)] = DestinationKnown
	if _, err := g.CheckWithCooldown(ctx, u, "USDT", "BSC", "0xabc", "ip", time.Hour); err != nil {
		t.Fatalf("known no debe esperar: %v", err)
	}
	repo.status[cdKey(u, "USDT", "BSC", h)] = DestinationAllowlisted
	if _, err := g.CheckWithCooldown(ctx, u, "USDT", "BSC", "0xabc", "ip", time.Hour); err != nil {
		t.Fatalf("allowlisted no debe esperar: %v", err)
	}
	repo.status[cdKey(u, "USDT", "BSC", h)] = DestinationBlocked
	if _, err := g.CheckWithCooldown(ctx, u, "USDT", "BSC", "0xabc", "ip", time.Hour); !errors.Is(err, ErrDestinationBlocked) {
		t.Fatalf("blocked debe rechazarse: %v", err)
	}
	if repo.registerCalls != 0 {
		t.Fatal("known/allowlisted/blocked no deben registrarse como pendientes")
	}
}

func TestCheckWithCooldown_DisabledOrUnsupported_NoWait(t *testing.T) {
	u := uuid.New()
	repo := newCDRepo()
	// cooldown 0 => desactivado y sin registro.
	if _, err := NewDestinationGuard(repo, nil).CheckWithCooldown(context.Background(), u, "USDT", "BSC", "0xabc", "ip", 0); err != nil {
		t.Fatalf("cooldown 0: %v", err)
	}
	if repo.registerCalls != 0 {
		t.Fatal("con cooldown 0 no se registra nada")
	}
	// Repositorio que no soporta espera: comportamiento anterior (sin espera).
	type onlyBase struct{ DestinationRepository }
	if _, err := NewDestinationGuard(onlyBase{newCDRepo()}, nil).CheckWithCooldown(context.Background(), u, "USDT", "BSC", "0xabc", "ip", time.Hour); err != nil {
		t.Fatalf("repo sin soporte de espera: %v", err)
	}
}

func TestCheckWithCooldown_RepoError_FailsClosed(t *testing.T) {
	repo := newCDRepo()
	repo.registerErr = errors.New("db caída")
	_, err := NewDestinationGuard(repo, nil).CheckWithCooldown(context.Background(), uuid.New(), "USDT", "BSC", "0xabc", "ip", time.Hour)
	var cd *DestinationCooldownError
	if err == nil || errors.As(err, &cd) {
		t.Fatalf("un fallo del repositorio debe devolver error (no cooldown): %v", err)
	}
}

func TestDestinationSetStatus_ValidatesAndAllowlistBypassesCooldown(t *testing.T) {
	repo := newCDRepo()
	g := NewDestinationGuard(repo, nil)
	u := uuid.New()
	ctx := context.Background()

	if err := g.SetStatus(ctx, u, "USDT", "BSC", "0xabc", DestinationNew); err == nil {
		t.Fatal("'new' no es un estado asignable")
	}
	if err := g.SetStatus(ctx, u, "USDT", "BSC", "0xabc", DestinationStatus("loquesea")); err == nil {
		t.Fatal("estado desconocido debía rechazarse")
	}
	// En espera => el admin la aprueba => ya no espera.
	if _, err := g.CheckWithCooldown(ctx, u, "USDT", "BSC", "0xabc", "ip", time.Hour); !errors.Is(err, ErrDestinationCooldown) {
		t.Fatalf("primero debía esperar: %v", err)
	}
	if err := g.SetStatus(ctx, u, "USDT", "BSC", "0xabc", DestinationAllowlisted); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if _, err := g.CheckWithCooldown(ctx, u, "USDT", "BSC", "0xabc", "ip", time.Hour); err != nil {
		t.Fatalf("tras aprobarla no debe esperar: %v", err)
	}
}

func TestHTTPStatusFor_Cooldown(t *testing.T) {
	err := fmt.Errorf("retiro: %w", &DestinationCooldownError{AvailableAt: time.Now().Add(time.Hour)})
	status, code, ok := HTTPStatusFor(err)
	if !ok || status != 403 || code != "new_destination_cooldown" {
		t.Fatalf("HTTPStatusFor = %d %q %v", status, code, ok)
	}
}
