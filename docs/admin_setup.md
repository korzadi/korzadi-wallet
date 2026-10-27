# Administrador de Korzadi

## Estado actual

El primer administrador se creó correctamente mediante el CLI
`backend/cmd/admin` y se confirmó el mensaje `Administrador creado.`.

La cuenta inicial configurada fue:

- Correo: [REDACTADO] (cuenta inicial creada con CLI)
- Rol: `super_admin`
- Estado: activo, según el resultado del CLI.
- Autenticación: contraseña y código TOTP de seis dígitos.

No vuelvas a ejecutar el comando de creación para esta cuenta si ya existe.
El secreto TOTP se muestra una sola vez al crear el administrador y no debe
guardarse en este documento ni en el repositorio.

## Cómo se creó el primer administrador

El backend de producción utiliza una imagen de ejecución que no incluye Go.
Por eso, el CLI se ejecutó desde un contenedor temporal `golang:1.26-alpine`,
con el código del backend montado en modo de solo lectura y conectado a la
red Docker donde PostgreSQL es accesible mediante el nombre `postgres`.

El archivo `.env` se montó en modo de solo lectura. Para la conexión se
construyó una `DATABASE_URL` temporal utilizando `POSTGRES_USER`,
`POSTGRES_PASSWORD` y `POSTGRES_DB`, en lugar de reutilizar la URL antigua
que apuntaba a `localhost` y tenía una contraseña diferente.

El procedimiento utilizado fue:

```bash
cd /home/korzadi/korzadi-wallet

docker run --rm -it \
  --network korzadi-wallet_default \
  -v "$PWD/backend:/src:ro" \
  -v "$PWD/.env:/run/project.env:ro" \
  -w /src \
  --entrypoint sh \
  golang:1.26-alpine -c '
    set -eu
    set -a
    . /run/project.env
    set +a

    export DATABASE_URL="postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable"

    exec go run ./cmd/admin \
      -email [REDACTADO] \
      -name "Korzadi" \
      -role super_admin
  '
```

Este comando es para la creación inicial, no para el acceso habitual.
Solo debe repetirse si se ha comprobado que la cuenta no existe y se necesita
crearla de nuevo. Si las credenciales de PostgreSQL cambian, revisa antes
la configuración y la codificación de la URL de conexión.

## Acceder al panel de administración

1. Abre https://wallet.korzadi.com/admin.
2. Introduce el correo de la cuenta administradora.
3. Introduce la contraseña establecida durante la creación.
4. Introduce el código TOTP actual de seis dígitos generado por la aplicación
   autenticadora que registraste con el secreto mostrado por el CLI.
5. Completa el inicio de sesión.

Si el panel no carga correctamente, comprueba primero la ruta de acceso y
el mensaje de error. No vuelvas a crear la cuenta solo porque falle el login.

## Seguridad del secreto TOTP

- Guarda el secreto TOTP en un gestor de contraseñas seguro.
- No lo incluyas en `.md`, Git, registros, capturas públicas ni mensajes.
- No compartas códigos TOTP ni contraseñas.
- Si el secreto se expone, revoca o vuelve a registrar el factor mediante
  el procedimiento de seguridad admitido por el proyecto.

## Advertencia sobre credenciales

No escribas contraseñas de PostgreSQL ni claves de cifrado en comandos
guardados, documentación o repositorios.

La contraseña de `DATABASE_URL` en `.env` anteriormente no coincidía con
`POSTGRES_PASSWORD`. Antes de modificar credenciales, confirma cuál es la
configuración efectiva y planifica la actualización coordinada del backend
y PostgreSQL.

No cambies `TWO_FACTOR_ENCRYPTION_KEY` sin un plan de migración: hacerlo
puede impedir descifrar secretos 2FA que ya estén almacenados.
