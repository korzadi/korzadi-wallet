# Gestión de claves de custodia: cifrado v1, rotación y herramientas

## 1. Formato de cifrado

Los secretos de custodia se guardan como

```
v1:<keyID>:<base64(nonce || ciphertext+tag)>      (AES-256-GCM)
```

autenticado con **AAD** (datos adicionales autenticados, no secretos y no
almacenados; se recalculan al leer). El AAD une el blob a *dónde vive*, de modo
que copiar un blob a otra fila/columna, o intercambiar dos wallets, falla la
autenticación.

| Columna | AAD (tabla · columna · referencia) |
|---|---|
| `crypto_addresses.encrypted_private_key` | `address` (UNIQUE; EVM normalizado a minúsculas) |
| `custody_wallets.encrypted_private_key` | `address` (UNIQUE; EVM normalizado) |
| `two_factor_credentials.secret_encrypted` | `user_id` (PK) |
| `GAS_STATION_ENCRYPTED_PRIVATE_KEY` (env) | `env · <variable> · <address>` |

Se usa la **dirección** y no el id de fila porque el id lo genera el repositorio
*después* de cifrar; la dirección es única y ya se conoce al cifrar.

**Formato legacy** (`base64(nonce||ct)`, sin versión, sin key id, sin AAD): sigue
siendo legible. Como no lleva key id, se prueba con todas las claves conocidas
(la activa primero); GCM garantiza que una clave equivocada falla limpiamente.

## 2. Configuración multi-clave

```
CUSTODY_ENCRYPTION_KEYS=k1=<hex64>,k2=<hex64>
CUSTODY_ACTIVE_KEY_ID=k2
```

* `CUSTODY_ACTIVE_KEY_ID` es la **única** que cifra; el resto solo descifra.
* Ids: `[A-Za-z0-9_-]{1,32}`. Cada clave: 64 caracteres hex (32 bytes).
* Con varias claves, `CUSTODY_ACTIVE_KEY_ID` es obligatorio.
* Compatibilidad: si `CUSTODY_ENCRYPTION_KEYS` está vacío se usa
  `CUSTODY_ENCRYPTION_KEY` con id `default`. Si ya se escribieron blobs
  `v1:default:…`, al pasar a multi-clave **conserva esa clave bajo el id `default`**.
* El backend aborta el arranque ante configuración inválida. Los mensajes
  nombran ids, nunca bytes de clave.
* `TWO_FACTOR_ENCRYPTION_KEY` es una clave **aparte** (una sola, id `default`):
  recibe el formato v1 + AAD, pero no la rotación multi-clave de custodia.

## 3. Migración perezosa

Al descifrar un blob legacy o de una clave ya no activa, el backend lo
re-cifra (clave activa + AAD) y lo guarda con **compare-and-swap** sobre el
valor antiguo (nunca pisa a un escritor concurrente). Es *best-effort*: si falla
la escritura, el descifrado igual tiene éxito y solo se loguea el nombre de la
columna. Las columnas con migración perezosa: `crypto_addresses`,
`custody_wallets`, `two_factor_credentials`. La variable de entorno del gas
station no se puede reescribir sola: el arranque avisa si está en formato
legacy/clave no activa.

## 4. Procedimiento de rotación

1. **Backup** de la base de datos y verifica que se puede restaurar.
2. Genera la clave nueva: `openssl rand -hex 32` (en una máquina de confianza;
   nunca en un chat/ticket).
3. Despliega con ambas claves, la nueva activa:
   `CUSTODY_ENCRYPTION_KEYS=k1=<vieja>,k2=<nueva>` y `CUSTODY_ACTIVE_KEY_ID=k2`.
4. Dry-run (no escribe nada):
   ```
   docker run --rm --env-file <env-seguro> korzadi-wallet-tools:TAG custody-reencrypt
   ```
5. Revisa el informe (`por_migrar`, `fallidas`). Si hay `fallidas`, **no sigas**.
6. Aplica: `custody-reencrypt -apply` (idempotente y reanudable; cada fila se
   verifica antes de escribir).
7. Repite el dry-run: debe decir «Todo está en formato v1 con la clave activa».
8. Si el arranque avisó sobre `GAS_STATION_ENCRYPTED_PRIVATE_KEY`, re-cífrala
   **conservando la misma dirección** con
   `custody-reencrypt -gas-station-env` (lee `GAS_STATION_ADDRESS` y la variable
   actual, imprime solo el valor cifrado nuevo; no toca la BD). Sustitúyela en
   tu entorno seguro y redespliega. (`gasstation-keygen`, en cambio, genera una
   dirección nueva.)
9. Solo entonces retira `k1` de `CUSTODY_ENCRYPTION_KEYS` y redespliega.
10. Destruye la clave vieja según tu política.

> **Rollback:** los blobs `v1:` **no los lee la versión anterior del binario**.
> Antes de la migración, no despliegues sobre una versión sin este cambio.
> Si necesitas revertir tras migrar, restaura el backup (o despliega la versión
> actual, que lee ambos formatos).

## 5. Imágenes: producción vs herramientas

`backend/Dockerfile` tiene dos objetivos:

* `runtime` (**por defecto, última etapa**): API + `watcher-backfill`. **No** contiene
  `custody-keygen`, `gasstation-keygen` ni `custody-reencrypt`.
* `tools` (opt-in): las tres herramientas, en `/usr/local/bin`, etiquetada
  `org.korzadi.image-role=tools`. Nunca se despliega como servicio.

```
docker build -t korzadi-wallet-api:TAG backend/                      # producción
docker build --target tools -t korzadi-wallet-tools:TAG backend/     # operadores
docker run --rm -it --env-file <env-seguro> korzadi-wallet-tools:TAG custody-keygen -list
```

`docker-compose.yml` fija `target: runtime`. Verificación de una imagen publicada:

```
docker run --rm --entrypoint sh korzadi-wallet-api:TAG -c \
  'ls /custody-keygen /gasstation-keygen /custody-reencrypt /usr/local/bin 2>&1'   # debe decir "No such file"
docker image inspect korzadi-wallet-api:TAG --format '{{ index .Config.Labels "org.korzadi.image-role" }}'  # runtime
```

`internal/buildguard` (test) falla si `runtime` deja de ser la última etapa o si
alguna herramienta de claves aparece en `runtime`/`build`.

## 6. KeyProvider (KMS/HSM)

`cryptutil.KeyProvider` entrega **AEADs**, no bytes de clave, para que un
proveedor remoto haga el cifrado sin que la clave entre al proceso. Hoy existe
`EnvKeyProvider`. **KMS/HSM: BLOQUEADO POR DEPENDENCIA EXTERNA** (hay que elegir
proveedor, credenciales y política de acceso); cuando exista, solo hay que
implementar la interfaz y pasarla a `cryptutil.New(...)` — los consumidores no cambian.

## 7. Revisión de fugas (logs/errores)

* Revisados todos los `log.*`, `fmt.Errorf`, `%v/%+v` y respuestas HTTP: ninguna
  clave privada ni plaintext se imprime; los handlers no devuelven structs con
  columnas cifradas.
* **Corregido:** `evmwallet.privateKeyFromHex` propagaba el error de
  `crypto.HexToECDSA`, que puede incluir el carácter hex ofensor de la clave
  privada. Ahora devuelve un error estático (test de regresión incluido).
* **Corregido:** `cryptutil` ya no envuelve errores de `hex`/`aes` (también
  repetían bytes de entrada) y los de descifrado son sentinelas sin contenido.
* Límite conocido: los strings de Go son inmutables, el plaintext no se puede
  poner a cero en memoria (ya documentado en signer/gasstation/treasury).
