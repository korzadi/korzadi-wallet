// Comando de arranque del panel de administración.
//
// Crea la PRIMERA cuenta de administrador sin recibir la contraseña por
// argumentos (nunca queda en el historial del shell). La contraseña se pide
// por terminal, se guarda con argon2id y el secreto TOTP se muestra UNA vez
// para enrolarlo en la app autenticadora.
//
// Uso:
//   go run ./cmd/admin -email admin@ejemplo.com -name "Admin"
//
// Requiere DATABASE_URL y TWO_FACTOR_ENCRYPTION_KEY en el entorno/.env.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"
	"golang.org/x/term"

	appadmin "korzadi-wallet/backend/internal/application/admin"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

func main() {
	_ = godotenv.Load()
	email := flag.String("email", "", "correo del administrador")
	name := flag.String("name", "", "nombre visible (opcional)")
	role := flag.String("role", "super_admin", "rol: super_admin|operador_financiero|soporte_p2p|solo_lectura")
	flag.Parse()

	*email = strings.ToLower(strings.TrimSpace(*email))
	if *email == "" || !strings.Contains(*email, "@") {
		log.Fatal("indica -email con un correo válido")
	}
	r := appadmin.Role(*role)
	if !r.Valid() {
		log.Fatal("rol inválido")
	}
	if r != appadmin.RoleSuperAdmin {
		log.Fatal("el primer administrador debe ser super_admin")
	}
	password := readPassword()
	if !strong(password) {
		log.Fatal("la contraseña debe tener al menos 12 caracteres, mayúscula, minúscula y número")
	}
	if password != readPasswordConfirm() {
		log.Fatal("las contraseñas no coinciden")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL no está definida")
	}
	key := os.Getenv("TWO_FACTOR_ENCRYPTION_KEY")
	if key == "" {
		log.Fatal("TWO_FACTOR_ENCRYPTION_KEY no está definida")
	}
	enc, err := cryptutil.NewEncryptor(key)
	if err != nil {
		log.Fatal("TWO_FACTOR_ENCRYPTION_KEY inválida")
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("no se pudo conectar a la base de datos: %v", err)
	}
	defer pool.Close()

	repo := postgres.NewAdminRepository(pool)
	if _, err := repo.FindByEmail(ctx, *email); err == nil {
		log.Fatal("ya existe un administrador con ese correo")
	}

	hash, err := authjwt.HashPassword(password)
	if err != nil {
		log.Fatal("no se pudo derivar la contraseña")
	}
	keyTOTP, err := totp.Generate(totp.GenerateOpts{Issuer: "Korzadi Admin", AccountName: *email})
	if err != nil {
		log.Fatal("no se pudo generar el secreto TOTP")
	}
	id := uuid.New()
	encSecret, err := enc.Encrypt(keyTOTP.Secret(), cryptutil.AAD{Table: "admin_accounts", Column: "totp_secret_encrypted", Ref: id.String()})
	if err != nil {
		log.Fatal("no se pudo cifrar el secreto TOTP")
	}
	display := strings.TrimSpace(*name)
	if display == "" {
		display = strings.Split(*email, "@")[0]
	}
	if err := repo.CreateAccount(ctx, &appadmin.Account{
		ID: id, Email: *email, DisplayName: display, PasswordHash: hash,
		Role: r, Status: "active", TOTPSecretEncrypted: encSecret,
	}); err != nil {
		log.Fatalf("no se pudo crear el administrador: %v", err)
	}
	if err := repo.SetTOTPConfirmed(ctx, id); err != nil {
		log.Fatalf("no se pudo confirmar el TOTP: %v", err)
	}
	_ = repo.Audit(ctx, appadmin.AuditEntry{
		ID: uuid.New(), AdminID: &id, AdminEmail: *email, Action: "admin.bootstrap",
		EntityType: "admin_account", EntityID: &id, Reason: "creación inicial por CLI",
		Result: "success", IP: "local",
	})

	fmt.Println("Administrador creado.")
	fmt.Println("Enrola este secreto en la app autenticadora (solo se muestra ahora):")
	fmt.Println("  " + keyTOTP.URL())
	fmt.Println("  secreto: " + keyTOTP.Secret())
	fmt.Println("Después inicia sesión en /admin con correo + contraseña + código TOTP.")
}

func readPassword() string {
	fmt.Print("Contraseña: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		log.Fatal("no se pudo leer la contraseña")
	}
	return string(b)
}

func readPasswordConfirm() string {
	fmt.Print("Repite la contraseña: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		log.Fatal("no se pudo leer la contraseña")
	}
	return string(b)
}

func strong(p string) bool {
	if len(p) < 12 {
		return false
	}
	var u, l, d bool
	for _, c := range p {
		switch {
		case c >= 'A' && c <= 'Z':
			u = true
		case c >= 'a' && c <= 'z':
			l = true
		case c >= '0' && c <= '9':
			d = true
		}
	}
	return u && l && d
}
