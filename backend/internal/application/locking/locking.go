// Package locking define el puerto de exclusión mutua ENTRE PROCESOS que usan
// los workers y servicios para poder correr con varias réplicas del backend.
//
// Producción: infrastructure/pglock (pg_advisory_lock de PostgreSQL). Tests y
// simulación de varias instancias en un mismo proceso: Memory, que comparten
// las "instancias" simuladas igual que compartirían la base de datos.
package locking

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// ErrNotAcquired lo devuelven los helpers cuando el lock lo tiene otra instancia.
var ErrNotAcquired = errors.New("locking: lock ocupado por otra instancia")

// Locker es exclusión mutua nombrada por clave (string), válida entre procesos.
type Locker interface {
	// Lock espera hasta obtener la clave o hasta que ctx termine. Devuelve la
	// función que la libera (idempotente).
	Lock(ctx context.Context, key string) (unlock func(), err error)
	// TryLock no espera: acquired=false si otra instancia ya la tiene.
	TryLock(ctx context.Context, key string) (unlock func(), acquired bool, err error)
}

// HotKey es la clave del lock de una wallet HOT: una por (network, address),
// con la dirección en minúsculas para que 0xABC y 0xabc sean la misma.
func HotKey(network, address string) string {
	return "hot:" + strings.ToLower(strings.TrimSpace(network)) + ":" + strings.ToLower(strings.TrimSpace(address))
}

// SingletonKey es la clave de una tarea que solo debe correr en UNA réplica a
// la vez (ciclos de watchers, recovery, reconciliation, descubrimiento).
func SingletonKey(name string) string { return "singleton:" + name }

// MigrateKey es el lock global de migraciones.
const MigrateKey = "migrate:global"

// RunExclusive ejecuta fn solo si obtiene la clave sin esperar. ran=false (y
// err=nil) significa que otra instancia ya lo está ejecutando. Con l == nil
// ejecuta fn directamente (despliegue de una sola instancia / tests).
func RunExclusive(ctx context.Context, l Locker, key string, fn func(context.Context) error) (ran bool, err error) {
	if l == nil {
		return true, fn(ctx)
	}
	unlock, ok, err := l.TryLock(ctx, key)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	defer unlock()
	return true, fn(ctx)
}

// Memory es un Locker en memoria. Una MISMA instancia de Memory compartida por
// dos servicios simula dos réplicas contra una misma base de datos.
type Memory struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
}

func NewMemory() *Memory { return &Memory{slots: map[string]chan struct{}{}} }

func (m *Memory) slot(key string) chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.slots[key]
	if !ok {
		ch = make(chan struct{}, 1)
		m.slots[key] = ch
	}
	return ch
}

func releaser(ch chan struct{}) func() {
	var once sync.Once
	return func() { once.Do(func() { <-ch }) }
}

func (m *Memory) Lock(ctx context.Context, key string) (func(), error) {
	ch := m.slot(key)
	select {
	case ch <- struct{}{}:
		return releaser(ch), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *Memory) TryLock(ctx context.Context, key string) (func(), bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	ch := m.slot(key)
	select {
	case ch <- struct{}{}:
		return releaser(ch), true, nil
	default:
		return nil, false, nil
	}
}
