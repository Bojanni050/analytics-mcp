package api

import (
	"testing"

	"analytics-mcp/analytics"
)

// De app stuurt negative keywords als objecten mee: {"text": …, "match_type": …}.
// Deze test legt die vorm vast, plus de eenvoudiger vorm zonder match type.
func TestNegativeKeywordsParam(t *testing.T) {
	objects := map[string]any{
		"keywords": []any{
			map[string]any{"text": "gratis dj", "match_type": "PHRASE"},
			map[string]any{"term": "dj cursus", "matchType": "EXACT"},
			"losse term",
		},
	}
	got := negativeKeywordsParam(objects)
	want := []analytics.NegativeKeyword{
		{Text: "gratis dj", MatchType: "PHRASE"},
		{Text: "dj cursus", MatchType: "EXACT"},
		{Text: "losse term"},
	}
	if len(got) != len(want) {
		t.Fatalf("aantal keywords = %d, want %d (%#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("keyword %d = %#v, want %#v", i, got[i], want[i])
		}
	}

	if got := negativeKeywordsParam(map[string]any{"keywords": "a, b ,c"}); len(got) != 3 {
		t.Errorf("komma-lijst gaf %d items, want 3", len(got))
	}
	if got := negativeKeywordsParam(map[string]any{}); got != nil {
		t.Errorf("zonder keywords verwacht nil, kreeg %#v", got)
	}
}

// De app stuurt budgetten in micros; een mens-euro moet ook werken.
func TestAmountMicrosParam(t *testing.T) {
	micros, err := amountMicrosParam(map[string]any{"budget_amount_micros": float64(2500000)}, "budget_amount_micros")
	if err != nil || micros != 2500000 {
		t.Fatalf("micros = %d, err = %v", micros, err)
	}
	micros, err = amountMicrosParam(map[string]any{"amount": "12.50"}, "budget_amount_micros")
	if err != nil || micros != 12500000 {
		t.Fatalf("euro → micros = %d, err = %v", micros, err)
	}
	if _, err := amountMicrosParam(map[string]any{}, "budget_amount_micros"); err == nil {
		t.Error("ontbrekend bedrag hoort een fout te geven")
	}
}

// De acties die de app aanroept moeten allemaal bekend zijn bij de router.
func TestActionRoutingCoversAppActions(t *testing.T) {
	// Zonder geldige sessie faalt de client, maar een onbekende actie moet
	// eerder een duidelijke fout geven dan een mislukte API-call.
	for _, action := range []string{"pause_campaign", "enable_campaign", "push_negative_keywords", "create_campaign", "create_ad_group", "set_campaign_budget"} {
		if _, ok := knownAdsActions[action]; !ok {
			t.Errorf("actie %q ontbreekt in knownAdsActions", action)
		}
	}
	if _, ok := knownAdsActions["delete_everything"]; ok {
		t.Error("onbekende actie hoort niet in knownAdsActions")
	}
}
