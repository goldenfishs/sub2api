//go:build unit

package service

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGPT61SolPricingCatalogAndFallbackBilling(t *testing.T) {
	data, err := os.ReadFile("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	catalog := &PricingService{}
	catalog.pricingData, err = catalog.parsePricingData(data)
	require.NoError(t, err)
	stale := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gpt-6":       {InputCostPerToken: 10e-6, OutputCostPerToken: 50e-6},
		"gpt-6-astra": openAIGPT6AstraFallbackPricing,
		"gpt-6-sol":   openAIGPT6SolFallbackPricing,
		"gpt-6.1":     {InputCostPerToken: 99e-6, OutputCostPerToken: 99e-6},
	}}
	for source, svc := range map[string]*BillingService{
		"billing fallback": newTestBillingService(),
		"stale catalog":    NewBillingService(&config.Config{}, stale),
		"bundled catalog":  NewBillingService(&config.Config{}, catalog),
	} {
		for _, model := range []string{"gpt-6.1-sol", "openai/gpt-6.1-sol-high", "azure/gpt-6.1-sol-max", "gpt-6.1-sol-openai-compact"} {
			t.Run(source+"/"+model, func(t *testing.T) {
				for _, totalInput := range []int{271999, 272000, 272001} {
					for tier, tierMultiplier := range map[string]float64{"": 1, "fast": 2, "priority": 2, "flex": 0.5} {
						tokens := UsageTokens{InputTokens: totalInput - 3000, CacheReadTokens: 2000, CacheCreationTokens: 1000, OutputTokens: 500}
						cost, err := svc.CalculateCostWithServiceTier(model, tokens, 1, tier)
						require.NoError(t, err)
						inputMultiplier, outputMultiplier := 1.0, 1.0
						if totalInput > 272000 {
							inputMultiplier, outputMultiplier = 2, 1.5
						}
						require.InDelta(t, float64(tokens.InputTokens)*2e-6*inputMultiplier*tierMultiplier, cost.InputCost, 1e-10)
						require.InDelta(t, 2000*0.1e-6*inputMultiplier*tierMultiplier, cost.CacheReadCost, 1e-10)
						require.InDelta(t, 1000*2.5e-6*inputMultiplier*tierMultiplier, cost.CacheCreationCost, 1e-10)
						require.InDelta(t, 500*10e-6*outputMultiplier*tierMultiplier, cost.OutputCost, 1e-10)
						require.InDelta(t, cost.InputCost+cost.OutputCost+cost.CacheCreationCost+cost.CacheReadCost, cost.TotalCost, 1e-10)
						require.Equal(t, totalInput > 272000, cost.LongContextBillingApplied)
					}
				}
			})
		}
	}
	// Batch is a separate API, not a realtime service_tier. Its official rates
	// remain available in the bundled catalog alongside Flex's 50% prices.
	var raw map[string]map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	card := raw["gpt-6.1-sol"]
	for _, suffix := range []string{"batches", "flex"} {
		require.Equal(t, 1e-6, card["input_cost_per_token_"+suffix])
		require.Equal(t, 5e-6, card["output_cost_per_token_"+suffix])
		require.Equal(t, 5e-8, card["cache_read_input_token_cost_"+suffix])
		require.Equal(t, 1.25e-6, card["cache_creation_input_token_cost_"+suffix])
	}
	require.Equal(t, float64(1050000), card["context_window"])
	require.Equal(t, float64(128000), card["max_output_tokens"])
}

func TestGPT61SolPricingPreservesExplicitCatalogAndChannelPrices(t *testing.T) {
	svc := &PricingService{}
	var err error
	svc.pricingData, err = svc.parsePricingData([]byte(`{
		"gpt-6.1-sol":{"litellm_provider":"openai","input_cost_per_token":0.000007,"output_cost_per_token":0.000011,"cache_read_input_token_cost":0.000003,"cache_creation_input_token_cost":0},
		"gpt-6.1-sol-high":{"litellm_provider":"openai","input_cost_per_token":0.000013,"output_cost_per_token":0.000017},
		"gpt-6-sol":{"input_cost_per_token":0.000002,"output_cost_per_token":0.00001}
	}`))
	require.NoError(t, err)
	billing := NewBillingService(&config.Config{}, svc)
	for _, model := range []string{"gpt-6.1-sol", "openai/gpt-6.1-sol-max"} {
		for tier, multiplier := range map[string]float64{"": 1, "priority": 2, "flex": 0.5} {
			cost, err := billing.CalculateCostWithServiceTier(model, UsageTokens{InputTokens: 1000, OutputTokens: 1000, CacheReadTokens: 1000, CacheCreationTokens: 1000}, 1, tier)
			require.NoError(t, err)
			require.InDelta(t, 1000*7e-6*multiplier, cost.InputCost, 1e-10)
			require.InDelta(t, 1000*11e-6*multiplier, cost.OutputCost, 1e-10)
			require.InDelta(t, 1000*3e-6*multiplier, cost.CacheReadCost, 1e-10)
			require.Zero(t, cost.CacheCreationCost)
		}
	}
	require.Same(t, svc.pricingData["gpt-6.1-sol-high"], svc.GetModelPricing("openai/gpt-6.1-sol-high"))
	for _, price := range []float64{0, 9e-6} {
		prices, err := billing.GetModelPricingWithChannel("gpt-6.1-sol", &ChannelModelPricing{InputPrice: &price, OutputPrice: &price, CacheReadPrice: &price, CacheWritePrice: &price})
		require.NoError(t, err)
		cost := billing.computeTokenBreakdown(prices, UsageTokens{InputTokens: 1000, OutputTokens: 1000, CacheReadTokens: 1000, CacheCreationTokens: 1000}, 1, "", true)
		require.InDelta(t, 4000*price, cost.TotalCost, 1e-10)
	}
	require.Equal(t, 7e-6, svc.pricingData["gpt-6.1-sol"].InputCostPerToken)
	require.Zero(t, svc.pricingData["gpt-6.1-sol"].CacheCreationInputTokenCost)
}

func TestGPT61SolPricingDoesNotCaptureOtherModelNames(t *testing.T) {
	billing := newTestBillingService()
	pricing := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gpt-6.1-sol":   openAIGPT61SolFallbackPricing,
		"gpt-6-sol":     openAIGPT6SolFallbackPricing,
		"gpt-6-astra":   openAIGPT6AstraFallbackPricing,
		"gpt-6.1":       openAIGPT6AstraFallbackPricing,
		"gpt-5.1-codex": openAIGPT6SolFallbackPricing,
	}}
	for _, model := range []string{"gpt-6.1-solitude", "gpt-6.1-sol-preview", "gpt-6.1-sol-none", "gpt-6.1-sol-minimal", "not-gpt-6.1-sol"} {
		require.Nil(t, pricing.GetModelPricing(model), model)
		_, err := billing.GetModelPricing(model)
		require.ErrorIs(t, err, ErrModelPricingUnavailable, model)
	}
	old, err := billing.GetModelPricing("gpt-6-sol")
	require.NoError(t, err)
	require.Equal(t, 0.2e-6, old.CacheReadPricePerToken)
	astra, err := billing.GetModelPricing("gpt-6-astra")
	require.NoError(t, err)
	require.Equal(t, 10e-6, astra.InputPricePerToken)
}
