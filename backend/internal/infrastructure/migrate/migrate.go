package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/application/locking"
	"korzadi-wallet/backend/internal/infrastructure/pglock"
)

// DefaultLockTimeout es lo máximo que una instancia espera a que otra termine
// de migrar antes de abortar el arranque.
const DefaultLockTimeout = 2 * time.Minute

// Run aplica las migraciones pendientes bajo un pg_advisory_lock GLOBAL: con
// varias réplicas arrancando a la vez, solo una migra y las demás esperan y
// luego encuentran todo aplicado.
func Run(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	return RunWithLockTimeout(ctx, pool, dir, DefaultLockTimeout)
}

// RunWithLockTimeout es Run con un tiempo máximo de espera del lock.
func RunWithLockTimeout(ctx context.Context, pool *pgxpool.Pool, dir string, lockTimeout time.Duration) error {
	if lockTimeout <= 0 {
		lockTimeout = DefaultLockTimeout
	}
	lockID := pglock.KeyID(locking.MigrateKey)

	lctx, cancel := context.WithTimeout(ctx, lockTimeout)
	conn, err := pool.Acquire(lctx)
	if err != nil {
		cancel()
		return fmt.Errorf("migraciones: obtener conexión para el lock: %w", err)
	}
	// pg_advisory_lock bloquea hasta que la otra instancia termina (o vence lctx).
	if _, err := conn.Exec(lctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		cancel()
		discardConn(conn) // estado incierto: cerrar la sesión libera cualquier lock
		return fmt.Errorf("migraciones: esperar el lock global (¿otra instancia migrando más de %s?): %w", lockTimeout, err)
	}
	cancel()
	defer func() {
		uctx, ucancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer ucancel()
		var released bool
		if err := conn.QueryRow(uctx, `SELECT pg_advisory_unlock($1)`, lockID).Scan(&released); err != nil || !released {
			discardConn(conn)
			return
		}
		conn.Release()
	}()

	return apply(ctx, pool, dir)
}

func discardConn(conn *pgxpool.Conn) {
	raw := conn.Hijack()
	cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = raw.Close(cctx)
}

func apply(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     VARCHAR(255) PRIMARY KEY,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("crear tabla schema_migrations: %w", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("leer directorio de migraciones %s: %w", dir, err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, filename := range files {
		version := strings.TrimSuffix(filename, ".up.sql")

		var alreadyApplied bool
		err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).
			Scan(&alreadyApplied)
		if err != nil {
			return fmt.Errorf("verificar migración %s: %w", version, err)
		}
		if alreadyApplied {
			continue
		}

		content, err := os.ReadFile(filepath.Join(dir, filename))
		if err != nil {
			return fmt.Errorf("leer migración %s: %w", filename, err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("iniciar transacción para %s: %w", filename, err)
		}

		if _, err := tx.Exec(ctx, string(content)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("aplicar migración %s: %w", filename, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("registrar migración %s: %w", filename, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("confirmar migración %s: %w", filename, err)
		}
	}

	return nil
}
