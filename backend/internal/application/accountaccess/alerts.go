package accountaccess

import (
	"context"
	"log"

	"github.com/google/uuid"
)

// Alert avisa a un usuario de un evento de seguridad (p. ej. "nueva dirección de
// retiro"): notificación interna + correo. Va en segundo plano y nunca devuelve
// error al llamador. Implementa blockchainwithdrawal.Alerter.
func (s *Service) Alert(ctx context.Context, userID uuid.UUID, title, body string) {
	s.dispatch(ctx, "security-alert", func(c context.Context) {
		s.notify(c, userID, "security_alert", title, body)
		if s.d.Sender == nil {
			return
		}
		user, err := s.d.Users.FindByID(c, userID)
		if err != nil || user == nil {
			return
		}
		if err := s.d.Sender.Send(c, user.Email, title+" — Korzadi Wallet", body+"\n\n— Korzadi Wallet"); err != nil {
			log.Printf("accountaccess: no se pudo enviar el aviso de seguridad user=%s: %v", userID, err)
		}
	})
}
