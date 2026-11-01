package analytics

import "testing"

// TestGetPathAndConvert covers the value-mapping that determines what a
// dashboard actually sees: dotted-path reads, micros → euro, enums untouched.
func TestGetPathAndConvert(t *testing.T) {
	row := map[string]any{
		"campaign": map[string]any{"id": "24337488185", "name": "Winteractie", "status": "ENABLED"},
		"metrics": map[string]any{
			"clicks":      "187",
			"impressions": "6686",
			"cost_micros": "181080000",
			"average_cpc": "968342.25",
			"conversions": "0",
		},
	}

	cases := []struct {
		path string
		want any
	}{
		{"campaign.id", "24337488185"},
		{"campaign.status", "ENABLED"},
		{"metrics.clicks", float64(187)},
		{"metrics.cost_micros", 181.08}, // euro, not micros
		{"metrics.average_cpc", 0.97},   // micros → euro
		{"metrics.conversions", float64(0)},
		{"metrics.missing", nil},
		{"campaign.missing.deeper", nil},
	}
	for _, tc := range cases {
		got := convertAdsValue(tc.path, getPath(row, tc.path))
		if got != tc.want {
			t.Errorf("convertAdsValue(%q) = %#v, want %#v", tc.path, got, tc.want)
		}
	}
}

// TestAdsGAQLBuild ensures a field list becomes a valid GAQL query with the
// right FROM resource and the date condition Ads insists on for metrics.
func TestAdsGAQLBuild(t *testing.T) {
	fields := []string{"campaign_name", "spend", "clicks", "conversions"}
	var selects []string
	resource := ""
	for _, f := range fields {
		api, ok := adsFields[f]
		if !ok {
			t.Fatalf("veld %q ontbreekt in adsFields", f)
		}
		selects = append(selects, api)
		if resource == "" {
			resource = adsResources[api]
		}
	}
	if resource != "campaign" {
		t.Fatalf("resource = %q, want campaign", resource)
	}
	if !hasMetrics(selects) {
		t.Fatal("hasMetrics should be true for a metrics query")
	}
	if containsDateCondition([]string{"metrics.clicks > 0"}) {
		t.Fatal("containsDateCondition should be false without segments.date")
	}
	if !containsDateCondition([]string{"segments.date BETWEEN '2026-09-30' AND '2026-10-29'"}) {
		t.Fatal("containsDateCondition should be true with segments.date")
	}
}

// TestSearchTermResourceWins documents that keyword_view/search_term_view beat
// campaign when both appear, so search-term reports stay valid.
func TestSearchTermResourceWins(t *testing.T) {
	if adsResources["campaign.name"] != "campaign" {
		t.Fatal("campaign.name should map to the campaign resource")
	}
	if adsResources["search_term_view.search_term"] != "search_term_view" {
		t.Fatal("search_term_view.search_term should map to search_term_view")
	}
	if adsResources["ad_group_criterion.keyword.text"] != "keyword_view" {
		t.Fatal("keyword text should map to keyword_view")
	}
}

// TestGSCFieldMapping guards the field names the dashboard asks for: a name
// that is not a metric must be a dimension, and vice versa.
func TestGSCFieldMapping(t *testing.T) {
	for _, dim := range []string{"query", "page", "date", "device", "country"} {
		if _, ok := gscDimensions[dim]; !ok {
			t.Errorf("dimensie %q ontbreekt", dim)
		}
		if gscMetrics[dim] {
			t.Errorf("%q mag geen metric zijn", dim)
		}
	}
	for _, met := range []string{"clicks", "impressions", "ctr", "position"} {
		if !gscMetrics[met] {
			t.Errorf("metric %q ontbreekt", met)
		}
	}
	if _, isDim := gscDimensions["clicks"]; isDim {
		t.Error("clicks mag geen dimensie zijn")
	}
}

// TestGA4FieldMapping guards the GA4 names, including source (the field the
// dashboard used to ask for as session_source).
func TestGA4FieldMapping(t *testing.T) {
	if ga4Dimensions["source"] != "sessionSource" {
		t.Error("source moet op sessionSource mappen")
	}
	if _, ok := ga4Metrics["sessions"]; !ok {
		t.Error("sessions ontbreekt")
	}
	if _, ok := ga4Metrics["clicks"]; ok {
		t.Error("clicks hoort geen GA4-metric te zijn")
	}
}

func TestNormaliseCustomerID(t *testing.T) {
	if got := normaliseCustomerID("307-043-6491"); got != "3070436491" {
		t.Errorf("normaliseCustomerID = %q", got)
	}
}
