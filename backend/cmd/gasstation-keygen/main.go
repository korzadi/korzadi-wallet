// Command gasstation-keygen generates a new BSC address for the withdrawal
// gas station, and encrypts its private key using the same
// custody key material already used for every deposit address (active key
// of CUSTODY_ENCRYPTION_KEYS, or the legacy CUSTODY_ENCRYPTION_KEY) — so this
// reuses the exact encryption path already exercised in production. The
// output is v1 ("v1:<keyID>:...") bound to the AAD env/<var>/<address>.
//
// OPERATIONAL TOOL: shipped only in the "tools" image target, never in the
// production runtime image. See docs/CUSTODY_KEY_MANAGEMENT.md.
//
// Usage:
//
//	CUSTODY_ENCRYPTION_KEYS=... CUSTODY_ACTIVE_KEY_ID=... gasstation-keygen
//
// Output: paste GAS_STATION_ADDRESS and GAS_STATION_ENCRYPTED_PRIVATE_KEY
// into your .env. Then send a small amount of BNB (enough for a few dozen
// ERC-20 transfers — a few tenths of a BNB is plenty to start) to
// GAS_STATION_ADDRESS before enabling withdrawals.
package main

import (
	"fmt"
	"log"
	"os"

	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/evmwallet"
)

func main() {
	provider, err := cryptutil.LoadEnvKeyProvider(os.Getenv)
	if err != nil {
		log.Fatalf("claves de custodia inválidas (usa la misma configuración que producción): %v", err)
	}
	encryptor := cryptutil.New(provider)

	keyPair, err := evmwallet.GenerateKeyPair()
	if err != nil {
		log.Fatalf("generar par de claves: %v", err)
	}

	encryptedKey, err := encryptor.Encrypt(keyPair.PrivateKeyHex, cryptutil.ForEnvSecret("GAS_STATION_ENCRYPTED_PRIVATE_KEY", keyPair.Address))
	if err != nil {
		log.Fatalf("cifrar clave privada: %v", err)
	}

	fmt.Println("=== Gas Station BSC — generado correctamente ===")
	fmt.Println()
	fmt.Printf("GAS_STATION_ADDRESS=%s\n", keyPair.Address)
	fmt.Printf("GAS_STATION_ENCRYPTED_PRIVATE_KEY=%s\n", encryptedKey)
	fmt.Println()
	fmt.Println("Copia esas dos líneas a tu .env.")
	fmt.Println()
	fmt.Println("⚠️  IMPORTANTE:")
	fmt.Println("  - Esta es la ÚNICA vez que la clave privada en texto plano existe fuera de la app.")
	fmt.Println("  - No la guardes en ningún otro lugar, no la mandes por chat/email.")
	fmt.Println("  - Envía una cantidad pequeña de BNB a esa dirección antes de habilitar retiros")
	fmt.Println("    (suficiente para varias decenas de transferencias — unas pocas décimas de BNB alcanza para empezar).")
	fmt.Println("  - Verifica la dirección en bscscan.com antes de enviarle fondos.")
}
