package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type NotificationRepository struct{ pool *pgxpool.Pool }

func NewNotificationRepository(pool *pgxpool.Pool) *NotificationRepository {
	return &NotificationRepository{pool: pool}
}

func (r *NotificationRepository) Notify(ctx context.Context, userID uuid.UUID, eventType, title, body string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO notifications (id, user_id, channel, event_type, title, body)
		VALUES ($1,$2,'internal',$3,$4,$5)
	`, uuid.New(), userID, eventType, title, body)
	return err
}

type NotificationRow struct {
	ID        uuid.UUID `json:"id"`
	EventType string    `json:"event_type"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	ReadAt    *string   `json:"read_at"`
	CreatedAt string    `json:"created_at"`
}

func (r *NotificationRepository) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]NotificationRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, event_type, title, body, read_at::text, created_at::text
		FROM notifications WHERE user_id=$1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []NotificationRow
	for rows.Next() {
		var n NotificationRow
		var body *string
		if err := rows.Scan(&n.ID, &n.EventType, &n.Title, &body, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		if body != nil {
			n.Body = *body
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

func (r *NotificationRepository) MarkRead(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE notifications SET read_at = now() WHERE id=$1 AND user_id=$2 AND read_at IS NULL
	`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("notificación no encontrada")
	}
	return nil
}
