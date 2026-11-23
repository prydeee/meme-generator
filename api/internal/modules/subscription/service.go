package subscription

import (
	"context"
	"fmt"
	"time"

	"github.com/nyxsr/meme-portal/api/internal/platform/token"
)

// PaymentGateway abstraksi payment provider; saat ini mock.
type PaymentGateway interface {
	CreateCharge(ctx context.Context, amount int64, reference string) (checkoutURL string, err error)
}

// MockGateway mensimulasikan payment gateway: charge langsung "berhasil dibuat"
// dan konfirmasi dilakukan lewat endpoint confirm milik API sendiri.
type MockGateway struct{}

func (MockGateway) CreateCharge(_ context.Context, _ int64, reference string) (string, error) {
	return "/api/v1/subscription/payments/" + reference + "/confirm", nil
}

type Service struct {
	repo    *Repository
	gateway PaymentGateway
}

func NewService(repo *Repository, gateway PaymentGateway) *Service {
	return &Service{repo: repo, gateway: gateway}
}

func (s *Service) ListPlans(ctx context.Context) ([]Plan, error) {
	return s.repo.ListPlans(ctx)
}

// Checkout membuat checkout baru; jika idempotencyKey pernah dipakai,
// checkout yang sama dikembalikan (replay) tanpa membuat baris baru.
func (s *Service) Checkout(ctx context.Context, userID string, dto CheckoutDTO, idempotencyKey string) (CheckoutResponse, error) {
	if prev, ok, err := s.repo.CheckoutByIdempotencyKey(ctx, userID, idempotencyKey); err != nil {
		return CheckoutResponse{}, err
	} else if ok {
		return prev, nil
	}

	plan, err := s.repo.GetPlanByCode(ctx, dto.PlanCode)
	if err != nil {
		return CheckoutResponse{}, err
	}
	reference := token.RandomHex(12)
	sub, pay, created, err := s.repo.CreateSubscription(ctx, userID, plan, reference, idempotencyKey)
	if err != nil {
		return CheckoutResponse{}, err
	}
	if !created {
		return CheckoutResponse{
			Subscription:   sub,
			Payment:        pay,
			MockGatewayURL: "/api/v1/subscription/payments/" + pay.Reference + "/confirm",
		}, nil
	}
	url, err := s.gateway.CreateCharge(ctx, pay.AmountIDR, reference)
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("subscription service: create charge gagal: %w", err)
	}
	return CheckoutResponse{Subscription: sub, Payment: pay, MockGatewayURL: url}, nil
}

// Confirm mensimulasikan callback payment gateway sukses.
func (s *Service) Confirm(ctx context.Context, reference string) (Subscription, Payment, error) {
	return s.repo.ConfirmPayment(ctx, reference)
}

// MarkExpired menandai langganan aktif yang sudah lewat masa aktif
// (dipanggil janitor periodik).
func (s *Service) MarkExpired(ctx context.Context) (int64, error) {
	return s.repo.MarkExpired(ctx)
}

func (s *Service) Status(ctx context.Context, userID string) (SubscriptionStatus, error) {
	sub, plan, err := s.repo.ActiveByUser(ctx, userID)
	if err == nil {
		used, err := s.repo.MemesUsed(ctx, userID, time.Now().AddDate(0, 0, -int(plan.Duration)))
		if err != nil {
			return SubscriptionStatus{}, err
		}
		var endsAt *string
		if sub.EndsAt != nil {
			s := sub.EndsAt.UTC().Format(time.RFC3339)
			endsAt = &s
		}
		return SubscriptionStatus{
			Subscribed: true,
			Status:     sub.Status,
			PlanCode:   plan.Code,
			PlanName:   plan.Name,
			EndsAt:     endsAt,
			MemesUsed:  used,
			MaxMemes:   plan.MaxMemes,
		}, nil
	}
	return SubscriptionStatus{Subscribed: false, MemesUsed: 0, MaxMemes: 0}, nil
}
