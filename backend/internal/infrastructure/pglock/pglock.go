// Package pglock implementa locking.Locker con advisory locks de PostgreSQL
// a nivel de SESIÓN (pg_try_advisory_lock / pg_advisory_unlock).
//
// Reglas que este paquete garantiza:
//   - Un lock retenido ocupa UNA conexión dedicada del pool hasta liberarse
//     (los advisory locks de sesión pertenecen a la conexión). Por eso se
//     recomienda un pool propio y pequeño (ver New) para que los locks nunca
//     dejen sin conexiones al pool principal.
//   - Si la liberación falla o el contexto se cancela a mitad de una consulta,
//     la conexión se DESTRUYE (Hijack+Close) en vez de devolverse al pool:
//     cerrar la sesión libera el lock en el servidor. Nunca vuelve al pool una
//     conexión que pueda seguir reteniendo un lock.
//   - Si la conexión se pierde mientras se retiene, PostgreSQL libera el lock
//     (fail-safe), pero el titular no se entera: las secciones críticas deben
//     ser cortas y protegidas además por compare-and-set en la base de datos
//     (como ya hacen retiros/sweeps/treasury).
//   - No es compatible con PgBouncer en modo transaction (rompe los locks de
//     sesión): usar conexión directa o modo session.
package pglock

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	keyPrefix      = "korzadi:advisory:"
	defaultRetry   = 50 * time.Millisecond
	releaseTimeout = 5 * time.Second
)

// Locker adquiere advisory locks sobre un pool de pgx.
type Locker struct {
	pool  *pgxpool.Pool
	retry time.Duration
}

// New crea el Locker. pool debería ser un pool dedicado (ver NewPool).
func New(pool *pgxpool.Pool) *Locker { return &Locker{pool: pool, retry: defaultRetry} }

// NewPool abre un pool exclusivo para locks con como máximo maxConns
// conexiones (mín. 2), a partir de la misma URL de la base de datos.
func NewPool(ctx context.Context, dbURL string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("pglock: URL de base de datos inválida: %w", err)
	}
	if maxConns < 2 {
		maxConns = 2
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pglock: abrir pool: %w", err)
	}
	return pool, nil
}

// KeyID convierte una clave de texto en el bigint del advisory lock
// (SHA-256 truncado; la probabilidad de colisión entre claves es despreciable).
func KeyID(key string) int64 {
	sum := sha256.Sum256([]byte(keyPrefix + key))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

// TryLock intenta tomar la clave sin esperar.
func (l *Locker) TryLock(ctx context.Context, key string) (func(), bool, error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("pglock: obtener conexión: %w", err)
	}
	id := KeyID(key)
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, id).Scan(&ok); err != nil {
		// Estado incierto (¿se llegó a tomar?): destruir la sesión.
		discard(conn)
		return nil, false, fmt.Errorf("pglock: pg_try_advisory_lock: %w", err)
	}
	if !ok {
		conn.Release()
		return nil, false, nil
	}
	var once sync.Once
	unlock := func() {
		once.Do(func() {
			rctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
			defer cancel()
			var released bool
			if err := conn.QueryRow(rctx, `SELECT pg_advisory_unlock($1)`, id).Scan(&released); err != nil || !released {
				discard(conn) // cerrar la sesión libera el lock en el servidor
				return
			}
			conn.Release()
		})
	}
	return unlock, true, nil
}

// Lock espera (sondeando con jitter) hasta obtener la clave o hasta que ctx
// termine. No mantiene ninguna conexión mientras espera.
func (l *Locker) Lock(ctx context.Context, key string) (func(), error) {
	for {
		unlock, ok, err := l.TryLock(ctx, key)
		if err != nil {
			return nil, err
		}
		if ok {
			return unlock, nil
		}
		wait := l.retry + time.Duration(rand.Int63n(int64(l.retry)))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func discard(conn *pgxpool.Conn) {
	raw := conn.Hijack()
	cctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	_ = raw.Close(cctx)
}
