package redis

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// incrExpire hace INCR + PEXPIRE de forma ATÓMICA en el servidor. Además de
// fijar el TTL en el primer incremento, lo repone si la clave existe sin TTL
// (p. ej. por un INCR+EXPIRE no atómico anterior que se cortó entre ambos),
// evitando un bloqueo permanente.
var incrExpire = redis.NewScript(`
local c = redis.call('INCR', KEYS[1])
if c == 1 or redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return c
`)

// AtomicIncr incrementa key y garantiza que tenga TTL de window. Devuelve el
// nuevo contador. Un error de Redis se propaga: el llamador decide si falla
// abierto o cerrado.
func AtomicIncr(ctx context.Context, c redis.Scripter, key string, window time.Duration) (int64, error) {
	ms := window.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	return incrExpire.Run(ctx, c, []string{key}, ms).Int64()
}
