package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	p2pd "korzadi-wallet/backend/internal/domain/p2p"
	"korzadi-wallet/backend/internal/http/middleware"
	"korzadi-wallet/backend/internal/infrastructure/mailer"
)

const publicCacheTTL = 15 * time.Second

// ttlCache es una caché en memoria mínima para las respuestas públicas.
type ttlCache struct {
	mu    sync.Mutex
	items map[string]cacheEntry
}

type cacheEntry struct {
	body    []byte
	expires time.Time
}

func newTTLCache() *ttlCache {
	return &ttlCache{items: make(map[string]cacheEntry)}
}

func (c *ttlCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok || time.Now().After(entry.expires) {
		if ok {
			delete(c.items, key)
		}
		return nil, false
	}
	return entry.body, true
}

func (c *ttlCache) set(key string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = cacheEntry{body: body, expires: time.Now().Add(publicCacheTTL)}
}

// Public endpoints mínimos, sin autenticación, solo datos agregados.
func registerPublicRoutes(r *gin.Engine, cfg Config, h Handlers) {
	cache := newTTLCache()
	public := r.Group("/api/public")

	getLimiters := []gin.HandlerFunc{}
	if cfg.RedisClient != nil {
		getLimiters = append(getLimiters, middleware.NewRedisRateLimiter(cfg.RedisClient, 60, time.Minute).Middleware())
	}

	get := func(path string, handler gin.HandlerFunc) {
		handlers := append([]gin.HandlerFunc{}, getLimiters...)
		handlers = append(handlers, handler)
		public.GET(path, handlers...)
	}

	get("/stats", func(c *gin.Context) { publicStatsHandler(c, cache, h) })
	get("/rates", func(c *gin.Context) { publicRatesHandler(c, cache, h) })
	get("/status", func(c *gin.Context) { publicStatusHandler(c, cache, h) })

	contactHandlers := []gin.HandlerFunc{}
	if cfg.RedisClient != nil {
		contactHandlers = append(contactHandlers, middleware.NewRedisRateLimiter(cfg.RedisClient, 5, time.Minute).Middleware())
	}
	contactHandlers = append(contactHandlers, func(c *gin.Context) { publicContactHandler(c, h) })
	public.POST("/contact", contactHandlers...)
}

func writeCachedJSON(c *gin.Context, cache *ttlCache, key string, build func() (any, error)) {
	c.Header("Cache-Control", "public, max-age=15")
	if body, ok := cache.get(key); ok {
		c.Data(http.StatusOK, "application/json; charset=utf-8", body)
		return
	}
	payload, err := build()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "servicio_no_disponible"})
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error_interno"})
		return
	}
	cache.set(key, body)
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

// publicStatsHandler devuelve métricas agregadas reales. No incluye datos
// personales: solo conteos y sumas.
func publicStatsHandler(c *gin.Context, cache *ttlCache, h Handlers) {
	writeCachedJSON(c, cache, "stats", func() (any, error) {
		if h.DB == nil {
			return nil, fmt.Errorf("db no disponible")
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		const q = `
			SELECT
				(SELECT count(*) FROM users u
				   WHERE u.status <> 'deleted'
				     AND u.id <> '00000000-0000-0000-0000-000000000001'
				     AND NOT EXISTS (
				       SELECT 1 FROM user_roles ur
				       JOIN roles r ON r.id = ur.role_id
				       WHERE ur.user_id = u.id AND r.name IN ('admin','super_admin')
				     )),
				COALESCE((
				  SELECT sum(CASE WHEN asset_code = 'USDT' THEN amount ELSE fiat_amount END)
				  FROM p2p_orders
				  WHERE status = 'released'
				    AND released_at >= now() - interval '24 hours'
				    AND (asset_code = 'USDT' OR upper(fiat_currency) = 'USD')
				), 0)::float8,
				(SELECT count(*) FROM p2p_orders WHERE status = 'released'),
				COALESCE((
				  SELECT 100.0 * count(*) FILTER (
				           WHERE status IN ('resolved_release','resolved_refund'))
				         / NULLIF(count(*), 0)
				  FROM p2p_disputes
				  WHERE status IN ('resolved_release','resolved_refund','closed')
				), 0)::float8
		`

		var users, orders int64
		var volume, disputesPct float64
		if err := h.DB.QueryRow(ctx, q).Scan(&users, &volume, &orders, &disputesPct); err != nil {
			return nil, err
		}
		return gin.H{
			"users":               users,
			"volume24h":           volume,
			"orders":              orders,
			"disputesResolvedPct": disputesPct,
		}, nil
	})
}

// publicRatesHandler calcula cuántas unidades de cada moneda fiat equivalen a
// 1 USDT usando la mediana de PricePerUnit de ofertas P2P activas con saldo
// disponible. Cada moneda se agrupa por FiatCurrency; las monedas sin ofertas
// se omiten. USDT siempre vale 1. No se inventan valores.
func publicRatesHandler(c *gin.Context, cache *ttlCache, h Handlers) {
	writeCachedJSON(c, cache, "rates", func() (any, error) {
		rates := map[string]float64{"USDT": 1}
		updatedAt := time.Now().UTC().Format(time.RFC3339)

		if h.P2P != nil && h.P2P.Service() != nil {
			offers, err := h.P2P.Service().ListOffers(c.Request.Context(), "USDT", "", "", 100, 0)
			if err != nil {
				return nil, err
			}
			byFiat := map[string][]float64{}
			for _, o := range offers {
				if o.Status != p2pd.OfferActive {
					continue
				}
				if !o.AvailableAmount.GreaterThan(decimal.Zero) {
					continue
				}
				if o.PricePerUnit.LessThanOrEqual(decimal.Zero) {
					continue
				}
				fiat := strings.ToUpper(strings.TrimSpace(o.FiatCurrency))
				if fiat == "" || fiat == "USDT" {
					continue
				}
				if v, _ := o.PricePerUnit.Float64(); v > 0 {
					byFiat[fiat] = append(byFiat[fiat], v)
				}
			}
			for fiat, values := range byFiat {
				if len(values) == 0 {
					continue
				}
				sort.Float64s(values)
				n := len(values)
				if n%2 == 1 {
					rates[fiat] = values[n/2]
				} else {
					rates[fiat] = (values[n/2-1] + values[n/2]) / 2
				}
			}
		}

		return gin.H{
			"rates":     rates,
			"updatedAt": updatedAt,
		}, nil
	})
}

// publicStatusHandler devuelve estados a partir de comprobaciones reales.
// No incluye "uptime" porque no existe historial de disponibilidad.
func publicStatusHandler(c *gin.Context, cache *ttlCache, h Handlers) {
	writeCachedJSON(c, cache, "status", func() (any, error) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 4*time.Second)
		defer cancel()

		services := []map[string]any{{"name": "API", "state": "Operativo"}}

		dbState := checkDB(ctx, h)
		services = append(services, map[string]any{"name": "Depósitos", "state": dbState})

		retirosState := dbState
		if h.Redis != nil {
			if state := checkRedis(ctx, h.Redis); state == "Interrumpido" {
				retirosState = "Interrumpido"
			} else if state == "Latencia alta" && retirosState == "Operativo" {
				retirosState = "Latencia alta"
			}
		}
		services = append(services, map[string]any{"name": "Retiros", "state": retirosState})

		services = append(services, map[string]any{"name": "Mercado P2P", "state": checkP2P(ctx, h)})
		services = append(services, map[string]any{"name": "Nodo TRON", "state": checkHealthFn(ctx, h.HealthTRON)})
		services = append(services, map[string]any{"name": "Nodo BSC", "state": checkHealthFn(ctx, h.HealthBSC)})

		return gin.H{"services": services}, nil
	})
}

func checkDB(ctx context.Context, h Handlers) string {
	if h.DB == nil {
		return "Interrumpido"
	}
	start := time.Now()
	if err := h.DB.Ping(ctx); err != nil {
		return "Interrumpido"
	}
	if time.Since(start) > 400*time.Millisecond {
		return "Latencia alta"
	}
	return "Operativo"
}

func checkRedis(ctx context.Context, client *redis.Client) string {
	if client == nil {
		return "Interrumpido"
	}
	start := time.Now()
	if err := client.Ping(ctx).Err(); err != nil {
		return "Interrumpido"
	}
	if time.Since(start) > 400*time.Millisecond {
		return "Latencia alta"
	}
	return "Operativo"
}

func checkP2P(ctx context.Context, h Handlers) string {
	if h.DB == nil {
		return "Interrumpido"
	}
	start := time.Now()
	var exists bool
	if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM p2p_offers)`).Scan(&exists); err != nil {
		return "Interrumpido"
	}
	if time.Since(start) > 400*time.Millisecond {
		return "Latencia alta"
	}
	return "Operativo"
}

func checkHealthFn(ctx context.Context, fn func(context.Context) error) string {
	if fn == nil {
		return "Interrumpido"
	}
	start := time.Now()
	if err := fn(ctx); err != nil {
		return "Interrumpido"
	}
	if time.Since(start) > 800*time.Millisecond {
		return "Latencia alta"
	}
	return "Operativo"
}

// publicContactHandler valida, aplica anti-spam, persiste el ticket y avisa por
// correo solo si SMTP y SUPPORT_EMAIL están configurados. Nunca expone
// credenciales ni datos personales en la respuesta.
func publicContactHandler(c *gin.Context, h Handlers) {
	var payload struct {
		Name    string `json:"name"`
		Email   string `json:"email"`
		Subject string `json:"subject"`
		Message string `json:"message"`
		Website string `json:"website"` // campo señuelo anti-spam: debe quedar vacío
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "payload_invalido"})
		return
	}

	payload.Name = strings.TrimSpace(payload.Name)
	payload.Email = strings.TrimSpace(payload.Email)
	payload.Subject = strings.TrimSpace(payload.Subject)
	payload.Message = strings.TrimSpace(payload.Message)

	if payload.Website != "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "spam_detected"})
		return
	}
	if len(payload.Name) < 2 || len(payload.Name) > 120 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nombre_invalido"})
		return
	}
	if len(payload.Email) < 5 || len(payload.Email) > 255 || !validEmail(payload.Email) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "correo_invalido"})
		return
	}
	if len(payload.Subject) < 3 || len(payload.Subject) > 120 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "asunto_invalido"})
		return
	}
	if len(payload.Message) < 10 || len(payload.Message) > 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mensaje_invalido"})
		return
	}
	if h.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "servicio_no_disponible"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var id int64
	err := h.DB.QueryRow(ctx,
		`INSERT INTO support_tickets (name, email, subject, message, ip, user_agent)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		payload.Name, payload.Email, payload.Subject, payload.Message,
		c.ClientIP(), c.Request.UserAgent(),
	).Scan(&id)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no_se_pudo_guardar"})
		return
	}

	ticket := fmt.Sprintf("KZ-%04d", id)
	notifySupport(ticket, payload.Name, payload.Email, payload.Subject, payload.Message)
	c.JSON(http.StatusOK, gin.H{"ticket": ticket})
}

func validEmail(value string) bool {
	addr, err := mail.ParseAddress(value)
	if err != nil {
		return false
	}
	return addr.Address == value && strings.Contains(value, "@")
}

// notifySupport envía el aviso en segundo plano si hay SMTP y destinatario.
// Sin SUPPORT_EMAIL no se inventa un destino: solo se registra el ticket.
func notifySupport(ticket, name, email, subject, message string) {
	sender, configured, err := mailer.NewFromEnv()
	if err != nil || !configured {
		return
	}
	to := strings.TrimSpace(os.Getenv("SUPPORT_EMAIL"))
	if to == "" {
		return
	}
	clean := func(s string) string {
		return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
	}
	body := fmt.Sprintf("Ticket: %s\nNombre: %s\nCorreo: %s\nAsunto: %s\n\n%s\n",
		ticket, clean(name), clean(email), clean(subject), message)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = sender.Send(ctx, to, "Nuevo ticket "+ticket+": "+clean(subject), body)
	}()
}
