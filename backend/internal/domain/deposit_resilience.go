package domain

import (
	"github.com/google/uuid"
)

// FailedDepositEvent es un evento on-chain que el watcher detectó pero no
// pudo registrar. Se persiste para reintentarlo en vez de perderlo.
type FailedDepositEvent struct {
	ID        uuid.UUID
	Transfer  BlockchainTransfer
	Attempts  int
	LastError string
}

// DepositIncidentKind clasifica un incidente de depósitos que requiere
// revisión humana. Ninguno se resuelve automáticamente.
type DepositIncidentKind string

const (
	// IncidentReorgAfterCredit: un depósito YA acreditado en el ledger dejó de
	// estar en la cadena canónica (tx desaparecida o movida de bloque).
	IncidentReorgAfterCredit DepositIncidentKind = "reorg_after_credit"
	// IncidentRevertedAfterCredit: un depósito ya acreditado ahora tiene
	// receipt con status=false.
	IncidentRevertedAfterCredit DepositIncidentKind = "reverted_after_credit"
	// IncidentPoisonedEvent: un evento agotó sus reintentos sin poder
	// registrarse.
	IncidentPoisonedEvent DepositIncidentKind = "poisoned_event"
)

// DepositIncident es el registro de auditoría de un incidente.
type DepositIncident struct {
	Network Network
	Kind    DepositIncidentKind
	// TransferID es uuid.Nil cuando el evento nunca llegó a persistirse
	// como blockchain_transfer (p. ej. IncidentPoisonedEvent).
	TransferID uuid.UUID
	Transfer   BlockchainTransfer
	Details    string
}
