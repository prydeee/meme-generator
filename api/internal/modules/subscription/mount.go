package subscription

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewModule menyusun modul subscription dari pool database.
func NewModule(pool *pgxpool.Pool) *Handler {
	repo := NewPostgresRepository(pool)
	return NewHandler(NewService(repo, MockGateway{}))
}

func RegisterRoutes(mux *http.ServeMux, h *Handler, authMW func(http.Handler) http.Handler) {
	// Daftar paket publik agar bisa dilihat sebelum login/berlangganan.
	mux.HandleFunc("GET /api/v1/subscription/plans", h.ListPlans)
	// BUG: /status berisi data per-user tapi diberi header cache publik —
	// CDN/browser bisa menyajikan status user lain / status basi sehari penuh.
	mux.Handle("GET /api/v1/subscription/status", authMW(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "private, no-store")
			h.Status(w, r)
		})))
	mux.Handle("POST /api/v1/subscription/checkout", authMW(http.HandlerFunc(h.Checkout)))
	// Endpoint mock payment gateway (di produksi: webhook dengan verifikasi signature).
	mux.HandleFunc("POST /api/v1/subscription/payments/{reference}/confirm", h.Confirm)
}
