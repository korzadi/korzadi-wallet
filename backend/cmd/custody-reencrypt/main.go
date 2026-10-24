// Command custody-reencrypt re-encrypts, in bulk, every custody ciphertext in
// the database to the CURRENT format: "v1:<keyID>:..." with the ACTIVE key
// and the row-bound AAD. It migrates legacy blobs (no version/AAD) and blobs
// written with a key that is no longer active, so an old key can be retired.
//
// DRY-RUN BY DEFAULT: without -apply nothing is written; the report shows
// what would change. Each row is decrypted, re-encrypted and VERIFIED
// (decrypting the new blob must give the same plaintext) before the write,
// and the write is a compare-and-swap on the old ciphertext, so a concurrent
// writer is never overwritten and the command is safe to re-run (idempotent).
//
// It prints only table names, row references (addresses / user ids, which are
// not secret) and counters — never plaintext, ciphertext or key bytes.
//
// OPERATIONAL TOOL: shipped only in the "tools" image target, never in the
// production runtime image. See docs/CUSTODY_KEY_MANAGEMENT.md.
//
// Usage:
//
//	DATABASE_URL=... CUSTODY_ENCRYPTION_KEYS=... CUSTODY_ACTIVE_KEY_ID=... \
//	  [TWO_FACTOR_ENCRYPTION_KEY=...] custody-reencrypt            # dry-run
//	custody-reencrypt -apply                                        # writes
//	custody-reencrypt -tables custody_wallets,crypto_addresses
//	custody-reencrypt -gas-station-env   # re-cifra GAS_STATION_ENCRYPTED_PRIVATE_KEY (env, sin BD)
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

type stats struct {
	total, current, toMigrate, migrated, stale, failed int
}

func main() {
	var (
		apply  = flag.Bool("apply", false, "ESCRIBE los cambios (sin este flag es dry-run)")
		tables = flag.String("tables", "", "lista separada por comas; por defecto todas: "+strings.Join(postgres.RekeyTables, ","))
		batch  = flag.Int("batch", 200, "filas por lote")
		gasEnv = flag.Bool("gas-station-env", false,
			"re-cifra GAS_STATION_ENCRYPTED_PRIVATE_KEY (variable de entorno) y muestra SOLO el valor cifrado nuevo; no usa la BD")
	)
	flag.Parse()

	custodyProvider, err := cryptutil.LoadEnvKeyProvider(os.Getenv)
	if err != nil {
		log.Fatalf("claves de custodia inválidas: %v", err)
	}
	custodyEnc := cryptutil.New(custodyProvider)

	if *gasEnv {
		reencryptGasStationEnv(custodyEnc)
		return
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL no definido")
	}

	var twoFactorEnc *cryptutil.Encryptor
	if k := os.Getenv("TWO_FACTOR_ENCRYPTION_KEY"); k != "" {
		twoFactorEnc, err = cryptutil.NewEncryptor(k)
		if err != nil {
			log.Fatalf("TWO_FACTOR_ENCRYPTION_KEY inválida: %v", err)
		}
	}

	selected, err := selectTables(*tables)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("conectar a la base de datos: %v", err)
	}
	defer pool.Close()
	repo := postgres.NewCustodyRekeyRepository(pool)

	mode := "DRY-RUN (no se escribe nada; usa -apply para aplicar)"
	if *apply {
		mode = "APPLY (escribe en la base de datos — ¿hay backup reciente?)"
	}
	fmt.Printf("Clave activa: %s | claves conocidas: %s\nModo: %s\n\n",
		custodyProvider.ActiveKeyID(), strings.Join(custodyProvider.KeyIDs(), ","), mode)

	anyFailed, anyPending := false, false
	for _, table := range selected {
		enc := custodyEnc
		if table == cryptutil.TableTwoFactor {
			if twoFactorEnc == nil {
				fmt.Printf("%-24s OMITIDA (TWO_FACTOR_ENCRYPTION_KEY no definido)\n", table)
				continue
			}
			enc = twoFactorEnc
		}
		st, err := processTable(ctx, repo, enc, table, *batch, *apply)
		if err != nil {
			log.Fatalf("%s: %v", table, err)
		}
		fmt.Printf("%-24s total=%d ya_vigentes=%d por_migrar=%d migradas=%d obsoletas(cas)=%d fallidas=%d\n",
			table, st.total, st.current, st.toMigrate, st.migrated, st.stale, st.failed)
		anyFailed = anyFailed || st.failed > 0
		anyPending = anyPending || (*apply && st.stale > 0) || (!*apply && st.toMigrate > 0)
	}

	fmt.Println()
	switch {
	case anyFailed:
		fmt.Println("Hay filas que NO se pudieron procesar: NO retires ninguna clave hasta resolverlas.")
		os.Exit(1)
	case anyPending:
		fmt.Println("Quedan filas pendientes (dry-run o cambios concurrentes). Vuelve a ejecutar.")
	default:
		fmt.Println("Todo está en formato v1 con la clave activa: las claves antiguas ya se pueden retirar.")
	}
}

// reencryptGasStationEnv upgrades the env-held gas-station blob (which cannot
// rewrite itself) to v1/active key, keeping the SAME address. It prints only
// the new CIPHERTEXT, like gasstation-keygen does.
func reencryptGasStationEnv(enc *cryptutil.Encryptor) {
	const name = "GAS_STATION_ENCRYPTED_PRIVATE_KEY"
	addr, cur := os.Getenv("GAS_STATION_ADDRESS"), os.Getenv(name)
	if addr == "" || cur == "" {
		log.Fatal("define GAS_STATION_ADDRESS y GAS_STATION_ENCRYPTED_PRIVATE_KEY")
	}
	aad := cryptutil.ForEnvSecret(name, addr)
	d, err := enc.DecryptDetailed(cur, aad)
	if err != nil {
		log.Fatalf("no se pudo descifrar %s: %v", name, err)
	}
	if !d.NeedsReencrypt {
		fmt.Println("Ya está en formato v1 con la clave activa; no hay nada que cambiar.")
		return
	}
	fresh, err := enc.Encrypt(d.Plaintext, aad)
	if err != nil {
		log.Fatalf("cifrar: %v", err)
	}
	back, err := enc.DecryptDetailed(fresh, aad)
	if err != nil || subtle.ConstantTimeCompare([]byte(back.Plaintext), []byte(d.Plaintext)) != 1 {
		log.Fatal("la verificación del re-cifrado falló; no se emite ningún valor")
	}
	fmt.Printf("%s=%s\n", name, fresh)
	fmt.Println("Reemplaza esa línea en tu entorno seguro y redespliega. La dirección NO cambia.")
}

func selectTables(arg string) ([]string, error) {
	if strings.TrimSpace(arg) == "" {
		return postgres.RekeyTables, nil
	}
	valid := map[string]bool{}
	for _, t := range postgres.RekeyTables {
		valid[t] = true
	}
	var out []string
	for _, t := range strings.Split(arg, ",") {
		t = strings.TrimSpace(t)
		if !valid[t] {
			return nil, fmt.Errorf("tabla no soportada: %q", t)
		}
		out = append(out, t)
	}
	return out, nil
}

func processTable(ctx context.Context, repo *postgres.CustodyRekeyRepository, enc *cryptutil.Encryptor,
	table string, batch int, apply bool) (stats, error) {

	var st stats
	cursor := ""
	for {
		lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		rows, err := repo.ListEncrypted(lctx, table, cursor, batch)
		cancel()
		if err != nil {
			return st, err
		}
		if len(rows) == 0 {
			return st, nil
		}
		for _, row := range rows {
			cursor = row.Cursor
			st.total++

			aad, err := cryptutil.ForTable(table, row.Ref)
			if err != nil {
				st.failed++
				report(table, row.Ref, err)
				continue
			}
			d, err := enc.DecryptDetailed(row.Ciphertext, aad)
			if err != nil {
				st.failed++
				report(table, row.Ref, err)
				continue
			}
			if !d.NeedsReencrypt {
				st.current++
				continue
			}
			fresh, err := enc.Encrypt(d.Plaintext, aad)
			if err != nil {
				st.failed++
				report(table, row.Ref, err)
				continue
			}
			// Verify BEFORE writing: the new blob must decrypt to the same plaintext.
			back, err := enc.DecryptDetailed(fresh, aad)
			if err != nil || back.NeedsReencrypt ||
				subtle.ConstantTimeCompare([]byte(back.Plaintext), []byte(d.Plaintext)) != 1 {
				st.failed++
				report(table, row.Ref, errors.New("verificación del re-cifrado falló"))
				continue
			}
			if !apply {
				st.toMigrate++
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = repo.Rewrite(wctx, aad, row.Ciphertext, fresh)
			cancel()
			switch {
			case err == nil:
				st.migrated++
			case errors.Is(err, postgres.ErrRekeyStale):
				st.stale++
			default:
				st.failed++
				report(table, row.Ref, errors.New("escritura fallida"))
			}
		}
	}
}

// report prints a classified reason only — never the underlying error text,
// which could in theory carry values.
func report(table, ref string, err error) {
	reason := "error"
	switch {
	case errors.Is(err, cryptutil.ErrUnknownKeyID):
		reason = "key id desconocido (¿falta esa clave en CUSTODY_ENCRYPTION_KEYS?)"
	case errors.Is(err, cryptutil.ErrDecryptFail):
		reason = "no descifra (clave, AAD o dato incorrectos)"
	case errors.Is(err, cryptutil.ErrMalformed):
		reason = "formato inválido"
	case err != nil:
		reason = err.Error()
	}
	fmt.Printf("  FALLO %s ref=%s: %s\n", table, ref, reason)
}
