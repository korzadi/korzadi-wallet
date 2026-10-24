package blockchainwithdrawal

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/locking"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
)

const defaultHotLockWait = 30 * time.Second

// lockHot serializa la sección "comprobar saldo + firmar" de UNA wallet HOT
// entre TODAS las réplicas (pg_advisory_lock por network+address; la dirección
// se normaliza a minúsculas). Devuelve la función de liberación:
//
//	unlock, err := s.lockHot(ctx, network, addr); if err != nil {...}; defer unlock()
//
// Si no se obtiene en HotLockWait (por defecto 30s) devuelve ErrHotLockBusy:
// el worker reintenta sin consumir intentos del retiro. Sin Locks configurado
// falla cerrado (ErrNoLocker) en vez de degradar a un lock local.
func (s *Service) lockHot(ctx context.Context, network domain.Network, address string) (func(), error) {
	if s.Locks == nil {
		return nil, ErrNoLocker
	}
	wait := s.HotLockWait
	if wait <= 0 {
		wait = defaultHotLockWait
	}
	lctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	unlock, err := s.Locks.Lock(lctx, locking.HotKey(string(network), address))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrHotLockBusy, err)
	}
	return unlock, nil
}

// HotKeyLookup finds the ENCRYPTED key of an ACTIVE custody HOT wallet by
// (network, address). Satisfied by postgres.CustodyWalletRepository.
type HotKeyLookup interface {
	FindActiveHotKey(ctx context.Context, network domain.Network, address string) (encrypted string, found bool, err error)
}

// HotFirstKeyResolver resolves the signing key for an address: if the address
// is an ACTIVE custody HOT wallet it returns that key (Camino B withdrawals);
// otherwise it falls back to the user-scoped deposit-address resolver (used by
// sweeps, which sign from user deposit addresses). A user can never influence
// which source address is used — the withdrawal service picks it.
type HotFirstKeyResolver struct {
	Hot  HotKeyLookup
	User KeyResolver
}

func (r HotFirstKeyResolver) FindEncryptedPrivateKey(ctx context.Context, network domain.Network, address string, userID uuid.UUID) (string, error) {
	key, _, err := r.FindEncryptedKeyWithAAD(ctx, network, address, userID)
	return key, err
}

// FindEncryptedKeyWithAAD implements KeySourceResolver: a HOT wallet blob is
// bound to custody_wallets, a user deposit-address blob to crypto_addresses.
func (r HotFirstKeyResolver) FindEncryptedKeyWithAAD(ctx context.Context, network domain.Network, address string, userID uuid.UUID) (string, cryptutil.AAD, error) {
	if r.Hot != nil {
		key, found, err := r.Hot.FindActiveHotKey(ctx, network, address)
		if err != nil {
			return "", cryptutil.AAD{}, err
		}
		if found {
			return key, cryptutil.ForCustodyWallet(address), nil
		}
	}
	key, err := r.User.FindEncryptedPrivateKey(ctx, network, address, userID)
	if err != nil {
		return "", cryptutil.AAD{}, err
	}
	return key, cryptutil.ForCryptoAddress(address), nil
}
