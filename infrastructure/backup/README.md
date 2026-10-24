# Backups fuera del VPS

Por defecto, los backups diarios de Postgres solo se guardan en el mismo servidor.
Esto protege contra corrupción de datos o error humano, pero NO protege si el VPS
entero se pierde. El servicio offsite-backup sincroniza a almacenamiento externo
(S3, Backblaze B2, etc.) cada 6 horas usando rclone. Inactivo hasta configurar.

## Activación
1. Crea cuenta en tu proveedor de almacenamiento preferido (ej. Backblaze B2)
2. Crea un bucket dedicado (ej. korzadi-wallet-backups)
3. Genera credenciales con permisos solo sobre ese bucket
4. Agrega a .env:

RCLONE_REMOTE_TYPE=s3
RCLONE_REMOTE_PROVIDER=Backblaze
RCLONE_ACCESS_KEY_ID=tu_access_key
RCLONE_SECRET_ACCESS_KEY=tu_secret_key
RCLONE_ENDPOINT=s3.us-west-002.backblazeb2.com
RCLONE_DESTINATION=korzadi-wallet-backups

5. docker compose up -d offsite-backup
6. docker compose logs offsite-backup --tail 20

## Verificación periódica
Restaura el backup más reciente en un entorno de prueba mensualmente.
