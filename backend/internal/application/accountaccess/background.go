package accountaccess

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Background ejecuta trabajo en segundo plano con un tope de concurrencia.
//
// Se usa para el envío de correos: así las respuestas de los endpoints
// públicos (reenvío, "olvidé mi contraseña") tardan lo mismo exista o no la
// cuenta. Sin esto, el tiempo de respuesta (milisegundos si la cuenta no
// existe vs. ~1 s si se envía un correo) permitiría enumerar usuarios.
type Background struct {
	sem     chan struct{}
	timeout time.Duration
	wg      sync.WaitGroup
}

// NewBackground crea un ejecutor con hasta maxConcurrent tareas simultáneas y
// un límite de tiempo por tarea. Si la cola está llena, la tarea se descarta
// (y se registra): bajo inundación es preferible no enviar a acumular goroutines.
func NewBackground(maxConcurrent int, timeout time.Duration) *Background {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	return &Background{sem: make(chan struct{}, maxConcurrent), timeout: timeout}
}

// Go lanza fn en una goroutine. Devuelve false si se descartó por falta de capacidad.
func (b *Background) Go(name string, fn func(ctx context.Context)) bool {
	select {
	case b.sem <- struct{}{}:
	default:
		log.Printf("accountaccess: tarea %q descartada (capacidad en segundo plano agotada)", name)
		return false
	}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer func() { <-b.sem }()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("accountaccess: pánico en tarea %q recuperado: %v", name, r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		defer cancel()
		fn(ctx)
	}()
	return true
}

// Wait espera a que terminen las tareas en curso (útil al apagar y en tests).
// Devuelve false si venció el plazo.
func (b *Background) Wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() { b.wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// WithBackground activa el envío en segundo plano para los endpoints públicos
// y para BackgroundVerifier. Sin esto el servicio es síncrono (tests).
func (s *Service) WithBackground(b *Background) *Service {
	s.bg = b
	return s
}

// dispatch ejecuta fn en segundo plano si hay Background; si no, en línea.
func (s *Service) dispatch(ctx context.Context, name string, fn func(ctx context.Context)) {
	if s.bg == nil {
		fn(ctx)
		return
	}
	s.bg.Go(name, fn)
}

// ResendVerification es el endpoint público: responde siempre sin error de
// negocio y SIN depender del tiempo de envío (no revela si el correo existe,
// ya está verificado o está bloqueado).
func (s *Service) ResendVerification(ctx context.Context, email, ip string) {
	s.dispatch(ctx, "resend-verification", func(c context.Context) { s.resendVerificationNow(c, email, ip) })
}

// ForgotPassword es público y responde siempre igual, sin depender del
// tiempo de envío. Cuentas inexistentes, bloqueadas o borradas no reciben correo.
func (s *Service) ForgotPassword(ctx context.Context, email, ip string) {
	s.dispatch(ctx, "forgot-password", func(c context.Context) { s.forgotPasswordNow(c, email, ip) })
}

// BackgroundVerifier es el adaptador que se pasa a auth.Service.WithEmailVerifier:
// el registro no espera al servidor SMTP y su fallo nunca lo afecta.
type BackgroundVerifier struct{ s *Service }

func (s *Service) BackgroundVerifier() BackgroundVerifier { return BackgroundVerifier{s: s} }

func (v BackgroundVerifier) RequestEmailVerification(ctx context.Context, userID uuid.UUID, ip string) error {
	v.s.dispatch(ctx, "register-verification", func(c context.Context) {
		if err := v.s.RequestEmailVerification(c, userID, ip); err != nil {
			log.Printf("accountaccess: verificación al registrarse falló user=%s: %v", userID, err)
		}
	})
	return nil
}
