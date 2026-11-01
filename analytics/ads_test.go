package analytics

import (
	"strings"
	"testing"
)

// De API antwoordt óf met één JSON-array (soms pretty-printed) óf met een
// stroom losse objecten. Regel-voor-regel parsen brak op de eerste vorm.
func TestParseAdsStreamArrayShape(t *testing.T) {
	pretty := []byte(`[
  {
    "results": [
      {"campaign": {"id": "24337488185", "name": "Winteractie"}, "metrics": {"clicks": "187"}},
      {"campaign": {"id": "24343546045", "name": "Bedrijfsfeest"}, "metrics": {"clicks": "143"}}
    ]
  }
]`)
	rows, err := parseAdsStream(pretty)
	if err != nil {
		t.Fatalf("array-vorm gaf fout: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if got := getPath(rows[0], "campaign.name"); got != "Winteractie" {
		t.Errorf("eerste rij = %v, want Winteractie", got)
	}
}

func TestParseAdsStreamNDJSONShape(t *testing.T) {
	nd := []byte("{\"results\":[{\"campaign\":{\"id\":\"1\"}}]}\n{\"results\":[{\"campaign\":{\"id\":\"2\"}}]}\n")
	rows, err := parseAdsStream(nd)
	if err != nil {
		t.Fatalf("NDJSON gaf fout: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if got := getPath(rows[1], "campaign.id"); got != "2" {
		t.Errorf("tweede rij = %v, want 2", got)
	}
}

func TestParseAdsStreamError(t *testing.T) {
	body := []byte(`[{"error": {"message": "Request contains an invalid argument.", "status": "INVALID_ARGUMENT"}}]`)
	if _, err := parseAdsStream(body); err == nil || !strings.Contains(err.Error(), "invalid argument") {
		t.Fatalf("foutrespons niet herkend: %v", err)
	}
	if rows, err := parseAdsStream(nil); err != nil || rows != nil {
		t.Errorf("lege respons moet stil leeg blijven: %v %v", rows, err)
	}
}

// De API-eis: metrics vereisen een eindige periode, en die conditie moet vóór
// ORDER BY/LIMIT staan (anders is de GAQL ongeldig).
func TestBuildAdsQuery(t *testing.T) {
	fields := []string{"campaign_name", "spend", "clicks", "conversions"}
	conditions := []string{"segments.date BETWEEN '2026-09-30' AND '2026-10-29'"}

	query, paths, err := buildAdsQuery(fields, conditions, []string{"metrics.cost_micros DESC"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT campaign.name, metrics.cost_micros, metrics.clicks, metrics.conversions FROM campaign " +
		"WHERE segments.date BETWEEN '2026-09-30' AND '2026-10-29' " +
		"ORDER BY metrics.cost_micros DESC LIMIT 20"
	if query != want {
		t.Errorf("query =\n%s\nwant =\n%s", query, want)
	}
	if paths["spend"] != "metrics.cost_micros" {
		t.Errorf("pad voor spend = %q", paths["spend"])
	}
	if strings.Contains(query, "LAST_30_DAYS") {
		t.Error("er hoort geen tweede datumconditie bij te komen als de caller er al een geeft")
	}

	query, _, err = buildAdsQuery(fields, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "WHERE segments.date DURING LAST_30_DAYS") {
		t.Errorf("zonder periode hoort er een standaardperiode in te staan: %s", query)
	}
	if strings.Index(query, "DURING") > strings.Index(query, "SELECT")+len(query) {
		t.Error("datumconditie hoort in de WHERE-clausule te staan")
	}

	if _, _, err := buildAdsQuery([]string{"bestaat_niet"}, nil, nil, 0); err == nil {
		t.Error("onbekend veld hoort een fout te geven")
	}
}

// Google's REST-JSON gebruikt camelCase, GAQL snake_case. Zonder deze vertaling
// kwam metrics.cost_micros leeg terug (kosten op nul).
func TestToCamelPath(t *testing.T) {
	cases := map[string]string{
		"metrics.cost_micros":             "metrics.costMicros",
		"campaign.name":                   "campaign.name",
		"ad_group_criterion.keyword.text": "adGroupCriterion.keyword.text",
		"campaign_budget.amount_micros":   "campaignBudget.amountMicros",
		"search_term_view.search_term":    "searchTermView.searchTerm",
	}
	for in, want := range cases {
		if got := toCamelPath(in); got != want {
			t.Errorf("toCamelPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGetAdsPathAcceptsBothSpellings(t *testing.T) {
	row := map[string]any{
		"campaign": map[string]any{"name": "Winteractie", "status": "ENABLED"},
		"metrics":  map[string]any{"costMicros": "181080000", "clicks": "187"},
	}
	if got := convertAdsValue("metrics.cost_micros", getAdsPath(row, "metrics.cost_micros")); got != 181.08 {
		t.Errorf("kosten = %v, want 181.08", got)
	}
	if got := convertAdsValue("metrics.clicks", getAdsPath(row, "metrics.clicks")); got != float64(187) {
		t.Errorf("klikken = %v, want 187", got)
	}
	if got := convertAdsValue("campaign.name", getAdsPath(row, "campaign.name")); got != "Winteractie" {
		t.Errorf("naam = %v, want Winteractie", got)
	}
}

// Een naam of zoekterm mag nooit door een getalparser gaan: dat maakte er null van.
func TestConvertAdsValueKeepsStrings(t *testing.T) {
	for _, path := range []string{
		"campaign.name", "campaign.status", "segments.date", "campaign.id",
		"search_term_view.search_term", "ad_group_criterion.keyword.text",
		"ad_group_criterion.keyword.match_type",
	} {
		if got := convertAdsValue(path, "Winteractie"); got != "Winteractie" {
			t.Errorf("convertAdsValue(%q) = %#v, wil de tekst ongewijzigd terug", path, got)
		}
	}
}
