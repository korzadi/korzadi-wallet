package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RoleRepository struct{ pool *pgxpool.Pool }

func NewRoleRepository(pool *pgxpool.Pool) *RoleRepository { return &RoleRepository{pool: pool} }

func (r *RoleRepository) GetRolesForUser(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT r.name FROM roles r
		JOIN user_roles ur ON ur.role_id = r.id
		WHERE ur.user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	roles := []string{"user"} // todo usuario tiene el rol base implícito
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if name != "user" {
			roles = append(roles, name)
		}
	}
	return roles, rows.Err()
}

func (r *RoleRepository) AssignRole(ctx context.Context, userID uuid.UUID, roleName string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id)
		SELECT $1, id FROM roles WHERE name=$2
		ON CONFLICT DO NOTHING
	`, userID, roleName)
	return err
}
