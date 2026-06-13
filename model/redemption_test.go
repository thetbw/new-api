package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func insertRedemptionTestUser(t *testing.T, quota int, group string) *User {
	t.Helper()
	user := &User{
		Username: "redemption-user-" + common.GetRandomString(8),
		Quota:    quota,
		Group:    group,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func insertRedemptionTestPlan(t *testing.T, enabled bool, maxPurchasePerUser int, upgradeGroup string) *SubscriptionPlan {
	t.Helper()
	plan := &SubscriptionPlan{
		Title:              "Pro",
		PriceAmount:        1,
		Currency:           "USD",
		DurationUnit:       SubscriptionDurationMonth,
		DurationValue:      1,
		Enabled:            enabled,
		TotalAmount:        1000,
		MaxPurchasePerUser: maxPurchasePerUser,
		UpgradeGroup:       upgradeGroup,
	}
	require.NoError(t, DB.Create(plan).Error)
	return plan
}

func insertRedemptionTestCode(t *testing.T, redemption *Redemption) *Redemption {
	t.Helper()
	if redemption.Key == "" {
		redemption.Key = common.GetUUID()
	}
	if redemption.Status == 0 {
		redemption.Status = common.RedemptionCodeStatusEnabled
	}
	if redemption.CreatedTime == 0 {
		redemption.CreatedTime = common.GetTimestamp()
	}
	require.NoError(t, DB.Create(redemption).Error)
	return redemption
}

func TestRedeemQuotaCodeAddsQuotaAndMarksUsed(t *testing.T) {
	truncateTables(t)
	user := insertRedemptionTestUser(t, 100, "default")
	code := insertRedemptionTestCode(t, &Redemption{
		Name:  "quota",
		Quota: 250,
	})

	result, err := Redeem(code.Key, user.Id)

	require.NoError(t, err)
	require.Equal(t, RedemptionTypeQuota, result.Type)
	require.Equal(t, 250, result.Quota)

	var updatedUser User
	require.NoError(t, DB.First(&updatedUser, user.Id).Error)
	require.Equal(t, 350, updatedUser.Quota)

	var updatedCode Redemption
	require.NoError(t, DB.First(&updatedCode, code.Id).Error)
	require.Equal(t, common.RedemptionCodeStatusUsed, updatedCode.Status)
	require.Equal(t, user.Id, updatedCode.UsedUserId)
	require.NotZero(t, updatedCode.RedeemedTime)
}

func TestRedeemSubscriptionCodeCreatesActiveSubscription(t *testing.T) {
	truncateTables(t)
	user := insertRedemptionTestUser(t, 100, "default")
	plan := insertRedemptionTestPlan(t, true, 0, "vip")
	code := insertRedemptionTestCode(t, &Redemption{
		Name:               "subscription",
		Type:               RedemptionTypeSubscription,
		SubscriptionPlanId: plan.Id,
	})

	result, err := Redeem(code.Key, user.Id)

	require.NoError(t, err)
	require.Equal(t, RedemptionTypeSubscription, result.Type)
	require.Equal(t, plan.Id, result.SubscriptionPlanId)
	require.Equal(t, plan.Title, result.PlanTitle)
	require.NotZero(t, result.SubscriptionId)

	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, result.SubscriptionId).Error)
	require.Equal(t, user.Id, subscription.UserId)
	require.Equal(t, plan.Id, subscription.PlanId)
	require.Equal(t, "active", subscription.Status)
	require.Equal(t, "redemption", subscription.Source)

	var updatedUser User
	require.NoError(t, DB.First(&updatedUser, user.Id).Error)
	require.Equal(t, 100, updatedUser.Quota)
	require.Equal(t, "vip", updatedUser.Group)

	var updatedCode Redemption
	require.NoError(t, DB.First(&updatedCode, code.Id).Error)
	require.Equal(t, common.RedemptionCodeStatusUsed, updatedCode.Status)
}

func TestRedeemSubscriptionCodeRejectsDisabledPlanWithoutUsingCode(t *testing.T) {
	truncateTables(t)
	user := insertRedemptionTestUser(t, 100, "default")
	plan := insertRedemptionTestPlan(t, false, 0, "")
	code := insertRedemptionTestCode(t, &Redemption{
		Name:               "subscription",
		Type:               RedemptionTypeSubscription,
		SubscriptionPlanId: plan.Id,
	})

	result, err := Redeem(code.Key, user.Id)

	require.ErrorIs(t, err, ErrRedeemFailed)
	require.Nil(t, result)

	var updatedCode Redemption
	require.NoError(t, DB.First(&updatedCode, code.Id).Error)
	require.Equal(t, common.RedemptionCodeStatusEnabled, updatedCode.Status)

	var count int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", user.Id).Count(&count).Error)
	require.Zero(t, count)
}

func TestRedeemSubscriptionCodeRejectsPurchaseLimitWithoutUsingCode(t *testing.T) {
	truncateTables(t)
	user := insertRedemptionTestUser(t, 100, "default")
	plan := insertRedemptionTestPlan(t, true, 1, "")
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:      user.Id,
		PlanId:      plan.Id,
		AmountTotal: 1000,
		StartTime:   common.GetTimestamp(),
		EndTime:     common.GetTimestamp() + 3600,
		Status:      "active",
		Source:      "order",
	}).Error)
	code := insertRedemptionTestCode(t, &Redemption{
		Name:               "subscription",
		Type:               RedemptionTypeSubscription,
		SubscriptionPlanId: plan.Id,
	})

	result, err := Redeem(code.Key, user.Id)

	require.ErrorIs(t, err, ErrRedeemFailed)
	require.Nil(t, result)

	var updatedCode Redemption
	require.NoError(t, DB.First(&updatedCode, code.Id).Error)
	require.Equal(t, common.RedemptionCodeStatusEnabled, updatedCode.Status)
}

func TestRedeemRejectsUsedDisabledAndExpiredCodes(t *testing.T) {
	truncateTables(t)
	user := insertRedemptionTestUser(t, 100, "default")
	now := common.GetTimestamp()
	cases := []Redemption{
		{Name: "used", Quota: 1, Status: common.RedemptionCodeStatusUsed},
		{Name: "disabled", Quota: 1, Status: common.RedemptionCodeStatusDisabled},
		{Name: "expired", Quota: 1, Status: common.RedemptionCodeStatusEnabled, ExpiredTime: now - 1},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			code := insertRedemptionTestCode(t, &tc)
			result, err := Redeem(code.Key, user.Id)

			require.ErrorIs(t, err, ErrRedeemFailed)
			require.Nil(t, result)

			var updatedCode Redemption
			require.NoError(t, DB.First(&updatedCode, code.Id).Error)
			require.Equal(t, code.Status, updatedCode.Status)
			require.Zero(t, updatedCode.UsedUserId)
		})
	}
}
