// Command watcher-backfill re-escanea un rango de bloques de una red EVM para
// recuperar depósitos de un hueco histórico (p. ej. tras una caída larga).
//
// Es una herramienta INTERNA de operación (no hay endpoint HTTP). Solo
// registra eventos como 'detected'; NO acredita nada ni toca el checkpoint:
// el watcher en vivo los confirma y acredita por el flujo normal (idempotente).
//
// Uso (dentro de la red de docker, con las mismas variables de entorno del
// backend):
//
//	docker compose run --rm --entrypoint /watcher-backfill backend \
//	    -network=BSC -from=126000000 -to=126010000
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	appwatcher "korzadi-wallet/backend/internal/application/watcher"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/assetregistry"
	"korzadi-wallet/backend/internal/infrastructure/bscclient"
	"korzadi-wallet/backend/internal/infrastructure/evmclient"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

func main() {
	network := flag.String("network", "", "red EVM: BSC, ETHEREUM, POLYGON, ARBITRUM, OPTIMISM, BASE")
	from := flag.Uint64("from", 0, "primer bloque a escanear (inclusive)")
	to := flag.Uint64("to", 0, "último bloque a escanear (inclusive; se recorta al bloque seguro)")
	maxBlocks := flag.Uint64("max-blocks", 200000, "tope de bloques por ejecución (protección contra errores de tipeo)")
	chunk := flag.Uint64("chunk", 100, "bloques por lote de getLogs")
	flag.Parse()

	net := domain.Network(strings.ToUpper(strings.TrimSpace(*network)))
	if net == "" || *from == 0 || *to == 0 || *from > *to {
		fmt.Fprintln(os.Stderr, "uso: watcher-backfill -network=BSC -from=N -to=M")
		os.Exit(2)
	}
	if *to-*from+1 > *maxBlocks {
		log.Fatalf("rango de %d bloques excede -max-blocks=%d; divide el trabajo en tramos más pequeños", *to-*from+1, *maxBlocks)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL no definido")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("no se pudo conectar a postgres: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("postgres no responde: %v", err)
	}

	provider, confirmations, err := buildProvider(ctx, net)
	if err != nil {
		log.Fatalf("proveedor RPC de %s: %v", net, err)
	}

	assets, err := assetregistry.LoadEVMNetworkAssets(net, confirmations)
	if err != nil {
		log.Fatalf("configuración de assets de %s inválida: %v", net, err)
	}

	resolver := postgres.NewAddressToUserResolver(pool)
	w := appwatcher.NewEVMDepositWatcher(
		provider,
		resolver,
		resolver,
		postgres.NewBlockchainTransferRepository(pool),
		postgres.NewBlockchainSyncStateRepository(pool), // no se usa: el backfill no toca el checkpoint
		nil, // confirmer: el backfill no acredita
		appwatcher.Config{
			Network:               net,
			Assets:                toWatcherAssets(assets),
			ConfirmationsRequired: confirmations,
			PollInterval:          time.Second, // no se usa
			MaxBlocksPerScan:      *chunk,
			SafetyLagBlocks:       3,
		},
	)
	w.WithFailedEventStore(postgres.NewWatcherFailedEventRepository(pool))

	if err := w.Backfill(ctx, *from, *to); err != nil {
		log.Fatalf("backfill falló: %v", err)
	}
	log.Printf("backfill de %s [%d,%d] terminado; el watcher en vivo confirmará y acreditará los depósitos nuevos", net, *from, *to)
}

func buildProvider(ctx context.Context, network domain.Network) (domain.BlockchainProvider, uint64, error) {
	if network == domain.NetworkBSC {
		cfg, err := bscclient.LoadConfigFromEnv()
		if err != nil {
			return nil, 0, err
		}
		client, err := bscclient.Dial(ctx, cfg)
		if err != nil {
			return nil, 0, err
		}
		return bscclient.NewProvider(client, cfg), cfg.ConfirmationsRequired, nil
	}
	for _, nd := range evmclient.KnownEVMNetworks() {
		if nd.Network != network {
			continue
		}
		cfg, ok, err := evmclient.LoadConfigFromEnv(nd)
		if err != nil {
			return nil, 0, err
		}
		if !ok {
			return nil, 0, fmt.Errorf("%s_RPC_URL no definido", nd.EnvPrefix)
		}
		client, err := evmclient.Dial(ctx, cfg)
		if err != nil {
			return nil, 0, err
		}
		return evmclient.NewProvider(client, cfg), cfg.ConfirmationsRequired, nil
	}
	return nil, 0, fmt.Errorf("red EVM desconocida: %s", network)
}

func toWatcherAssets(assets []domain.NetworkAsset) []appwatcher.EVMAsset {
	out := make([]appwatcher.EVMAsset, 0, len(assets))
	for _, a := range assets {
		out = append(out, appwatcher.EVMAsset{
			AssetCode:       a.AssetCode,
			Kind:            a.Kind,
			ContractAddress: a.ContractAddress,
			Decimals:        a.Decimals,
		})
	}
	return out
}
