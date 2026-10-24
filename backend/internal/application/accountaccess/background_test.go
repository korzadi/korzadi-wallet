package accountaccess

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
)

// blockingSender se queda esperando hasta que se libera (simula un SMTP lento).
type blockingSender struct {
	release chan struct{}
	sent    atomic.Int32
}

func (b *blockingSender) Send(ctx context.Context, to, subject, body string) error {
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	b.sent.Add(1)
	return nil
}

func slowEnv(t *testing.T) (*env, *blockingSender, *Background) {
	t.Helper()
	e := newEnv(t, nil)
	bs := &blockingSender{release: make(chan struct{})}
	bg := NewBackground(4, 5*time.Second)
	e.svc = NewService(Deps{
		Users: e.users, Tokens: e.tokens, Sessions: e.sessions, Sender: bs,
		BaseURL: "https://wallet.example.com", Now: e.clk.Now,
	}).WithBackground(bg)
	return e, bs, bg
}

// El endpoint responde igual de rápido exista o no la cuenta, aunque el SMTP tarde:
// no se puede enumerar usuarios midiendo el tiempo de respuesta.
func TestForgotPassword_Async_ResponseTimeIndependentOfSMTP(t *testing.T) {
	e, bs, bg := slowEnv(t)
	e.addUser(t, "ana@example.com", domain.UserStatusActive, true)

	timeIt := func(email string) time.Duration {
		start := time.Now()
		e.svc.ForgotPassword(context.Background(), email, "ip")
		return time.Since(start)
	}
	known := timeIt("ana@example.com")
	unknown := timeIt("nadie@example.com")

	if known > 200*time.Millisecond || unknown > 200*time.Millisecond {
		t.Fatalf("la respuesta no debe esperar al SMTP: known=%v unknown=%v", known, unknown)
	}

	close(bs.release) // el SMTP "responde"
	if !bg.Wait(3 * time.Second) {
		t.Fatal("las tareas en segundo plano no terminaron")
	}
	if n := bs.sent.Load(); n != 1 {
		t.Fatalf("correos enviados = %d, esperaba 1 (solo la cuenta real)", n)
	}
}

func TestResendVerification_Async_ResponseTimeIndependentOfSMTP(t *testing.T) {
	e, bs, bg := slowEnv(t)
	e.addUser(t, "ana@example.com", domain.UserStatusPendingVerification, false)

	start := time.Now()
	e.svc.ResendVerification(context.Background(), "ana@example.com", "ip")
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("la respuesta esperó al SMTP: %v", d)
	}
	close(bs.release)
	if !bg.Wait(3 * time.Second) {
		t.Fatal("las tareas no terminaron")
	}
	if bs.sent.Load() != 1 {
		t.Fatalf("correos enviados = %d, esperaba 1", bs.sent.Load())
	}
}

// El registro no espera al SMTP y su resultado nunca afecta al alta.
func TestBackgroundVerifier_ReturnsImmediately_AndSends(t *testing.T) {
	e, bs, bg := slowEnv(t)
	u := e.addUser(t, "ana@example.com", domain.UserStatusPendingVerification, false)

	start := time.Now()
	if err := e.svc.BackgroundVerifier().RequestEmailVerification(context.Background(), u.ID, "ip"); err != nil {
		t.Fatalf("no debe devolver error: %v", err)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("esperó al SMTP: %v", d)
	}
	// Un usuario inexistente tampoco produce error hacia el llamador.
	if err := e.svc.BackgroundVerifier().RequestEmailVerification(context.Background(), uuid.New(), "ip"); err != nil {
		t.Fatalf("usuario inexistente: %v", err)
	}
	close(bs.release)
	if !bg.Wait(3 * time.Second) {
		t.Fatal("las tareas no terminaron")
	}
	if bs.sent.Load() != 1 {
		t.Fatalf("correos enviados = %d, esperaba 1", bs.sent.Load())
	}
}

func TestBackground_DropsWhenFull_AndRecoversFromPanic(t *testing.T) {
	bg := NewBackground(1, 5*time.Second)
	hold := make(chan struct{})

	if !bg.Go("a", func(ctx context.Context) { <-hold }) {
		t.Fatal("la primera tarea debía aceptarse")
	}
	if bg.Go("b", func(ctx context.Context) {}) {
		t.Fatal("con la capacidad agotada la tarea debía descartarse")
	}
	close(hold)
	if !bg.Wait(2 * time.Second) {
		t.Fatal("no terminó")
	}

	// Un pánico no tumba el proceso y libera la capacidad.
	if !bg.Go("panic", func(ctx context.Context) { panic("boom") }) {
		t.Fatal("debía aceptarse")
	}
	if !bg.Wait(2 * time.Second) {
		t.Fatal("no terminó tras el pánico")
	}
	var ran atomic.Bool
	if !bg.Go("after", func(ctx context.Context) { ran.Store(true) }) {
		t.Fatal("la capacidad debía haberse liberado tras el pánico")
	}
	bg.Wait(2 * time.Second)
	if !ran.Load() {
		t.Fatal("la tarea posterior no se ejecutó")
	}
}

func TestBackground_TaskTimeoutCancelsContext(t *testing.T) {
	bg := NewBackground(1, 50*time.Millisecond)
	var cancelled atomic.Bool
	bg.Go("slow", func(ctx context.Context) {
		<-ctx.Done()
		cancelled.Store(true)
	})
	if !bg.Wait(2*time.Second) || !cancelled.Load() {
		t.Fatal("el contexto de la tarea debía cancelarse por el timeout")
	}
}
