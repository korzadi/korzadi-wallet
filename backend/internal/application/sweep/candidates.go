package sweep

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"

	bw "korzadi-wallet/backend/internal/application/blockchainwithdrawal"
	"korzadi-wallet/backend/internal/domain"
)

// custodyWalletTypeTreasury mirrors custody.WalletTypeTreasury's value
// ("TREASURY") as a plain string constant. Deliberately NOT importing
// application/custody here: custody imports infrastructure/postgres (for
// *postgres.CustodyWallet, same reason gasstation does), and postgres must
// import sweep to implement Store/CandidateSource — so sweep importing
// custody would close the cycle postgres -> sweep -> custody -> postgres.
// CustodyWalletRegistry.FindActive takes walletType as a plain string
// precisely so callers never need custody's package for this one constant.
const custodyWalletTypeTreasury = "TREASURY"

// DiscoverAndClaim scans confirmed-deposit candidates and turns the
// eligible ones into persisted, claimed `pending` sweeps.
//
// This is the ONLY place eligibility is decided, and it runs entirely
// BEFORE any row is written: a candidate that fails any check (unsupported
// network, asset disabled, no dust threshold configured, balance below
// dust, no TREASURY destination) is simply skipped — no row, no
// "failed" record, nothing to retry. This is what keeps the worker from
// looping aggressively on an address that has a confirmed deposit but
// nothing currently worth sweeping (spec: "No debe entrar en loop
// agresivo."): the SAME address will simply be re-evaluated, at the same
// low cost, on the next tick, and only crosses into `sweeps` once a real,
// fundable opportunity exists.
//
// Returns how many candidates were evaluated and how many became sweeps.
func (s *Service) DiscoverAndClaim(ctx context.Context) (evaluated, claimed int, err error) {
	if !s.deps.Config.Enabled {
		return 0, 0, ErrSweepsDisabled
	}
	candidates, err := s.deps.Candidates.FindCandidates(ctx, s.deps.Config.CandidateScan)
	if err != nil {
		return 0, 0, fmt.Errorf("sweep: buscando candidatos: %w", err)
	}
	for _, c := range candidates {
		evaluated++
		sw, verr := s.evaluateCandidate(ctx, c)
		if verr != nil {
			// Not eligible right now — expected and frequent, never fatal
			// to the scan. Distinct sentinel errors let callers/tests
			// assert exactly why without parsing strings.
			continue
		}
		if _, cerr := s.deps.Store.Create(ctx, sw); cerr != nil {
			if errors.Is(cerr, ErrInFlight) {
				// Another worker (or an earlier tick) already claimed this
				// exact (network, asset, from_address) — success, not an
				// error: this is precisely the "only one sweep can claim
				// it" guarantee doing its job.
				continue
			}
			return evaluated, claimed, fmt.Errorf("sweep: creando candidato %s/%s: %w", c.Network, c.FromAddress, cerr)
		}
		claimed++
		s.auditWallet(ctx, &sw.ToCustodyWalletID, "sweep_detected", map[string]any{
			"network": string(c.Network), "asset_code": c.AssetCode,
			"from_address": c.FromAddress, "amount": sw.Amount.String(),
		})
	}
	return evaluated, claimed, nil
}

// evaluateCandidate applies every eligibility check described in the
// package doc and, only if all pass, returns a ready-to-persist Sweep with
// its Amount priced against a FRESH on-chain balance read (never against
// blockchain_deposits' historical amounts — see model.go).
func (s *Service) evaluateCandidate(ctx context.Context, c Candidate) (*Sweep, error) {
	// 1. Network family: sweeps are EVM-only in this block. TRON is
	// explicitly out of scope (see docs/SWEEPS_7_3.md) because
	// tronclient/tronwallet has no signing or broadcast capability today —
	// implementing a partial TRON sweep would violate "es preferible
	// soportar correctamente una red que crear una implementación
	// incompleta para varias."
	if !c.Network.IsEVM() {
		return nil, ErrUnsupportedNetwork
	}
	runtime, ok := s.runtimeFor(c.Network)
	if !ok {
		return nil, ErrNetworkNotConfigured
	}
	if s.deps.IsMainnetChain(c.Network) && !s.deps.Config.MainnetEnabled {
		return nil, ErrNetworkNotConfigured
	}

	// 2. Asset must be enabled in the SAME registry blockchainwithdrawal
	// and deposit already trust — no separate sweep asset list.
	netAsset, found, err := s.deps.Registry.Get(ctx, c.Network, c.AssetCode)
	if err != nil {
		return nil, fmt.Errorf("sweep: registry: %w", err)
	}
	if !found || !netAsset.Enabled {
		return nil, ErrAssetNotEnabled
	}

	// 3. Dust threshold must be explicitly configured for this asset.
	dust, ok := s.deps.Config.DustThresholdFor(c.AssetCode)
	if !ok {
		return nil, ErrAssetNotEnabled
	}

	// 4. Custody destination must exist NOW. Fail closed otherwise.
	wallet, werr := s.deps.Wallets.FindActive(ctx, custodyWalletTypeTreasury, string(c.Network), c.AssetCode)
	if werr != nil || wallet == nil {
		return nil, ErrNoCustodyDestination
	}

	// 5. Price against a fresh on-chain read — the only source of truth
	// for "how much is actually sitting at this address right now". Gas is
	// estimated against the REAL destination (a custody wallet could be a
	// contract, e.g. a multisig, whose acceptance cost differs from a
	// plain EOA transfer).
	amount, kind, perr := s.priceCandidate(ctx, runtime, netAsset, c, wallet.Address, dust)
	if perr != nil {
		return nil, perr
	}

	// Freeze the confirmation target at creation time — same convention as
	// blockchainwithdrawal.Service.CreateWithdrawal — so a later change to
	// the network's default never re-litigates how many confirmations an
	// already-broadcast sweep needs.
	required := int64(runtime.Chain.ConfirmationsRequired())
	if netAsset.ConfirmationsRequired > 0 {
		required = int64(netAsset.ConfirmationsRequired)
	}

	return &Sweep{
		Network:               c.Network,
		AssetCode:             c.AssetCode,
		AssetKind:             kind,
		FromAddress:           c.FromAddress,
		FromUserID:            c.FromUserID,
		FromCryptoAddressID:   c.FromCryptoAddressID,
		ToCustodyWalletID:     wallet.ID,
		ToAddress:             wallet.Address,
		Amount:                amount,
		Status:                StatusPending,
		RequiredConfirmations: &required,
	}, nil
}

// priceCandidate reads the live balance and returns the amount that will
// actually be swept. For a native asset the sweep amount is the balance
// MINUS the gas the sweep transaction itself will cost (a native sweep
// pays its own way out of the same balance it drains) — for a token the
// full token balance is swept and gas is paid separately by the native
// coin at that same address (topped up via Gas Station if short, see
// process.go), never out of the token amount itself.
func (s *Service) priceCandidate(ctx context.Context, rt NetworkRuntime, netAsset domain.NetworkAsset, c Candidate, toAddress string, dust decimal.Decimal) (decimal.Decimal, domain.AssetKind, error) {
	scale := decimal.New(1, int32(netAsset.Decimals))

	if netAsset.Kind == domain.AssetKindToken {
		rawBal, err := rt.Chain.TokenBalanceRaw(ctx, netAsset.ContractAddress, c.FromAddress)
		if err != nil {
			return decimal.Decimal{}, "", fmt.Errorf("sweep: leyendo balance de token: %w", err)
		}
		human := decimal.NewFromBigInt(rawBal, 0).Div(scale)
		if human.LessThan(dust) {
			return decimal.Decimal{}, "", ErrBelowDustThreshold
		}
		return human, domain.AssetKindToken, nil
	}

	// Native asset.
	balWei, err := rt.Chain.NativeBalanceWei(ctx, c.FromAddress)
	if err != nil {
		return decimal.Decimal{}, "", fmt.Errorf("sweep: leyendo balance nativo: %w", err)
	}
	if balWei.Sign() <= 0 {
		return decimal.Decimal{}, "", ErrBelowDustThreshold
	}
	gasFees, err := bw.ResolveGasFeeParams(ctx, rt.Chain)
	if err != nil {
		return decimal.Decimal{}, "", fmt.Errorf("sweep: consultando gas price: %w", err)
	}
	gasPrice := gasFees.CapPerGasWei()
	// A plain native transfer's gas usage does not depend on the amount
	// sent, so estimating against the full balance is accurate and safe.
	gasLimit, err := rt.Chain.EstimateGasNative(ctx, c.FromAddress, toAddress, balWei)
	if err != nil {
		return decimal.Decimal{}, "", fmt.Errorf("sweep: estimando gas: %w", err)
	}
	gasLimit += gasLimit / 5 // same 20% headroom convention as blockchainwithdrawal
	gasCost := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), gasPrice)

	netWei := new(big.Int).Sub(balWei, gasCost)
	if netWei.Sign() <= 0 {
		// Balance cannot even cover its own gas — Gas Station can be asked
		// to top it up during processing (see process.go); at candidate
		// time we still require dust to be positive net-of-gas, otherwise
		// every dust-level deposit would perpetually ask Gas Station for a
		// top-up it can never recoup.
		return decimal.Decimal{}, "", ErrInsufficientOnChain
	}
	human := decimal.NewFromBigInt(netWei, 0).Div(scale)
	if human.LessThan(dust) {
		return decimal.Decimal{}, "", ErrBelowDustThreshold
	}
	return human, domain.AssetKindNative, nil
}
