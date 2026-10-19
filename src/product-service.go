// Product API (Products section of api.yaml) built with Gin.
//
//	GET  /products/{productId}          -> 200 Product | 404 Error | 500 Error
//	POST /products/{productId}/details  -> 204         | 400 Error | 404 Error | 500 Error
//
// Extras for operations (not part of api.yaml):
//
//	GET /health   -> liveness check
//	GET /metrics  -> Prometheus metrics (RED: rate, errors, duration)
//
// Every request also produces one structured JSON log line on stdout.
//
// Environment variables:
//
//	PORT           listen port (default 8080, must match container_port in terraform/variables.tf)
//	SEED_PRODUCTS  number of products created at startup with IDs 1..N (default 1000)
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ---------- schemas (components/schemas in api.yaml) ----------

// Product is the stored and returned representation.
type Product struct {
	ProductID    int32  `json:"product_id"`
	SKU          string `json:"sku"`
	Manufacturer string `json:"manufacturer"`
	CategoryID   int32  `json:"category_id"`
	Weight       int32  `json:"weight"`
	SomeOtherID  int32  `json:"some_other_id"`
}

// productInput uses pointers so a missing field can be told apart from a zero value
// (weight: 0 is valid, a missing weight is not).
type productInput struct {
	ProductID    *int64  `json:"product_id"`
	SKU          *string `json:"sku"`
	Manufacturer *string `json:"manufacturer"`
	CategoryID   *int64  `json:"category_id"`
	Weight       *int64  `json:"weight"`
	SomeOtherID  *int64  `json:"some_other_id"`
}

// APIError matches the Error schema: error and message are required, details is optional.
type APIError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// ---------- in-memory storage ----------

// ProductStore is a map guarded by a RWMutex: many GETs can read at once,
// while a POST takes the exclusive lock.
type ProductStore struct {
	mu       sync.RWMutex
	products map[int32]Product
}

func NewProductStore() *ProductStore {
	return &ProductStore{products: make(map[int32]Product)}
}

func (s *ProductStore) Get(id int32) (Product, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.products[id]
	return p, ok
}

// UpdateIfExists does the existence check and the write under one lock,
// so a concurrent request can never slip in between the check and the write.
func (s *ProductStore) UpdateIfExists(p Product) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.products[p.ProductID]; !ok {
		return false
	}
	s.products[p.ProductID] = p
	return true
}

func (s *ProductStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.products)
}

// Seed creates products 1..n. api.yaml has no "create product" endpoint and
// POST /details returns 404 for unknown products, so the catalog is pre-loaded.
func (s *ProductStore) Seed(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 1; i <= n; i++ {
		id := int32(i)
		s.products[id] = Product{
			ProductID:    id,
			SKU:          fmt.Sprintf("SKU-%05d", i),
			Manufacturer: "Acme Corporation",
			CategoryID:   int32(i%50 + 1),
			Weight:       int32(100 + i%5000),
			SomeOtherID:  id,
		}
	}
}

// ---------- validation ----------

const maxBodyBytes = 1 << 20 // 1 MB

func parseProductID(raw string) (int32, error) {
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("productId must be an integer between 1 and %d, got %q", math.MaxInt32, raw)
	}
	if n < 1 {
		return 0, fmt.Errorf("productId must be >= 1, got %d", n)
	}
	return int32(n), nil
}

func requireInt(problems *[]string, name string, v *int64, min int64) int32 {
	if v == nil {
		*problems = append(*problems, name+" is required")
		return 0
	}
	if *v < min || *v > math.MaxInt32 {
		*problems = append(*problems, fmt.Sprintf("%s must be between %d and %d", name, min, math.MaxInt32))
		return 0
	}
	return int32(*v)
}

func requireString(problems *[]string, name string, v *string, minLen, maxLen int) string {
	if v == nil {
		*problems = append(*problems, name+" is required")
		return ""
	}
	if n := utf8.RuneCountInString(*v); n < minLen || n > maxLen {
		*problems = append(*problems, fmt.Sprintf("%s length must be between %d and %d characters", name, minLen, maxLen))
		return ""
	}
	return *v
}

// decodeProduct parses the request body and checks every rule of the Product schema.
func decodeProduct(body io.Reader) (Product, []string) {
	dec := json.NewDecoder(io.LimitReader(body, maxBodyBytes))
	var in productInput
	if err := dec.Decode(&in); err != nil {
		if errors.Is(err, io.EOF) {
			return Product{}, []string{"request body is required"}
		}
		return Product{}, []string{"request body must be a JSON object matching the Product schema: " + err.Error()}
	}
	if dec.More() {
		return Product{}, []string{"request body must contain exactly one JSON object"}
	}

	var problems []string
	p := Product{
		ProductID:    requireInt(&problems, "product_id", in.ProductID, 1),
		SKU:          requireString(&problems, "sku", in.SKU, 1, 100),
		Manufacturer: requireString(&problems, "manufacturer", in.Manufacturer, 1, 200),
		CategoryID:   requireInt(&problems, "category_id", in.CategoryID, 1),
		Weight:       requireInt(&problems, "weight", in.Weight, 0),
		SomeOtherID:  requireInt(&problems, "some_other_id", in.SomeOtherID, 1),
	}
	return p, problems
}

// maybeSimulateFailure lets us trigger the 500 response on demand for testing:
// a request with header "X-Simulate-Error: true" panics, and the recovery
// middleware turns the panic into a 500 Error response.
func maybeSimulateFailure(c *gin.Context) {
	if strings.EqualFold(c.GetHeader("X-Simulate-Error"), "true") {
		panic("simulated internal failure (X-Simulate-Error header)")
	}
}

// ---------- handlers ----------

type handler struct {
	store *ProductStore
}

// GET /products/{productId}
func (h *handler) getProduct(c *gin.Context) {
	maybeSimulateFailure(c)

	id, err := parseProductID(c.Param("productId"))
	if err != nil {
		// api.yaml defines only 200/404/500 for this endpoint, so an ID that
		// can never exist is reported as "not found".
		c.JSON(http.StatusNotFound, APIError{"PRODUCT_NOT_FOUND", "Product not found", err.Error()})
		return
	}
	p, ok := h.store.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, APIError{"PRODUCT_NOT_FOUND", "Product not found", fmt.Sprintf("no product with id %d", id)})
		return
	}
	c.JSON(http.StatusOK, p)
}

// POST /products/{productId}/details
func (h *handler) addProductDetails(c *gin.Context) {
	maybeSimulateFailure(c)

	id, err := parseProductID(c.Param("productId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, APIError{"INVALID_INPUT", "Invalid product ID", err.Error()})
		return
	}
	p, problems := decodeProduct(c.Request.Body)
	if len(problems) > 0 {
		c.JSON(http.StatusBadRequest, APIError{"INVALID_INPUT", "The provided input data is invalid", strings.Join(problems, "; ")})
		return
	}
	if p.ProductID != id {
		c.JSON(http.StatusBadRequest, APIError{"INVALID_INPUT", "The provided input data is invalid",
			fmt.Sprintf("body product_id (%d) does not match path productId (%d)", p.ProductID, id)})
		return
	}
	if !h.store.UpdateIfExists(p) {
		c.JSON(http.StatusNotFound, APIError{"PRODUCT_NOT_FOUND", "Product not found", fmt.Sprintf("no product with id %d", id)})
		return
	}
	c.Status(http.StatusNoContent)
}

// ---------- observability: RED metrics + one JSON log line per request ----------

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests (Rate).",
	}, []string{"method", "route", "status"})

	httpErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_request_errors_total",
		Help: "HTTP requests that returned a non-2xx status (Errors).",
	}, []string{"method", "route", "status"})

	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency in seconds (Duration).",
		Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
	}, []string{"method", "route"})
)

func observability(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath() // the route pattern, e.g. /products/:productId (keeps label cardinality low)
		if route == "" {
			route = "unmatched"
		}
		if route == "/metrics" {
			return // don't measure the Prometheus scrapes themselves
		}
		status := c.Writer.Status()
		elapsed := time.Since(start)
		code := strconv.Itoa(status)

		httpRequests.WithLabelValues(c.Request.Method, route, code).Inc()
		if status < 200 || status > 299 {
			httpErrors.WithLabelValues(c.Request.Method, route, code).Inc()
		}
		httpDuration.WithLabelValues(c.Request.Method, route).Observe(elapsed.Seconds())

		logger.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"route", route,
			"status", status,
			"duration_ms", float64(elapsed.Microseconds())/1000,
			"client_ip", c.ClientIP(),
		)
	}
}

// ---------- main ----------

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v >= 0 {
		return v
	}
	return def
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	gin.SetMode(gin.ReleaseMode)

	store := NewProductStore()
	seeded := envInt("SEED_PRODUCTS", 1000)
	store.Seed(seeded)
	h := &handler{store: store}

	r := gin.New()
	r.Use(observability(logger)) // outermost: sees the final status, including 500s from panics
	r.Use(gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, rec any) {
		logger.Error("panic recovered", "error", fmt.Sprint(rec), "path", c.Request.URL.Path)
		c.AbortWithStatusJSON(http.StatusInternalServerError,
			APIError{"INTERNAL_ERROR", "Internal server error", "an unexpected error occurred"})
	}))

	r.GET("/products/:productId", h.getProduct)
	r.POST("/products/:productId/details", h.addProductDetails)
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "products": store.Len()})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, APIError{"NOT_FOUND", "Route not found", c.Request.Method + " " + c.Request.URL.Path})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	logger.Info("starting product service", "port", port, "seeded_products", seeded)
	if err := r.Run(":" + port); err != nil {
		logger.Error("server stopped", "error", err.Error())
		os.Exit(1)
	}
}
