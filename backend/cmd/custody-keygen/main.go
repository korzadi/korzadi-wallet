// Command custody-keygen creates and lists Korzadi's own custody wallets
// (HOT, GAS, TREASURY) on EVM networks.
//
// It reuses the production registration path (application/custody.Service):
// address validation, asset/network validation, encryption with
// the custody encryption keys (CUSTODY_ENCRYPTION_KEYS) and the audit trail. The PRIVATE KEY IS NEVER
// PRINTED: it is generated in memory, encrypted, stored, and dropped. Only the
// public address is shown.
//
// OPERATIONAL TOOL: shipped only in the "tools" image target, never in the
// production runtime image. See docs/CUSTODY_KEY_MANAGEMENT.md.
//
// Usage (from the tools image or a shell with DATABASE_URL and
// CUSTODY_ENCRYPTION_KEYS / CUSTODY_ACTIVE_KEY_ID set):
//
//	custody-keygen -list
//	custody-keygen -network BSC -asset USDT -type HOT
//
// Scope: EVM networks only (BSC, ETHEREUM, POLYGON, ARBITRUM, OPTIMISM, BASE).
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/application/custody"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/evmwallet"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

// registryCheck adapts the network_assets registry to custody's validator port.
type registryCheck struct {
	repo *postgres.NetworkAssetRepository
}

func (r registryCheck) IsEnabled(ctx context.Context, network domain.Network, assetCode string) (bool, error) {
	na, found, err := r.repo.Get(ctx, network, assetCode)
	if err != nil {
		return false, err
	}
	return found && na.Enabled, nil
}

func main() {
	var (
		list    = flag.Bool("list", false, "lista las wallets de custody (solo direcciones, nunca claves)")
		network = flag.String("network", "", "red EVM: BSC, ETHEREUM, POLYGON, ARBITRUM, OPTIMISM o BASE")
		asset   = flag.String("asset", "", "activo que maneja la wallet (p. ej. USDT, USDC, ETH, BNB)")
		wtype   = flag.String("type", "", "tipo de wallet: HOT, GAS o TREASURY")
		actor   = flag.String("actor", "custody-keygen", "quién registra (queda en la auditoría)")
		replace = flag.Bool("allow-replace", false, "permite crear otra wallet activa del mismo tipo/red/activo (la nueva pasa a ser la usada)")
	)
	flag.Parse()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL no definido")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("conectar a la base de datos: %v", err)
	}
	defer pool.Close()
	repo := postgres.NewCustodyWalletRepository(pool)

	if *list {
		wallets, err := repo.List(ctx, postgres.CustodyWalletFilter{})
		if err != nil {
			log.Fatalf("listar: %v", err)
		}
		if len(wallets) == 0 {
			fmt.Println("(no hay wallets de custody registradas)")
			return
		}
		fmt.Printf("%-9s %-9s %-6s %-9s %s\n", "TIPO", "RED", "ACTIVO", "ESTADO", "DIRECCION")
		for _, w := range wallets {
			fmt.Printf("%-9s %-9s %-6s %-9s %s\n", w.WalletType, w.Network, w.AssetCode, w.Status, w.Address)
		}
		return
	}

	net := strings.ToUpper(strings.TrimSpace(*network))
	if !domain.Network(net).IsEVM() {
		log.Fatalf("red %q no soportada por esta herramienta (solo EVM: BSC, ETHEREUM, POLYGON, ARBITRUM, OPTIMISM, BASE)", *network)
	}
	wt := strings.ToUpper(strings.TrimSpace(*wtype))
	switch custody.WalletType(wt) {
	case custody.WalletTypeHot, custody.WalletTypeGas, custody.WalletTypeTreasury:
	default:
		log.Fatalf("tipo %q inválido: usa HOT, GAS o TREASURY", *wtype)
	}
	assetCode := strings.ToUpper(strings.TrimSpace(*asset))
	if assetCode == "" {
		log.Fatal("falta -asset")
	}

	// Safety: the withdrawal flow uses the NEWEST active wallet of a
	// (type, network, asset). Creating a second one would silently re-point
	// payments to the new address and strand funds in the old one.
	if !*replace {
		existing, err := repo.FindActive(ctx, wt, net, assetCode)
		if err != nil {
			log.Fatalf("comprobar wallets existentes: %v", err)
		}
		if existing != nil {
			log.Fatalf("ya existe una wallet %s activa para %s/%s (%s). Si de verdad quieres reemplazarla usa -allow-replace.", wt, net, assetCode, existing.Address)
		}
	}

	fmt.Printf("Se creará una wallet %s para %s/%s.\n", wt, net, assetCode)
	fmt.Print("Escribe CREAR para continuar: ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(line) != "CREAR" {
		log.Fatal("cancelado")
	}

	provider, err := cryptutil.LoadEnvKeyProvider(os.Getenv)
	if err != nil {
		log.Fatalf("claves de custodia inválidas: %v", err)
	}
	encryptor := cryptutil.New(provider)

	kp, err := evmwallet.GenerateKeyPair()
	if err != nil {
		log.Fatalf("generar par de claves: %v", err)
	}

	svc := custody.NewService(repo, encryptor, postgres.NewCustodyAuditRepository(pool),
		registryCheck{repo: postgres.NewNetworkAssetRepository(pool)})
	created, err := svc.Register(ctx, custody.RegisterInput{
		Network:             domain.Network(net),
		AssetCode:           assetCode,
		WalletType:          custody.WalletType(wt),
		Address:             kp.Address,
		AddressType:         "evm",
		PrivateKeyPlaintext: kp.PrivateKeyHex,
		Actor:               *actor,
	})
	kp.PrivateKeyHex = "" // drop the plaintext reference immediately
	if err != nil {
		log.Fatalf("registrar la wallet: %v", err)
	}

	fmt.Println()
	fmt.Printf("Wallet %s creada y activa.\n", wt)
	fmt.Printf("  Red:       %s\n", created.Network)
	fmt.Printf("  Activo:    %s\n", created.AssetCode)
	fmt.Printf("  Dirección: %s\n", created.Address)
	fmt.Println()
	fmt.Println("La clave privada quedó cifrada en la base de datos y NO se muestra.")
	fmt.Println("Verifica la dirección en el explorador de la red antes de enviarle fondos.")
}
