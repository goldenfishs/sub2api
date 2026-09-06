package service

import (
	"context"
	"log"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrWeeklyAdvanceUnavailable = infraerrors.BadRequest("WEEKLY_ADVANCE_UNAVAILABLE", "weekly quota cannot be advanced for this subscription")
	ErrWeeklyAdvanceStale       = infraerrors.Conflict("WEEKLY_ADVANCE_STALE", "subscription changed; refresh the preview before confirming")
)

// WeeklyAdvancePreview carries the observed state for optimistic confirmation.
// The server rechecks this state under a row lock before changing the quota.
type WeeklyAdvancePreview struct {
	SubscriptionID    int64     `json:"subscription_id"`
	WeeklyWindowStart time.Time `json:"weekly_window_start"`
	ExpiresAt         time.Time `json:"expires_at"`
	NewExpiresAt      time.Time `json:"new_expires_at"`
	DeductSeconds     int64     `json:"deduct_seconds"`
}

func weeklyAdvancePreview(sub *UserSubscription, group *Group, now time.Time) (*WeeklyAdvancePreview, error) {
	if sub.Status != SubscriptionStatusActive || now.Before(sub.StartsAt) || !now.Before(sub.ExpiresAt) || group == nil || !group.IsSubscriptionType() || !group.HasWeeklyLimit() || sub.WeeklyWindowStart == nil || sub.WeeklyUsageUSD <= 0 {
		return nil, ErrWeeklyAdvanceUnavailable
	}
	resetAt := sub.WeeklyResetTime()
	if !now.Before(*resetAt) || !sub.ExpiresAt.After(*resetAt) || now.Before(*sub.WeeklyWindowStart) {
		return nil, ErrWeeklyAdvanceUnavailable
	}
	remaining := resetAt.Sub(now)
	return &WeeklyAdvancePreview{SubscriptionID: sub.ID, WeeklyWindowStart: *sub.WeeklyWindowStart, ExpiresAt: sub.ExpiresAt, NewExpiresAt: sub.ExpiresAt.Add(-remaining), DeductSeconds: int64((remaining + time.Second - 1) / time.Second)}, nil
}

func (s *SubscriptionService) PreviewAdvanceWeek(ctx context.Context, userID, id int64) (*WeeklyAdvancePreview, error) {
	sub, err := s.userSubRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if sub.UserID != userID {
		return nil, ErrSubscriptionNotFound
	}
	group, err := s.groupRepo.GetByID(ctx, sub.GroupID)
	if err != nil {
		return nil, err
	}
	return weeklyAdvancePreview(sub, group, s.now())
}

func (s *SubscriptionService) AdvanceWeek(ctx context.Context, userID, id int64, expectedStart, expectedExpiry time.Time) (*WeeklyAdvancePreview, error) {
	return s.advanceWeek(ctx, userID, id, expectedStart, expectedExpiry, false)
}

// Spend the same subscription time on the monthly clock. Usage is retained
// until the advanced clock crosses a full monthly boundary before expiry.
func monthlyWindowAfterWeeklyAdvance(sub *UserSubscription, newExpiry, now time.Time) (*time.Time, float64) {
	if sub.MonthlyWindowStart == nil {
		return nil, sub.MonthlyUsageUSD
	}
	start := sub.windowResetAnchor(*sub.MonthlyWindowStart).Add(newExpiry.Sub(sub.ExpiresAt))
	effective := *sub
	effective.MonthlyWindowStart = &start
	effective.ExpiresAt = newExpiry
	if next, ok := effective.automaticWindowStartAt(&start, 30*24*time.Hour, now); ok {
		return &next, 0
	}
	return &start, sub.MonthlyUsageUSD
}

func (s *SubscriptionService) advanceWeek(ctx context.Context, userID, id int64, expectedStart, expectedExpiry time.Time, automatic bool) (*WeeklyAdvancePreview, error) {
	var result *WeeklyAdvancePreview
	var groupID int64
	err := s.withSubscriptionUpdateTx(ctx, func(txCtx context.Context) error {
		sub, err := s.userSubRepo.GetByIDForUpdate(txCtx, id)
		if err != nil {
			return err
		}
		if sub.UserID != userID {
			return ErrSubscriptionNotFound
		}
		if sub.WeeklyWindowStart == nil || !sub.WeeklyWindowStart.Equal(expectedStart) || !sub.ExpiresAt.Equal(expectedExpiry) {
			return ErrWeeklyAdvanceStale
		}
		group, err := s.groupRepo.GetByID(txCtx, sub.GroupID)
		if err != nil {
			return err
		}
		now := s.now().Truncate(time.Microsecond)
		if automatic && !canAutoAdvanceWeek(sub, group, now) {
			return ErrWeeklyAdvanceUnavailable
		}
		result, err = weeklyAdvancePreview(sub, group, now)
		if err != nil {
			return err
		}
		monthlyStart, monthlyUsage := monthlyWindowAfterWeeklyAdvance(sub, result.NewExpiresAt, now)
		// Persist all affected fields together under the existing row lock, so
		// replayed requests and concurrent billing cannot overwrite each other.
		if err := s.userSubRepo.ApplyWeeklyAdvance(txCtx, id, now, result.NewExpiresAt, monthlyStart, monthlyUsage); err != nil {
			return err
		}
		groupID = sub.GroupID
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.invalidateSubscriptionCaches(userID, groupID); err != nil {
		log.Printf("advance weekly quota cache invalidation: %v", err)
	}
	return result, nil
}
