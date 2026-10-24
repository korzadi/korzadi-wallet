# Fase TRON: depósitos y retiros TRC-20 (USDT)

Todo se probó contra un **nodo TRON simulado en memoria** (`tronclient/tronfake`).
**Nada se ha ejecutado contra mainnet ni contra un nodo real.** Ver «BLOQUEADO».

## Qué se reutilizó (sin duplicar)

| Pieza existente | Uso en TRON |
|---|---|
| `tronclient.NodeRealClient` | `FullNode` lo embebe (misma URL/HTTP, `GetTRC20Balance`) |
| `tronwallet` (claves/direcciones) | + construcción/firma/lectura de tx (`tx.go`) |
| `watcher.EVMDepositWatcher` | **sin cambios**: `tronclient.Provider` implementa `domain.BlockchainProvider` |
| `blockchain_transfers`, `BlockchainDepositService`, idempotencia | los mismos |
| `blockchainwithdrawal` (máquina de estados, ledger, límites, reservas, persistir-antes-de-enviar, confirmar/liquidar) | la misma; TRON solo aporta `ResourceChain` + `TronSigner` |
| `gasstation` (política, `planRefill`, `gas_station_refills`) | `TronService` |
| `fee` (engine, precios) | `TronFeeEstimator` + `MultiNetworkFeeEstimator` |
| asset registry | `LoadTronNetworkAssets` |

## Diseño y decisiones de seguridad

* **La tx se construye y firma localmente.** No se le pide al nodo (`triggersmartcontract`):
  un nodo comprometido podría devolver una tx que pague a otra dirección. Tras firmar,
  `verifySignedTron` decodifica y comprueba origen, contrato, destino, monto, tiempos,
  `fee_limit` y que el firmante recuperado **es** la dirección de origen.
* **Sin nonce.** La columna `nonce` guarda el bloque de referencia TaPoS; el lease de
  `evm_nonces` no se usa. La unicidad la da la tx firmada persistida *antes* de transmitir.
* **Expiración en lugar de nonce.** Una tx TRON caduca (`TRON_*`: ventana 5 min). Si la
  hora de la cadena supera `expiración + margen` y el nodo no la conoce ni tiene receipt,
  **jamás podrá minarse** → se descarta (`expired_resign`) y se firma otra con bloque de
  referencia nuevo. Antes de eso se reenvían **los mismos bytes** (mismo txid). Un
  broadcast ambiguo nunca se trata como fallo. Transición añadida: `broadcasted → processing`.
* **Energy/Bandwidth.** `ResourceQuote`: energía estimada (`triggerconstantcontract`) +20 %,
  precios en vivo (`getchainparameters`), recursos de la HOT (`getaccountresource`). Si el
  stake no cubre, se quema TRX de la HOT; si le falta TRX, la **gas station** repone solo el
  faltante (límites `min/target/max_refill` de la política; fuera de política = no se firma).
  `fee_limit` tiene tope duro (`TRON_MAX_FEE_LIMIT_SUN`, 100 TRX por defecto).
* Cotización al usuario = **peor caso sin stake** (nunca se cobra de menos). Si el costo real
  supera lo cotizado + `MaxNetworkFeeOverrunBps`, el retiro falla antes de firmar y libera fondos.
* Solo **TRC-20** en retiros; TRX es combustible (rechazado en `Create`).
* Contrato USDT: al arrancar se verifica con `VerifyUSDTContract`. **mainnet**: solo el
  oficial de Tether; **testnet**: debe estar en `TRON_USDT_TESTNET_ALLOWLIST`.
* Ninguna clave/plaintext en errores: `SignRaw`/`HexToECDSA` no propagan errores que
  repitan caracteres de la clave (test de regresión).

## Variables de entorno

| Variable | Defecto | Descripción |
|---|---|---|
| `TRON_NETWORK` | `testnet` | `mainnet` \| `testnet` (ya existía) |
| `TRON_USDT_CONTRACT` | — | obligatorio; **se valida en cada arranque** |
| `TRON_USDT_TESTNET_ALLOWLIST` | vacío | **requerido en testnet**: contratos permitidos, separados por coma |
| `TRON_NODEREAL_URL` | — | nodo completo (ya existía) |
| `TRON_DEPOSIT_WATCHER_ENABLED` | `false` | registra TRON en el registry y en `activeDepositNetworks`, arranca el watcher |
| `TRON_WITHDRAWALS_ENABLED` | `false` | exige el watcher activo; en mainnet exige `WITHDRAWALS_MAINNET_ENABLED` |
| `TRON_CONFIRMATIONS_REQUIRED` | `20` | depósitos y retiros |
| `TRON_MAX_FEE_LIMIT_SUN` | `100000000` | tope de `fee_limit` |
| `TRON_FEE_ENERGY_UNITS` / `TRON_FEE_BANDWIDTH_BYTES` | `78000` / `400` | volumen típico para la cotización |

## Puesta en marcha (orden recomendado)

1. Añade `TRON_USDT_TESTNET_ALLOWLIST=<tu contrato actual>` (si usas testnet) o confirma que
   `TRON_USDT_CONTRACT` es el oficial (mainnet). **Sin esto el backend no arranca.**
2. Despliega con ambos flags en `false`; la migración `0021` crea el asset `TRX` (deshabilitado).
3. `TRON_DEPOSIT_WATCHER_ENABLED=true` en testnet; verifica depósitos con un nodo Nile/Shasta.
4. Registra la wallet HOT de TRON (`custody-keygen`, red TRON, asset USDT) y una wallet GAS (asset TRX)
   con su política; fondea la GAS con TRX.
5. `TRON_WITHDRAWALS_ENABLED=true` y prueba retiros pequeños.

## BLOQUEADO (requiere un nodo real o una decisión externa)

* Forma exacta de `gettransactioninfobyid` para transferencias TRX (`SuccessNative` asume que no hay
  `receipt.result`), visibilidad de txs **pendientes** (`gettransactionbyid`), disponibilidad de
  `/wallet/getblock`, y los códigos/mensajes de `broadcasthex`: modelados, no confirmados.
* Delegación de Energy/Bandwidth desde la GAS (`DelegateResourceContract`): **no implementada**
  (hoy la gas station solo repone TRX para quemar).
* Constantes de testnet (Nile/Shasta): no se hardcodean; van en `TRON_USDT_TESTNET_ALLOWLIST`.
* Sweeps/treasury/reconciliación para TRON: fuera de alcance (el runtime TRON no entra en esos mapas).

## Riesgos y límites conocidos

* Sin nonce, la **única** protección contra doble pago al re-firmar es la expiración: por eso el margen
  (`ExpirySafetyMargin`, 2 min) y que la hora usada sea la de la **cadena**, no la local. No lo reduzcas.
* La tx de top-up de la gas station no persiste los bytes firmados (solo el hash); si el envío es ambiguo
  se reconcilia o se descarta al expirar.
* El estimador de energía usa `triggerconstantcontract` desde la HOT: falla si la HOT no tiene el USDT.
