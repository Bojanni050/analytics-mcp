package api

import "testing"

// De nieuwe write-acties moeten door de router herkend worden.
func TestActionRoutingCoversAdsWrites(t *testing.T) {
	for _, action := range []string{
		"mutate", "add_keywords", "set_keyword_status", "remove_keyword",
		"set_ad_group_status", "set_ad_status", "set_conversion_action",
	} {
		if !knownAdsActions[action] {
			t.Errorf("actie %q ontbreekt in knownAdsActions", action)
		}
	}
	if knownAdsActions["delete_everything"] {
		t.Error("een niet-bestaande actie hoort niet in knownAdsActions te staan")
	}
}

// mutateParams accepteert één operatie in de params én een lijst onder
// "operations", met de veiligheidsschakelaars erbij.
func TestMutateParamsShapes(t *testing.T) {
	ops, opts, err := mutateParams(map[string]any{
		"resource":      "ad_group_criterion",
		"op":            "update",
		"resource_name": "customers/3070436491/adGroupCriteria/1~2",
		"object":        map[string]any{"status": "PAUSED"},
		"validate_only": true,
	})
	if err != nil {
		t.Fatalf("enkele operatie gaf fout: %v", err)
	}
	if len(ops) != 1 || ops[0].Resource != "ad_group_criterion" || ops[0].Action != "update" {
		t.Fatalf("operatie = %#v", ops)
	}
	if ops[0].Object["status"] != "PAUSED" || ops[0].ResourceName == "" {
		t.Errorf("operatie onvolledig: %#v", ops[0])
	}
	if !opts.ValidateOnly {
		t.Error("validate_only hoort door te komen")
	}

	ops, _, err = mutateParams(map[string]any{
		"operations": []any{
			map[string]any{"resource": "ad_group_criterion", "op": "remove", "resource_name": "a"},
			map[string]any{"resource": "ad_group_criterion", "op": "remove", "resource_name": "b"},
		},
		"partial_failure": true,
	})
	if err != nil || len(ops) != 2 {
		t.Fatalf("lijst gaf %d operaties, err = %v", len(ops), err)
	}

	if _, _, err := mutateParams(map[string]any{"resource": "ad_group_criterion"}); err == nil {
		t.Error("zonder op hoort er een fout te komen")
	}
	if _, _, err := mutateParams(map[string]any{"op": "update"}); err == nil {
		t.Error("zonder resource hoort er een fout te komen")
	}
	if _, _, err := mutateParams(map[string]any{"operations": "geen lijst"}); err == nil {
		t.Error("operations zonder lijst hoort te falen")
	}
}

// Zoekwoorden komen als platte strings of als objecten met eigen match type;
// de defaults van de call gelden voor beide.
func TestKeywordInputsParam(t *testing.T) {
	got := keywordInputsParam(map[string]any{
		"keywords":   []any{"dj bruiloft", map[string]any{"text": "dj huren", "match_type": "EXACT"}},
		"match_type": "PHRASE",
		"cpc_bid":    1.5,
	})
	if len(got) != 2 {
		t.Fatalf("keywords = %d, want 2 (%#v)", len(got), got)
	}
	if got[0].Text != "dj bruiloft" || got[0].MatchType != "PHRASE" || got[0].CPCBidMicros != 1500000 {
		t.Errorf("eerste keyword = %#v", got[0])
	}
	if got[1].MatchType != "EXACT" {
		t.Errorf("een eigen match type hoort te winnen: %#v", got[1])
	}
	if got[1].CPCBidMicros != 1500000 {
		t.Errorf("de default cpc hoort ook te gelden: %#v", got[1])
	}

	if got := keywordInputsParam(map[string]any{"keywords": "a, b ,,c"}); len(got) != 3 {
		t.Errorf("komma-lijst gaf %d items, want 3", len(got))
	}
	if got := keywordInputsParam(map[string]any{}); got != nil {
		t.Errorf("zonder keywords verwacht nil, kreeg %#v", got)
	}
}

// Booleans komen in meerdere spellingen binnen; alle moeten werken.
func TestBoolParamSpellings(t *testing.T) {
	for _, want := range []any{true, "true", "1", "ja", float64(1)} {
		if !boolParam(map[string]any{"v": want}, "v") {
			t.Errorf("%#v hoort true te zijn", want)
		}
	}
	for _, want := range []any{false, "false", "nee", float64(0), ""} {
		if boolParam(map[string]any{"v": want}, "v") {
			t.Errorf("%#v hoort false te zijn", want)
		}
	}
	if boolParam(map[string]any{}, "v") {
		t.Error("een ontbrekende sleutel is false")
	}
	// Alleen een echt meegestuurde sleutel mag in een update belanden.
	if ptr := boolPtrParam(map[string]any{"primary_for_goal": false}, "primary_for_goal"); ptr == nil || *ptr {
		t.Errorf("false moet als false doorkomen, kreeg %v", ptr)
	}
	if ptr := boolPtrParam(map[string]any{}, "primary_for_goal"); ptr != nil {
		t.Errorf("niets meegestuurd hoort nil te zijn, kreeg %v", *ptr)
	}
}

// De eigen MCP-tool stuurt de negatieve zoekwoorden als []string, een
// HTTP-client als []any (of één komma-string). Alle drie moeten door
// negativeKeywordsParam heen: een []string faalde hier eerder de []any-assertie,
// waardoor ads_push_negative_keywords altijd "geen (niet-lege) negative keywords
// ontvangen" teruggaf zonder iets te schrijven.
func TestNegativeKeywordsParamShapes(t *testing.T) {
	got := negativeKeywordsParam(map[string]any{"keywords": []string{"jacco silver", "  la fuente  "}})
	if len(got) != 2 || got[0].Text != "jacco silver" || got[1].Text != "la fuente" {
		t.Fatalf("[]string gaf %#v", got)
	}
	if got := negativeKeywordsParam(map[string]any{
		"keywords": []any{"a", map[string]any{"text": "b", "match_type": "EXACT"}},
	}); len(got) != 2 || got[1].Text != "b" || got[1].MatchType != "EXACT" {
		t.Errorf("[]any gaf %#v", got)
	}
	if got := negativeKeywordsParam(map[string]any{"keywords": "a, b ,,c"}); len(got) != 3 {
		t.Errorf("komma-lijst gaf %d items, want 3", len(got))
	}
	if got := negativeKeywordsParam(map[string]any{"keywords": []string{}}); len(got) != 0 {
		t.Errorf("lege lijst hoort leeg te blijven, kreeg %#v", got)
	}
	if got := negativeKeywordsParam(map[string]any{}); got != nil {
		t.Errorf("zonder keywords verwacht nil, kreeg %#v", got)
	}
}
