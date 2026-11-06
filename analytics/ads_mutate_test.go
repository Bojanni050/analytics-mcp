package analytics

import (
	"context"
	"strings"
	"testing"
)

const mutateCustomer = "3070436491"

// Een zoekwoord toevoegen: één create per keyword, camelCase, geen updateMask.
func TestBuildAdsMutateCreateKeyword(t *testing.T) {
	grouped, order, err := buildAdsMutateOperations(mutateCustomer, []MutateOperation{{
		Action:   "create",
		Resource: "ad_group_criterion",
		Object: map[string]any{
			"ad_group":       "customers/3070436491/adGroups/111",
			"status":         "ENABLED",
			"keyword":        map[string]any{"text": "dj bruiloft", "match_type": "PHRASE"},
			"cpc_bid_micros": "1500000",
		},
	}})
	if err != nil {
		t.Fatalf("create gaf fout: %v", err)
	}
	if len(order) != 1 || order[0] != "ad_group_criterion" {
		t.Fatalf("order = %#v, want [ad_group_criterion]", order)
	}
	ops := grouped["ad_group_criterion"]
	if len(ops) != 1 {
		t.Fatalf("operaties = %d, want 1", len(ops))
	}
	if _, ok := ops[0]["create"]; !ok {
		t.Fatalf("geen create-body: %#v", ops[0])
	}
	create, _ := ops[0]["create"].(map[string]any)
	if create["adGroup"] != "customers/3070436491/adGroups/111" {
		t.Errorf("adGroup = %v", create["adGroup"])
	}
	if create["cpcBidMicros"] != "1500000" {
		t.Errorf("cpcBidMicros = %v", create["cpcBidMicros"])
	}
	keyword, _ := create["keyword"].(map[string]any)
	if keyword["text"] != "dj bruiloft" || keyword["matchType"] != "PHRASE" {
		t.Errorf("keyword = %#v", keyword)
	}
	if _, ok := ops[0]["updateMask"]; ok {
		t.Error("een create hoort geen updateMask te hebben")
	}
}

// Punt-gesleutelde velden worden nesting en de update mask komt in snake_case.
func TestBuildAdsMutateUpdateMaskAndDottedKeys(t *testing.T) {
	grouped, _, err := buildAdsMutateOperations(mutateCustomer, []MutateOperation{{
		Action:       "update",
		Resource:     "ad_group_criterion",
		ResourceName: "customers/3070436491/adGroupCriteria/111~222",
		Object:       map[string]any{"keyword.text": "dj huren", "cpc_bid_micros": 2000000},
	}})
	if err != nil {
		t.Fatalf("update gaf fout: %v", err)
	}
	op := grouped["ad_group_criterion"][0]
	if op["updateMask"] != "cpc_bid_micros,keyword.text" {
		t.Errorf("updateMask = %q, want \"cpc_bid_micros,keyword.text\"", op["updateMask"])
	}
	update, _ := op["update"].(map[string]any)
	keyword, ok := update["keyword"].(map[string]any)
	if !ok || keyword["text"] != "dj huren" {
		t.Errorf("keyword = %#v — een punt-sleutel hoort genest te worden", update["keyword"])
	}
	if update["resourceName"] != "customers/3070436491/adGroupCriteria/111~222" {
		t.Errorf("resourceName = %v", update["resourceName"])
	}
}

// Ids in het object bouwen de resourceName; ze horen niet in body of mask.
func TestBuildAdsMutateComposesResourceName(t *testing.T) {
	grouped, _, err := buildAdsMutateOperations(mutateCustomer, []MutateOperation{{
		Action:   "update",
		Resource: "ad_group_criterion",
		Object: map[string]any{
			"ad_group_id":  "111",
			"criterion_id": "222",
			"status":       "PAUSED",
		},
	}})
	if err != nil {
		t.Fatalf("update gaf fout: %v", err)
	}
	op := grouped["ad_group_criterion"][0]
	update, _ := op["update"].(map[string]any)
	if update["resourceName"] != "customers/3070436491/adGroupCriteria/111~222" {
		t.Errorf("resourceName = %v", update["resourceName"])
	}
	if _, ok := update["criterionId"]; ok {
		t.Error("criterionId hoort niet meer in de body te staan")
	}
	if op["updateMask"] != "status" {
		t.Errorf("updateMask = %q, want \"status\"", op["updateMask"])
	}
}

// bo's afspraak: campagnes, ad groups en budgetten niet verwijderen;
// zoekwoorden, negatieve zoekwoorden en advertenties wel.
func TestBuildAdsMutateRemovePolicy(t *testing.T) {
	for _, resource := range []string{"campaign", "ad_group", "campaign_budget", "asset", "shared_set"} {
		_, _, err := buildAdsMutateOperations(mutateCustomer, []MutateOperation{{
			Action:       "remove",
			Resource:     resource,
			ResourceName: "customers/3070436491/x/2",
		}})
		if err == nil {
			t.Fatalf("remove op %s hoort geweigerd te worden", resource)
		}
		if !strings.Contains(err.Error(), "niet verwijderen") {
			t.Errorf("fout voor %s = %v", resource, err)
		}
	}
	for _, resource := range []string{"ad_group_criterion", "campaign_criterion", "ad_group_ad"} {
		if _, _, err := buildAdsMutateOperations(mutateCustomer, []MutateOperation{{
			Action:       "remove",
			Resource:     resource,
			ResourceName: "customers/3070436491/adGroupCriteria/2~3",
		}}); err != nil {
			t.Errorf("%s verwijderen hoort te mogen: %v", resource, err)
		}
	}
}

func TestBuildAdsMutateRejectsUnknownResourceAndAction(t *testing.T) {
	if _, _, err := buildAdsMutateOperations(mutateCustomer, []MutateOperation{{
		Action:   "create",
		Resource: "search_term_view",
		Object:   map[string]any{"x": 1},
	}}); err == nil || !strings.Contains(err.Error(), "onbekende resource") {
		t.Errorf("onbekende resource = %v, want een duidelijke fout", err)
	}
	if _, _, err := buildAdsMutateOperations(mutateCustomer, []MutateOperation{{
		Action:   "delete",
		Resource: "campaign_criterion",
	}}); err == nil || !strings.Contains(err.Error(), "onbekende action") {
		t.Errorf("onbekende action = %v", err)
	}
	if _, _, err := buildAdsMutateOperations(mutateCustomer, nil); err == nil {
		t.Error("zonder operaties hoort er een fout te komen")
	}
}

// Status REMOVED is op campagneniveau ook een verwijdering.
func TestBuildAdsMutateBlocksRemovedStatusOnCampaign(t *testing.T) {
	_, _, err := buildAdsMutateOperations(mutateCustomer, []MutateOperation{{
		Action:       "update",
		Resource:     "campaign",
		ResourceName: "customers/3070436491/campaigns/24337488185",
		Object:       map[string]any{"status": "REMOVED"},
	}})
	if err == nil || !strings.Contains(err.Error(), "PAUSED") {
		t.Errorf("REMOVED op een campagne = %v, want een verwijzing naar PAUSED", err)
	}
}

// Accountdoelen (customer_conversion_goal) zijn de plek waar "meetellen voor
// bieden" zit. De API kent geen create — het is een upsert op category~origin —
// en verwijderen mag niet.
func TestBuildAdsMutateCustomerConversionGoal(t *testing.T) {
	if _, _, err := buildAdsMutateOperations(mutateCustomer, []MutateOperation{{
		Action:   "create",
		Resource: "customer_conversion_goal",
		Object:   map[string]any{"category": "SUBMIT_LEAD_FORM", "origin": "WEBSITE", "biddable": true},
	}}); err == nil {
		t.Error("een accountdoel aanmaken kan niet via de API en hoort dus geweigerd te worden")
	}

	grouped, _, err := buildAdsMutateOperations(mutateCustomer, []MutateOperation{{
		Action:       "update",
		Resource:     "customer_conversion_goal",
		ResourceName: "customers/3070436491/customerConversionGoals/SUBMIT_LEAD_FORM~WEBSITE",
		Object:       map[string]any{"biddable": true},
	}})
	if err != nil {
		t.Fatalf("doel bijwerken gaf fout: %v", err)
	}
	update, _ := grouped["customer_conversion_goal"][0]["update"].(map[string]any)
	if update["biddable"] != true {
		t.Errorf("update-body = %#v", update)
	}
	if grouped["customer_conversion_goal"][0]["updateMask"] != "biddable" {
		t.Errorf("updateMask = %v", grouped["customer_conversion_goal"][0]["updateMask"])
	}
	name, err := AdsMutationResourceName(mutateCustomer, "customer_conversion_goal", "", "SUBMIT_LEAD_FORM~WEBSITE")
	if err != nil || name != "customers/3070436491/customerConversionGoals/SUBMIT_LEAD_FORM~WEBSITE" {
		t.Errorf("doel-naam = %q, err = %v", name, err)
	}
	if _, _, err := buildAdsMutateOperations(mutateCustomer, []MutateOperation{{
		Action:       "remove",
		Resource:     "customer_conversion_goal",
		ResourceName: "customers/3070436491/customerConversionGoals/SUBMIT_LEAD_FORM~WEBSITE",
	}}); err == nil {
		t.Error("een accountdoel verwijderen hoort geweigerd te worden")
	}
}

func TestAdsMutationResourceNameFormats(t *testing.T) {
	name, err := AdsMutationResourceName(mutateCustomer, "ad_group", "", "111")
	if err != nil || name != "customers/3070436491/adGroups/111" {
		t.Errorf("ad group-naam = %q, err = %v", name, err)
	}
	name, err = AdsMutationResourceName(mutateCustomer, "ad_group_ad", "111", "999")
	if err != nil || name != "customers/3070436491/adGroupAds/111~999" {
		t.Errorf("advertentie-naam = %q, err = %v", name, err)
	}
	if _, err := AdsMutationResourceName(mutateCustomer, "ad_group_ad", "", "999"); err == nil {
		t.Error("een advertentie zonder ad group hoort te falen")
	}
	if _, err := AdsMutationResourceName(mutateCustomer, "campaign", "", ""); err == nil {
		t.Error("een campagne zonder id hoort te falen")
	}
}

// Alle validatie hoort vóór het HTTP-verkeer te gebeuren, dus zonder netwerk.
func TestAdsWriteValidationBeforeHTTP(t *testing.T) {
	ctx := context.Background()
	client := &Client{}

	if _, err := client.SetAdsStatus(ctx, mutateCustomer, "campaign", "customers/1/campaigns/2", "REMOVED", MutateOptions{}, ""); err == nil {
		t.Error("REMOVED op een campagne hoort geweigerd te worden")
	}
	if _, err := client.SetAdsStatus(ctx, mutateCustomer, "ad_group", "customers/1/adGroups/2", "ACTIVE", MutateOptions{}, ""); err == nil {
		t.Error("een onbekende status hoort geweigerd te worden")
	}
	if _, err := client.SetConversionActionSettings(ctx, mutateCustomer, "123", nil, nil, "", MutateOptions{}, ""); err == nil {
		t.Error("een lege conversiewijziging hoort geweigerd te worden")
	}
	if _, err := client.SetConversionActionSettings(ctx, mutateCustomer, "123", nil, nil, "REMOVED", MutateOptions{}, ""); err == nil {
		t.Error("conversieacties verwijderen hoort geweigerd te worden")
	}
	if _, err := client.AddKeywords(ctx, mutateCustomer, "", []KeywordInput{{Text: "dj"}}, MutateOptions{}, ""); err == nil {
		t.Error("zoekwoorden zonder ad group horen geweigerd te worden")
	}
	if _, err := client.AddKeywords(ctx, mutateCustomer, "111", []KeywordInput{{Text: "dj", MatchType: "FUZZY"}}, MutateOptions{}, ""); err == nil {
		t.Error("een onbekend match_type hoort geweigerd te worden")
	}
}
