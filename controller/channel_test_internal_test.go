package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettleTestQuotaUsesTieredBilling(t *testing.T) {
	info := &relaycommon.RelayInfo{
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:   "tiered_expr",
			ExprString:    `param("stream") == true ? tier("stream", p * 3) : tier("base", p * 2)`,
			ExprHash:      billingexpr.ExprHashString(`param("stream") == true ? tier("stream", p * 3) : tier("base", p * 2)`),
			GroupRatio:    1,
			EstimatedTier: "stream",
			QuotaPerUnit:  common.QuotaPerUnit,
			ExprVersion:   1,
		},
		BillingRequestInput: &billingexpr.RequestInput{
			Body: []byte(`{"stream":true}`),
		},
	}

	quota, result := settleTestQuota(info, types.PriceData{
		ModelRatio:      1,
		CompletionRatio: 2,
	}, &dto.Usage{
		PromptTokens: 1000,
	})

	require.Equal(t, 1500, quota)
	require.NotNil(t, result)
	require.Equal(t, "stream", result.MatchedTier)
}

func TestBuildTestLogOtherInjectsTieredInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	info := &relaycommon.RelayInfo{
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode: "tiered_expr",
			ExprString:  `tier("base", p * 2)`,
		},
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	priceData := types.PriceData{
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}
	usage := &dto.Usage{
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 12,
		},
	}

	other := buildTestLogOther(ctx, info, priceData, usage, &billingexpr.TieredResult{
		MatchedTier: "base",
	})

	require.Equal(t, "tiered_expr", other["billing_mode"])
	require.Equal(t, "base", other["matched_tier"])
	require.NotEmpty(t, other["expr_b64"])
}

func TestResolveChannelTestUserIDUsesRequestUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("id", 2)

	userID, err := resolveChannelTestUserID(ctx)

	require.NoError(t, err)
	require.Equal(t, 2, userID)
}

func TestEvaluateAutomaticChannelTest(t *testing.T) {
	originalAutoDisable := common.AutomaticDisableChannelEnabled
	originalAutoEnable := common.AutomaticEnableChannelEnabled
	originalDisableRanges := operation_setting.AutomaticDisableStatusCodeRanges
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = originalAutoDisable
		common.AutomaticEnableChannelEnabled = originalAutoEnable
		operation_setting.AutomaticDisableStatusCodeRanges = originalDisableRanges
	})

	operation_setting.AutomaticDisableStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: http.StatusUnauthorized, End: http.StatusUnauthorized}}
	disableThreshold := int64(100_000)
	channelError := func() *types.NewAPIError {
		return types.NewOpenAIError(errors.New("unauthorized"), types.ErrorCodeBadResponseStatusCode, http.StatusUnauthorized)
	}

	t.Run("enabled channel notifies even when channel auto ban is disabled", func(t *testing.T) {
		common.AutomaticDisableChannelEnabled = true
		common.AutomaticEnableChannelEnabled = false

		decision, effectiveErr := evaluateAutomaticChannelTest(common.ChannelStatusEnabled, false, channelError(), 100, disableThreshold)

		require.NotNil(t, effectiveErr)
		require.True(t, decision.notifyUnavailable)
		require.False(t, decision.disable)
		require.False(t, decision.enable)
	})

	t.Run("enabled channel notifies even when global automatic disable is disabled", func(t *testing.T) {
		common.AutomaticDisableChannelEnabled = false
		common.AutomaticEnableChannelEnabled = false

		decision, effectiveErr := evaluateAutomaticChannelTest(common.ChannelStatusEnabled, true, channelError(), 100, disableThreshold)

		require.NotNil(t, effectiveErr)
		require.True(t, decision.notifyUnavailable)
		require.False(t, decision.disable)
		require.False(t, decision.enable)
	})

	t.Run("enabled channel disables when automatic disable matches and auto ban is enabled", func(t *testing.T) {
		common.AutomaticDisableChannelEnabled = true
		common.AutomaticEnableChannelEnabled = false

		decision, effectiveErr := evaluateAutomaticChannelTest(common.ChannelStatusEnabled, true, channelError(), 100, disableThreshold)

		require.NotNil(t, effectiveErr)
		require.True(t, decision.notifyUnavailable)
		require.True(t, decision.disable)
		require.False(t, decision.enable)
	})

	t.Run("enabled channel timeout notifies and disables", func(t *testing.T) {
		common.AutomaticDisableChannelEnabled = true
		common.AutomaticEnableChannelEnabled = false

		decision, effectiveErr := evaluateAutomaticChannelTest(common.ChannelStatusEnabled, true, nil, disableThreshold+1, disableThreshold)

		require.NotNil(t, effectiveErr)
		require.True(t, decision.notifyUnavailable)
		require.True(t, decision.disable)
		require.False(t, decision.enable)
	})

	t.Run("closed channel failure does not notify", func(t *testing.T) {
		common.AutomaticDisableChannelEnabled = true
		common.AutomaticEnableChannelEnabled = false

		decision, effectiveErr := evaluateAutomaticChannelTest(common.ChannelStatusAutoDisabled, true, channelError(), 100, disableThreshold)

		require.NotNil(t, effectiveErr)
		require.False(t, decision.notifyUnavailable)
		require.False(t, decision.disable)
		require.False(t, decision.enable)
	})

	t.Run("auto disabled channel success can enable", func(t *testing.T) {
		common.AutomaticDisableChannelEnabled = true
		common.AutomaticEnableChannelEnabled = true

		decision, effectiveErr := evaluateAutomaticChannelTest(common.ChannelStatusAutoDisabled, true, nil, 100, disableThreshold)

		require.Nil(t, effectiveErr)
		require.False(t, decision.notifyUnavailable)
		require.False(t, decision.disable)
		require.True(t, decision.enable)
	})
}
