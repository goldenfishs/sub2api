package service

import (
	"context"
	"errors"
	"log"
	"time"

	"entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	entgroup "github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
)

// Auto advance must only spend time when weekly quota is exhausted and the
// resulting quota is usable. Natural daily/monthly resets are considered here.
func canAutoAdvanceWeek(sub *UserSubscription, group *Group, now time.Time) bool {
	if !sub.AutoAdvanceWeek || group == nil || !group.IsActive() || !group.HasWeeklyLimit() || sub.WeeklyUsageUSD < *group.WeeklyLimitUSD {
		return false
	}
	effective := *sub
	if effective.canAutomaticallyResetDailyAt(now) {
		effective.DailyUsageUSD = 0
	}
	if effective.canAutomaticallyResetMonthlyAt(now) {
		effective.MonthlyUsageUSD = 0
	}
	return (!group.HasDailyLimit() || effective.DailyUsageUSD < *group.DailyLimitUSD) &&
		(!group.HasMonthlyLimit() || effective.MonthlyUsageUSD < *group.MonthlyLimitUSD)
}

func (s *SubscriptionService) SetAutoAdvanceWeek(ctx context.Context, userID, id int64, enabled bool) (*UserSubscription, error) {
	var groupID int64
	err := s.withSubscriptionUpdateTx(ctx, func(txCtx context.Context) error {
		sub, err := s.userSubRepo.GetByIDForUpdate(txCtx, id)
		if err != nil {
			return err
		}
		if sub.UserID != userID {
			return ErrSubscriptionNotFound
		}
		if enabled {
			group, err := s.groupRepo.GetByID(txCtx, sub.GroupID)
			if err != nil {
				return err
			}
			if sub.Status != SubscriptionStatusActive || !sub.ExpiresAt.After(s.now()) || !group.IsSubscriptionType() || !group.HasWeeklyLimit() {
				return ErrWeeklyAdvanceUnavailable
			}
		}
		client := s.entClient
		if tx := dbent.TxFromContext(txCtx); tx != nil {
			client = tx.Client()
		}
		if err := client.UserSubscription.UpdateOneID(id).SetAutoAdvanceWeek(enabled).Exec(txCtx); err != nil {
			return err
		}
		groupID = sub.GroupID
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.invalidateSubscriptionCaches(userID, groupID); err != nil {
		log.Printf("auto weekly advance setting cache invalidation: %v", err)
	}
	return s.userSubRepo.GetByID(ctx, id)
}

func (s *SubscriptionService) tryAutoAdvanceWeek(ctx context.Context, sub *UserSubscription) error {
	if !sub.AutoAdvanceWeek || sub.WeeklyWindowStart == nil {
		return nil
	}
	_, err := s.advanceWeek(ctx, sub.UserID, sub.ID, *sub.WeeklyWindowStart, sub.ExpiresAt, true)
	if errors.Is(err, ErrWeeklyAdvanceUnavailable) || errors.Is(err, ErrWeeklyAdvanceStale) || errors.Is(err, ErrSubscriptionNotFound) {
		return nil
	}
	return err
}

func (s *SubscriptionService) startAutoAdvanceWeek() {
	if s.entClient == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.autoAdvanceCancel = cancel
	s.autoAdvanceDone = make(chan struct{})
	go func() {
		defer close(s.autoAdvanceDone)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				scanCtx, done := context.WithTimeout(ctx, 10*time.Second)
				if err := s.scanAutoAdvanceWeek(scanCtx); err != nil && ctx.Err() == nil {
					log.Printf("auto weekly advance scan: %v", err)
				}
				done()
			}
		}
	}()
}

func (s *SubscriptionService) scanAutoAdvanceWeek(ctx context.Context) error {
	// Keyset pagination prevents an ineligible earlier subscription from starving
	// later subscriptions. Eligibility is always rechecked inside the row lock.
	var after int64
	for {
		rows, err := s.entClient.UserSubscription.Query().Where(
			usersubscription.IDGT(after), usersubscription.AutoAdvanceWeekEQ(true),
			usersubscription.StatusEQ(SubscriptionStatusActive), usersubscription.ExpiresAtGT(s.now()),
			usersubscription.WeeklyUsageUsdGT(0), usersubscription.WeeklyWindowStartNotNil(),
			func(selector *sql.Selector) {
				g := sql.Table(entgroup.Table).As("auto_advance_group")
				selector.Join(g).On(selector.C(usersubscription.FieldGroupID), g.C(entgroup.FieldID)).
					Where(sql.And(sql.GT(g.C(entgroup.FieldWeeklyLimitUsd), 0), sql.ColumnsGTE(selector.C(usersubscription.FieldWeeklyUsageUsd), g.C(entgroup.FieldWeeklyLimitUsd))))
			},
		).Order(dbent.Asc(usersubscription.FieldID)).Limit(100).All(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			sub := &UserSubscription{ID: row.ID, UserID: row.UserID, GroupID: row.GroupID, AutoAdvanceWeek: row.AutoAdvanceWeek, WeeklyWindowStart: row.WeeklyWindowStart, ExpiresAt: row.ExpiresAt}
			if err := s.tryAutoAdvanceWeek(ctx, sub); err != nil {
				return err
			}
			after = row.ID
		}
		if len(rows) < 100 {
			return nil
		}
	}
}
