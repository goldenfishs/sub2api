//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func weeklyAdvanceFixture() (*UserSubscription, *Group, time.Time) {
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	return &UserSubscription{ID: 10, UserID: 20, GroupID: 30, Status: SubscriptionStatusActive, StartsAt: start, ExpiresAt: start.Add(30 * 24 * time.Hour), WeeklyWindowStart: &start, MonthlyWindowStart: &start, WeeklyUsageUSD: 30, DailyUsageUSD: 4, MonthlyUsageUSD: 50}, &Group{ID: 30, SubscriptionType: "subscription", WeeklyLimitUSD: ptrWeeklyLimit(30)}, start.Add(2 * 24 * time.Hour)
}
func ptrWeeklyLimit(v float64) *float64 { return &v }

func TestWeeklyAdvanceCalculation(t *testing.T) {
	sub, group, now := weeklyAdvanceFixture()
	preview, err := weeklyAdvancePreview(sub, group, now)
	require.NoError(t, err)
	require.Equal(t, int64(5*24*3600), preview.DeductSeconds)
	require.Equal(t, sub.ExpiresAt.Add(-5*24*time.Hour), preview.NewExpiresAt)
	// Fractional days are charged exactly, not rounded up to whole days.
	preview, err = weeklyAdvancePreview(sub, group, now.Add(90*time.Minute))
	require.NoError(t, err)
	require.Equal(t, int64(5*24*3600-90*60), preview.DeductSeconds)
	require.Equal(t, sub.ExpiresAt.Add(-5*24*time.Hour+90*time.Minute), preview.NewExpiresAt)
}

func TestWeeklyAdvanceEligibility(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*UserSubscription, *Group, time.Time)
	}{
		{"unused", func(s *UserSubscription, g *Group, n time.Time) { s.WeeklyUsageUSD = 0 }},
		{"not activated", func(s *UserSubscription, g *Group, n time.Time) { s.WeeklyWindowStart = nil }},
		{"expired", func(s *UserSubscription, g *Group, n time.Time) { s.ExpiresAt = n }},
		{"suspended", func(s *UserSubscription, g *Group, n time.Time) { s.Status = "suspended" }},
		{"last week", func(s *UserSubscription, g *Group, n time.Time) {
			s.ExpiresAt = s.WeeklyWindowStart.Add(7 * 24 * time.Hour)
		}},
		{"partial final week", func(s *UserSubscription, g *Group, n time.Time) { s.ExpiresAt = n.Add(time.Hour) }},
		{"already due", func(s *UserSubscription, g *Group, n time.Time) {
			v := n.Add(-7 * 24 * time.Hour)
			s.WeeklyWindowStart = &v
		}},
		{"no weekly limit", func(s *UserSubscription, g *Group, n time.Time) { g.WeeklyLimitUSD = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sub, g, now := weeklyAdvanceFixture()
			tc.mutate(sub, g, now)
			_, err := weeklyAdvancePreview(sub, g, now)
			require.ErrorIs(t, err, ErrWeeklyAdvanceUnavailable)
		})
	}
}

type weeklyAdvanceRepo struct {
	UserSubscriptionRepository
	sub *UserSubscription
}

func (r *weeklyAdvanceRepo) GetByID(context.Context, int64) (*UserSubscription, error) {
	cp := *r.sub
	return &cp, nil
}
func (r *weeklyAdvanceRepo) GetByIDForUpdate(ctx context.Context, id int64) (*UserSubscription, error) {
	return r.GetByID(ctx, id)
}
func (r *weeklyAdvanceRepo) ApplyWeeklyAdvance(_ context.Context, _ int64, now, expires time.Time, monthlyStart *time.Time, monthlyUsage float64) error {
	r.sub.WeeklyUsageUSD = 0
	r.sub.WeeklyWindowStart = &now
	r.sub.ExpiresAt = expires
	r.sub.MonthlyWindowStart = monthlyStart
	r.sub.MonthlyUsageUSD = monthlyUsage
	return nil
}

type weeklyAdvanceGroupRepo struct {
	GroupRepository
	group *Group
}

func (r *weeklyAdvanceGroupRepo) GetByID(context.Context, int64) (*Group, error) { return r.group, nil }

func TestWeeklyAdvanceOwnershipAndReplay(t *testing.T) {
	sub, group, now := weeklyAdvanceFixture()
	repo := &weeklyAdvanceRepo{sub: sub}
	svc := &SubscriptionService{userSubRepo: repo, groupRepo: &weeklyAdvanceGroupRepo{group: group}, now: func() time.Time { return now }}
	start, expiry := *sub.WeeklyWindowStart, sub.ExpiresAt
	_, err := svc.PreviewAdvanceWeek(context.Background(), 999, sub.ID)
	require.ErrorIs(t, err, ErrSubscriptionNotFound)
	_, err = svc.AdvanceWeek(context.Background(), 999, sub.ID, start, expiry)
	require.ErrorIs(t, err, ErrSubscriptionNotFound)
	require.Equal(t, float64(30), sub.WeeklyUsageUSD)
	_, err = svc.AdvanceWeek(context.Background(), sub.UserID, sub.ID, start, expiry)
	require.NoError(t, err)
	require.Equal(t, float64(0), sub.WeeklyUsageUSD)
	require.Equal(t, float64(4), sub.DailyUsageUSD)
	require.Equal(t, float64(50), sub.MonthlyUsageUSD)
	require.Equal(t, now, *sub.WeeklyWindowStart)
	require.Equal(t, expiry.Add(-5*24*time.Hour), sub.ExpiresAt)
	require.Equal(t, start.Add(-5*24*time.Hour), *sub.MonthlyWindowStart)
	_, err = svc.AdvanceWeek(context.Background(), sub.UserID, sub.ID, start, expiry)
	require.ErrorIs(t, err, ErrWeeklyAdvanceStale)
	require.Equal(t, expiry.Add(-5*24*time.Hour), sub.ExpiresAt)
}

func TestWeeklyAdvanceAutomaticOptInAndLimits(t *testing.T) {
	sub, group, now := weeklyAdvanceFixture()
	group.Status = "active"
	require.False(t, canAutoAdvanceWeek(sub, group, now))
	sub.AutoAdvanceWeek = true
	require.True(t, canAutoAdvanceWeek(sub, group, now))
	sub.WeeklyUsageUSD = 29.99
	require.False(t, canAutoAdvanceWeek(sub, group, now))
	sub.WeeklyUsageUSD = 30
	group.MonthlyLimitUSD = ptrWeeklyLimit(50)
	require.False(t, canAutoAdvanceWeek(sub, group, now))
	monthly := now.Add(-30 * 24 * time.Hour)
	sub.MonthlyWindowStart = &monthly
	// Use an older subscription start so this is a real expired monthly window.
	sub.StartsAt = monthly
	require.True(t, canAutoAdvanceWeek(sub, group, now))
	group.DailyLimitUSD = ptrWeeklyLimit(4)
	require.False(t, canAutoAdvanceWeek(sub, group, now))
	daily := now.Add(-24 * time.Hour)
	sub.DailyWindowStart = &daily
	require.True(t, canAutoAdvanceWeek(sub, group, now))
	group.Status = "disabled"
	require.False(t, canAutoAdvanceWeek(sub, group, now))
}

func TestWeeklyAdvanceAutomaticRechecksStoredOptIn(t *testing.T) {
	sub, group, now := weeklyAdvanceFixture()
	group.Status = "active"
	repo := &weeklyAdvanceRepo{sub: sub}
	svc := &SubscriptionService{userSubRepo: repo, groupRepo: &weeklyAdvanceGroupRepo{group: group}, now: func() time.Time { return now }}
	start, expiry := *sub.WeeklyWindowStart, sub.ExpiresAt
	// A stale worker cannot spend time after the user turned the switch off.
	_, err := svc.advanceWeek(context.Background(), sub.UserID, sub.ID, start, expiry, true)
	require.ErrorIs(t, err, ErrWeeklyAdvanceUnavailable)
	require.Equal(t, expiry, sub.ExpiresAt)
	sub.AutoAdvanceWeek = true
	_, err = svc.advanceWeek(context.Background(), sub.UserID, sub.ID, start, expiry, true)
	require.NoError(t, err)
	require.True(t, sub.AutoAdvanceWeek)
	require.Equal(t, float64(0), sub.WeeklyUsageUSD)
	require.Equal(t, expiry.Add(-5*24*time.Hour), sub.ExpiresAt)
	_, err = svc.advanceWeek(context.Background(), sub.UserID, sub.ID, start, expiry, true)
	require.ErrorIs(t, err, ErrWeeklyAdvanceStale)
}

func TestWeeklyAdvanceRequestMaintenance(t *testing.T) {
	sub, group, now := weeklyAdvanceFixture()
	group.Status = "active"
	sub.AutoAdvanceWeek = true
	repo := &weeklyAdvanceRepo{sub: sub}
	svc := &SubscriptionService{userSubRepo: repo, groupRepo: &weeklyAdvanceGroupRepo{group: group}, now: func() time.Time { return now }}
	expiry := sub.ExpiresAt
	snapshot := *sub
	needed, err := svc.ValidateAndCheckLimits(&snapshot, group)
	require.NoError(t, err)
	require.True(t, needed)
	refreshed, err := svc.EnsureWindowMaintenance(context.Background(), &snapshot)
	require.NoError(t, err)
	require.Zero(t, refreshed.WeeklyUsageUSD)
	require.Equal(t, expiry.Add(-5*24*time.Hour), refreshed.ExpiresAt)
	require.True(t, refreshed.AutoAdvanceWeek)
}

func TestWeeklyAdvanceMonthlyClock(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		age, expiry, wantAge time.Duration
		wantUsage            float64
	}{
		{"preserve usage", 2 * 24 * time.Hour, 28 * 24 * time.Hour, 7 * 24 * time.Hour, 50},
		{"renewed subscription crosses month", 28 * 24 * time.Hour, 32 * 24 * time.Hour, 3 * 24 * time.Hour, 0},
		{"exact month boundary", 25 * 24 * time.Hour, 35 * 24 * time.Hour, 0, 0},
		{"expiry is not a new month", 25 * 24 * time.Hour, 5 * 24 * time.Hour, 30 * 24 * time.Hour, 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sub, _, now := weeklyAdvanceFixture()
			start := now.Add(-tc.age)
			sub.StartsAt = start
			sub.MonthlyWindowStart = &start
			sub.ExpiresAt = now.Add(tc.expiry)
			monthly, usage := monthlyWindowAfterWeeklyAdvance(sub, sub.ExpiresAt.Add(-5*24*time.Hour), now)
			require.Equal(t, now.Add(-tc.wantAge), *monthly)
			require.Equal(t, tc.wantUsage, usage)
		})
	}
	sub, _, now := weeklyAdvanceFixture()
	sub.MonthlyWindowStart = nil
	monthly, usage := monthlyWindowAfterWeeklyAdvance(sub, sub.ExpiresAt.Add(-time.Hour), now)
	require.Nil(t, monthly)
	require.Equal(t, sub.MonthlyUsageUSD, usage)
	legacy := startOfDay(sub.StartsAt)
	sub.MonthlyWindowStart = &legacy
	monthly, usage = monthlyWindowAfterWeeklyAdvance(sub, sub.ExpiresAt.Add(-90*time.Minute), now)
	require.Equal(t, sub.StartsAt.Add(-90*time.Minute), *monthly)
	require.Equal(t, sub.MonthlyUsageUSD, usage)
}

func TestWeeklyAdvanceMonthlyLimitAfterShift(t *testing.T) {
	sub, group, now := weeklyAdvanceFixture()
	group.Status = "active"
	group.MonthlyLimitUSD = ptrWeeklyLimit(50)
	sub.AutoAdvanceWeek = true
	// Monthly quota exhausted but the advanced clock has not reached renewal.
	require.False(t, canAutoAdvanceWeek(sub, group, now))
	month := now.Add(-28 * 24 * time.Hour)
	sub.StartsAt = month
	sub.MonthlyWindowStart = &month
	sub.ExpiresAt = month.Add(60 * 24 * time.Hour)
	require.True(t, canAutoAdvanceWeek(sub, group, now))
	repo := &weeklyAdvanceRepo{sub: sub}
	svc := &SubscriptionService{userSubRepo: repo, groupRepo: &weeklyAdvanceGroupRepo{group: group}, now: func() time.Time { return now }}
	_, err := svc.advanceWeek(context.Background(), sub.UserID, sub.ID, *sub.WeeklyWindowStart, sub.ExpiresAt, true)
	require.NoError(t, err)
	require.Zero(t, sub.MonthlyUsageUSD)
	require.Equal(t, now.Add(-3*24*time.Hour), *sub.MonthlyWindowStart)
	require.Equal(t, float64(4), sub.DailyUsageUSD)
}

func TestWeeklyAdvanceRepeatedMonthlyShift(t *testing.T) {
	sub, group, now := weeklyAdvanceFixture()
	sub.ExpiresAt = sub.StartsAt.Add(90 * 24 * time.Hour)
	repo := &weeklyAdvanceRepo{sub: sub}
	svc := &SubscriptionService{userSubRepo: repo, groupRepo: &weeklyAdvanceGroupRepo{group: group}, now: func() time.Time { return now }}
	for i := 0; i < 5; i++ {
		oldMonth, oldExpiry := *sub.MonthlyResetTime(), sub.ExpiresAt
		sub.WeeklyUsageUSD = 30
		_, err := svc.AdvanceWeek(context.Background(), sub.UserID, sub.ID, *sub.WeeklyWindowStart, oldExpiry)
		require.NoError(t, err)
		expected := oldMonth.Add(sub.ExpiresAt.Sub(oldExpiry))
		if !expected.After(now) {
			expected = expected.Add(30 * 24 * time.Hour)
			require.Zero(t, sub.MonthlyUsageUSD)
		}
		require.Equal(t, expected, *sub.MonthlyResetTime())
		now = now.Add(24 * time.Hour)
	}
}
