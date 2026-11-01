package analytics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const adsBaseURL = "https://googleads.googleapis.com/v25"

// adsFields maps the field names the app asks for onto Google Ads API fields.
var adsFields = map[string]string{
	"campaign_id":         "campaign.id",
	"campaign_name":       "campaign.name",
	"campaign_status":     "campaign.status",
	"status":              "campaign.status",
	"campaign_budget":     "campaign_budget.amount_micros",
	"daily_budget":        "campaign_budget.amount_micros",
	"ad_group_id":         "ad_group.id",
	"ad_group_name":       "ad_group.name",
	"ad_group_status":     "ad_group.status",
	"keyword_id":          "ad_group_criterion.criterion_id",
	"keyword_text":        "ad_group_criterion.keyword.text",
	"keyword":             "ad_group_criterion.keyword.text",
	"match_type":          "ad_group_criterion.keyword.match_type",
	"quality_score":       "ad_group_criterion.quality_info.quality_score",
	"search_term":         "search_term_view.search_term",
	"query":               "search_term_view.search_term",
	"date":                "segments.date",
	"device":              "segments.device",
	"clicks":              "metrics.clicks",
	"impressions":         "metrics.impressions",
	"ctr":                 "metrics.ctr",
	"conversions":         "metrics.conversions",
	"conversions_value":   "metrics.conversions_value",
	"cost":                "metrics.cost_micros",
	"spend":               "metrics.cost_micros",
	"average_cpc":         "metrics.average_cpc",
	"cost_per_conversion": "metrics.cost_per_conversion",
}

// adsResources are the FROM resources we support, keyed by the resource the
// caller's fields imply.
var adsResources = map[string]string{
	"campaign.id":                                   "campaign",
	"campaign.name":                                 "campaign",
	"campaign.status":                               "campaign",
	"campaign_budget.amount_micros":                 "campaign",
	"ad_group.id":                                   "ad_group",
	"ad_group.name":                                 "ad_group",
	"ad_group.status":                               "ad_group",
	"ad_group_criterion.criterion_id":               "keyword_view",
	"ad_group_criterion.keyword.text":               "keyword_view",
	"ad_group_criterion.keyword.match_type":         "keyword_view",
	"ad_group_criterion.quality_info.quality_score": "keyword_view",
	"search_term_view.search_term":                  "search_term_view",
}

// adsHeaders returns the extra headers the Ads API needs. A developer token is
// no longer required for OAuth-based access (Google retired standalone tokens
// in 2026; the access level now follows the Cloud project), so it is only sent
// when explicitly configured.
func adsHeaders(loginCustomerID string) map[string]string {
	h := map[string]string{"X-Goog-Api-Client": "analytics-mcp/0.1"}
	if token := strings.TrimSpace(os.Getenv("GOOGLE_ADS_DEVELOPER_TOKEN")); token != "" {
		h["developer-token"] = token
	}
	if id := strings.TrimSpace(firstNonEmpty(loginCustomerID, os.Getenv("GOOGLE_ADS_LOGIN_CUSTOMER_ID"))); id != "" {
		h["login-customer-id"] = strings.ReplaceAll(id, "-", "")
	}
	return h
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func normaliseCustomerID(id string) string {
	return strings.ReplaceAll(strings.TrimSpace(id), "-", "")
}

// AdsQuery runs a GAQL query and returns only the rows.
func (c *Client) AdsQuery(ctx context.Context, customerID string, fields, conditions, orderings []string, limit int, loginCustomerID string) ([]map[string]any, error) {
	rows, _, err := c.AdsQueryWithQuery(ctx, customerID, fields, conditions, orderings, limit, loginCustomerID)
	return rows, err
}

// AdsQueryWithQuery runs a GAQL query built from the requested fields and
// conditions and also returns the generated GAQL, which the tests and the app
// log for debugging.
func (c *Client) AdsQueryWithQuery(ctx context.Context, customerID string, fields, conditions, orderings []string, limit int, loginCustomerID string) ([]map[string]any, string, error) {
	customerID = normaliseCustomerID(customerID)
	if customerID == "" {
		return nil, "", fmt.Errorf("google_ads: account (customer id) is required")
	}
	requested := toStringSlice(fields)
	if len(requested) == 0 {
		return nil, "", fmt.Errorf("google_ads: at least one field is required")
	}

	resource := ""
	selects := make([]string, 0, len(requested))
	paths := make(map[string]string, len(requested))
	for _, f := range requested {
		api, ok := adsFields[f]
		if !ok {
			return nil, "", fmt.Errorf("google_ads: onbekend veld %q", f)
		}
		paths[f] = api
		selects = append(selects, api)
		if resource == "" {
			resource = adsResources[api]
		} else if r, ok := adsResources[api]; ok && r != resource {
			// keyword_view and search_term_view both carry campaign metrics; in
			// that case the more specific resource wins.
			if resource == "campaign" {
				resource = r
			}
		}
	}
	if resource == "" {
		resource = "campaign"
	}

	query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(selects, ", "), resource)
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	if len(orderings) > 0 {
		query += " ORDER BY " + strings.Join(orderings, ", ")
	}
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	if resource == "campaign" && !containsDateCondition(conditions) && hasMetrics(selects) {
		// Ads requires a finite date range as soon as metrics are selected.
		query += " AND segments.date DURING LAST_30_DAYS"
	}

	rows, err := c.adsSearch(ctx, customerID, query, loginCustomerID)
	if err != nil {
		return nil, query, err
	}

	out := make([]map[string]any, 0, len(rows))
	for _, raw := range rows {
		row := map[string]any{}
		for field, path := range paths {
			row[field] = convertAdsValue(path, getPath(raw, path))
		}
		out = append(out, row)
	}
	return out, query, nil
}

func containsDateCondition(conditions []string) bool {
	for _, c := range conditions {
		if strings.Contains(strings.ToLower(c), "segments.date") {
			return true
		}
	}
	return false
}

func hasMetrics(selects []string) bool {
	for _, s := range selects {
		if strings.HasPrefix(s, "metrics.") {
			return true
		}
	}
	return false
}

// adsSearch issues a googleAds:searchStream call and flattens the streamed
// response chunks into one slice of result rows.
func (c *Client) adsSearch(ctx context.Context, customerID, query string, loginCustomerID string) ([]map[string]any, error) {
	body := map[string]string{"query": query}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/customers/%s/googleAds:searchStream", adsBaseURL, customerID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range adsHeaders(loginCustomerID) {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call google ads: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("google ads returned HTTP %d: %s", resp.StatusCode, truncate(string(raw), 900))
	}

	var rows []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 1<<20), 32<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// searchStream can also answer as a single JSON array.
		if strings.HasPrefix(line, "[") {
			var chunk []struct {
				Results []map[string]any `json:"results"`
			}
			if err := json.Unmarshal([]byte(line), &chunk); err != nil {
				return nil, fmt.Errorf("decode google ads response: %w", err)
			}
			for _, c := range chunk {
				rows = append(rows, c.Results...)
			}
			continue
		}
		var chunk struct {
			Results []map[string]any `json:"results"`
			Error   *struct {
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			return nil, fmt.Errorf("decode google ads response: %w", err)
		}
		if chunk.Error != nil {
			return nil, fmt.Errorf("google ads error: %s (%s)", chunk.Error.Message, chunk.Error.Status)
		}
		rows = append(rows, chunk.Results...)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

// getPath reads a dotted path out of a decoded JSON object.
func getPath(obj map[string]any, path string) any {
	var current any = obj
	for _, part := range strings.Split(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = m[part]
		if !ok {
			return nil
		}
	}
	return current
}

// adsMicrosPaths lists the money fields whose API name does not end in
// _micros. average_cpc and cost_per_conversion are micros too; without this
// list a CPC of €0,97 shows up as 968342.
var adsMicrosPaths = map[string]bool{
	"metrics.average_cpc":              true,
	"metrics.average_cpm":              true,
	"metrics.cost_per_conversion":      true,
	"metrics.cost_per_all_conversions": true,
	"metrics.average_cost":             true,
}

// convertAdsValue turns API values into the shape the app expects: micros to
// whole units, numeric strings to numbers, enums left as strings.
func convertAdsValue(path string, value any) any {
	if value == nil {
		return nil
	}
	if strings.HasSuffix(path, "_micros") || adsMicrosPaths[path] {
		return micros(value)
	}
	switch path {
	case "campaign.status", "ad_group.status", "segments.device",
		"ad_group_criterion.keyword.match_type",
		"ad_group_criterion.quality_info.quality_score":
		return value
	case "campaign.id", "ad_group.id", "ad_group_criterion.criterion_id", "segments.date":
		return value
	}
	return num(value)
}

// SetCampaignStatus pauses or enables one campaign.
func (c *Client) SetCampaignStatus(ctx context.Context, customerID, campaignID, status, loginCustomerID string) error {
	customerID = normaliseCustomerID(customerID)
	campaignID = strings.TrimSpace(campaignID)
	if customerID == "" || campaignID == "" {
		return fmt.Errorf("google_ads: customer id and campaign id are required")
	}
	status = strings.ToUpper(strings.TrimSpace(status))
	if status != "PAUSED" && status != "ENABLED" {
		return fmt.Errorf("google_ads: status must be PAUSED or ENABLED, got %q", status)
	}

	body := map[string]any{
		"operations": []map[string]any{{
			"updateMask": "status",
			"update": map[string]any{
				"resourceName": fmt.Sprintf("customers/%s/campaigns/%s", customerID, campaignID),
				"status":       status,
			},
		}},
	}
	u := fmt.Sprintf("%s/customers/%s/campaigns:mutate", adsBaseURL, customerID)
	return c.postJSON(ctx, u, adsHeaders(loginCustomerID), body, nil)
}

// PushNegativeKeywords adds negative keywords to one campaign. matchType is
// BROAD, PHRASE or EXACT.
func (c *Client) PushNegativeKeywords(ctx context.Context, customerID, campaignID string, keywords []string, matchType, loginCustomerID string) (int, error) {
	customerID = normaliseCustomerID(customerID)
	campaignID = strings.TrimSpace(campaignID)
	if customerID == "" || campaignID == "" {
		return 0, fmt.Errorf("google_ads: customer id and campaign id are required")
	}
	matchType = strings.ToUpper(strings.TrimSpace(matchType))
	switch matchType {
	case "", "BROAD":
		matchType = "BROAD"
	case "PHRASE", "EXACT":
	default:
		return 0, fmt.Errorf("google_ads: matchType must be BROAD, PHRASE or EXACT, got %q", matchType)
	}

	operations := make([]map[string]any, 0, len(keywords))
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		operations = append(operations, map[string]any{
			"create": map[string]any{
				"campaign": fmt.Sprintf("customers/%s/campaigns/%s", customerID, campaignID),
				"negative": true,
				"keyword":  map[string]any{"text": keyword, "matchType": matchType},
			},
		})
	}
	if len(operations) == 0 {
		return 0, fmt.Errorf("google_ads: no keywords given")
	}

	body := map[string]any{"operations": operations}
	u := fmt.Sprintf("%s/customers/%s/campaignCriteria:mutate", adsBaseURL, customerID)
	if err := c.postJSON(ctx, u, adsHeaders(loginCustomerID), body, nil); err != nil {
		return 0, err
	}
	return len(operations), nil
}

// SetCampaignBudget sets the daily budget of one campaign's budget.
func (c *Client) SetCampaignBudget(ctx context.Context, customerID, budgetResourceName string, amountMicros int64, loginCustomerID string) error {
	customerID = normaliseCustomerID(customerID)
	if customerID == "" || strings.TrimSpace(budgetResourceName) == "" {
		return fmt.Errorf("google_ads: customer id and budget resource name are required")
	}
	body := map[string]any{
		"operations": []map[string]any{{
			"updateMask": "amount_micros",
			"update": map[string]any{
				"resourceName": budgetResourceName,
				"amountMicros": fmt.Sprintf("%d", amountMicros),
			},
		}},
	}
	u := fmt.Sprintf("%s/customers/%s/campaignBudgets:mutate", adsBaseURL, customerID)
	return c.postJSON(ctx, u, adsHeaders(loginCustomerID), body, nil)
}

// KeywordIdeas returns Keyword Planner ideas for one or more seed keywords.
// Unlike the field-based AdsQuery this uses the KeywordPlanIdeaService, which is
// the only place the planner metrics (volume, competition, top-of-page bids)
// exist.
func (c *Client) KeywordIdeas(ctx context.Context, customerID string, seeds []string, geo, language, pageURL, loginCustomerID string) ([]map[string]any, error) {
	customerID = normaliseCustomerID(customerID)
	if customerID == "" {
		return nil, fmt.Errorf("google_ads: account (customer id) is required")
	}
	seeds = toStringSlice(seeds)
	if len(seeds) == 0 {
		return nil, fmt.Errorf("google_ads: keyword_seeds is required for keyword ideas")
	}
	if geo == "" {
		geo = "geoTargetConstants/2840" // Netherlands
	}
	if !strings.Contains(geo, "/") {
		geo = "geoTargetConstants/" + strings.TrimSpace(geo)
	}
	if language == "" {
		language = "languageConstants/1010" // Dutch
	}
	if !strings.Contains(language, "/") {
		language = "languageConstants/" + strings.TrimSpace(language)
	}

	body := map[string]any{
		"language":             language,
		"geoTargetConstants":   []string{geo},
		"keywordSeed":          map[string]any{"keywords": seeds},
		"includeAdultKeywords": false,
	}
	if strings.TrimSpace(pageURL) != "" {
		body["urlSeed"] = map[string]any{"url": strings.TrimSpace(pageURL)}
	}

	var resp struct {
		Results []struct {
			Text               string `json:"text"`
			KeywordIdeaMetrics struct {
				AvgMonthlySearches   string `json:"avgMonthlySearches"`
				MonthlySearchVolumes []struct {
					Year            string `json:"year"`
					Month           string `json:"month"`
					MonthlySearches string `json:"monthlySearches"`
				} `json:"monthlySearchVolumes"`
				Competition            string `json:"competition"`
				CompetitionIndex       string `json:"competitionIndex"`
				LowTopOfPageBidMicros  string `json:"lowTopOfPageBidMicros"`
				HighTopOfPageBidMicros string `json:"highTopOfPageBidMicros"`
			} `json:"keywordIdeaMetrics"`
		} `json:"results"`
	}
	u := fmt.Sprintf("%s/customers/%s:generateKeywordIdeas", adsBaseURL, customerID)
	if err := c.postJSON(ctx, u, adsHeaders(loginCustomerID), body, &resp); err != nil {
		return nil, err
	}

	rows := make([]map[string]any, 0, len(resp.Results))
	for _, r := range resp.Results {
		m := r.KeywordIdeaMetrics
		row := map[string]any{
			"keyword_text": r.Text,
			"keyword":      r.Text,
			// Windsor-compatible names, so existing readers keep working.
			"monthly_search_volume":       num(m.AvgMonthlySearches),
			"avg_monthly_searches":        num(m.AvgMonthlySearches),
			"competition":                 m.Competition,
			"competition_index":           num(m.CompetitionIndex),
			"low_top_of_page_bid":         micros(m.LowTopOfPageBidMicros),
			"high_top_of_page_bid":        micros(m.HighTopOfPageBidMicros),
			"low_top_of_page_bid_micros":  num(m.LowTopOfPageBidMicros),
			"high_top_of_page_bid_micros": num(m.HighTopOfPageBidMicros),
		}
		if len(m.MonthlySearchVolumes) > 0 {
			last := m.MonthlySearchVolumes[len(m.MonthlySearchVolumes)-1]
			row["month"] = last.Year + "-" + last.Month
			row["monthly_searches"] = num(last.MonthlySearches)
		}
		rows = append(rows, row)
	}
	return rows, nil
}
