package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"korzadi-wallet/backend/internal/infrastructure/postgres/p2p"
)

func NewP2PRepository(pool *pgxpool.Pool) *p2p.Repo {
	return p2p.New(pool)
}
