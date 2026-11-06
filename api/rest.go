// Package api exposes a flat JSON facade over the Google APIs, shaped so a
// dashboard can keep the same request/response contract it had with a
// third-party aggregator. Every call is authenticated with the same bearer
// token as the MCP endpoint.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"analytics-mcp/analytics"
)

// Options carries connector-specific extras (the keyword-plan options of the
// former aggregator live here so existing clients keep working unchanged).
type Options struct {
	KeywordSeeds string `json:"keyword_seeds,omitempty"`
	Geo          string `json:"geo_target_constants,omitempty"`
	Language     string `json:"language,omitempty"`
	PageURL      string `json:"page_url,omitempty"`
	Network      string `json:"keyword_plan_network,omitempty"`
}

// QueryRequest is the read contract: a connector, an account, the fields you
// want back, a period and an optional grouping/limit.
type QueryRequest struct {
	Connector       string   `json:"connector"`
	Account         string   `json:"account"`
	Fields          []string `json:"fields"`
	DateFrom        string   `json:"date_from"`
	DateTo          string   `json:"date_to"`
	GroupBy         []string `json:"group_by"`
	Conditions      []string `json:"conditions"`
	Orderings       []string `json:"orderings"`
	Limit           int      `json:"limit"`
	Options         Options  `json:"options"`
	LoginCustomerID string   `json:"login_customer_id,omitempty"`
}

// QueryResponse mirrors the previous shape: status, data, total_rows.
type QueryResponse struct {
	Status    string           `json:"status"`
	Data      []map[string]any `json:"data"`
	TotalRows int              `json:"total_rows"`
	Connector string           `json:"connector"`
	Account   string           `json:"account,omitempty"`
	Query     string           `json:"query,omitempty"`
}

// ActionRequest is the write contract: pause/enable a campaign, push negative
// keywords, set a budget.
type ActionRequest struct {
	Connector       string         `json:"connector"`
	Account         string         `json:"account"`
	Action          string         `json:"action"`
	Params          map[string]any `json:"params"`
	LoginCustomerID string         `json:"login_customer_id,omitempty"`
}

// ActionResponse reports what a write did. Newly created entities carry their
// id so the caller can chain (campagne aanmaken → ad group aanmaken).
type ActionResponse struct {
	Status        string   `json:"status"`
	Action        string   `json:"action"`
	Affected      int      `json:"affected"`
	Message       string   `json:"message,omitempty"`
	CampaignID    string   `json:"campaign_id,omitempty"`
	AdGroupID     string   `json:"ad_group_id,omitempty"`
	ResourceNames []string `json:"resource_names,omitempty"`
	Validated     bool     `json:"validated_only,omitempty"`
}

// SupportedConnectors are the connector ids this facade understands.
var SupportedConnectors = []string{"googleanalytics4", "searchconsole", "google_ads"}

// knownAdsActions are the write actions of the Google Ads connector. The
// dashboard calls them by name, so a typo should fail fast with the list of
// what does work instead of an opaque API error.
var knownAdsActions = map[string]bool{
	"pause_campaign":         true,
	"enable_campaign":        true,
	"push_negative_keywords": true,
	"set_campaign_budget":    true,
	"create_campaign":        true,
	"create_ad_group":        true,
	"mutate":                 true,
	"add_keywords":           true,
	"set_keyword_status":     true,
	"remove_keyword":         true,
	"set_ad_group_status":    true,
	"set_ad_status":          true,
	"set_conversion_action":  true,
}

// sortedAdsActions lists the supported Google Ads actions for error messages.
func sortedAdsActions() []string {
	names := make([]string, 0, len(knownAdsActions))
	for name := range knownAdsActions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RunQuery executes a read request against the right Google API.
func RunQuery(ctx context.Context, req QueryRequest) (QueryResponse, error) {
	client, err := analytics.NewClient(ctx)
	if err != nil {
		return QueryResponse{}, err
	}

	connector := strings.ToLower(strings.TrimSpace(req.Connector))
	switch connector {
	case "googleanalytics4", "ga4", "analytics":
		rows, err := client.GA4Query(ctx, req.Account, req.Fields, req.GroupBy, req.DateFrom, req.DateTo, req.Limit)
		if err != nil {
			return QueryResponse{}, err
		}
		return QueryResponse{Status: "done", Data: rows, TotalRows: len(rows), Connector: "googleanalytics4"}, nil

	case "searchconsole", "gsc":
		rows, err := client.GSCQuery(ctx, req.Account, req.Fields, req.GroupBy, req.DateFrom, req.DateTo, req.Limit)
		if err != nil {
			return QueryResponse{}, err
		}
		return QueryResponse{Status: "done", Data: rows, TotalRows: len(rows), Connector: "searchconsole"}, nil

	case "google_ads", "ads":
		if strings.TrimSpace(req.Options.KeywordSeeds) != "" {
			seeds := strings.Split(req.Options.KeywordSeeds, ",")
			rows, err := client.KeywordIdeas(ctx, req.Account, seeds, req.Options.Geo, req.Options.Language, req.Options.PageURL, req.LoginCustomerID)
			if err != nil {
				return QueryResponse{}, err
			}
			return QueryResponse{Status: "done", Data: rows, TotalRows: len(rows), Connector: "google_ads"}, nil
		}
		rows, query, err := client.AdsQueryWithQuery(ctx, req.Account, req.Fields, req.Conditions, req.Orderings, req.Limit, req.LoginCustomerID)
		if err != nil {
			return QueryResponse{}, err
		}
		return QueryResponse{Status: "done", Data: rows, TotalRows: len(rows), Connector: "google_ads", Query: query}, nil
	}
	return QueryResponse{}, fmt.Errorf("onbekende connector %q (kies uit %s)", req.Connector, strings.Join(SupportedConnectors, ", "))
}

// RunAction executes a write request.
func RunAction(ctx context.Context, req ActionRequest) (ActionResponse, error) {
	client, err := analytics.NewClient(ctx)
	if err != nil {
		return ActionResponse{}, err
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	switch strings.ToLower(strings.TrimSpace(req.Connector)) {
	case "google_ads", "ads", "":
		if !knownAdsActions[action] {
			return ActionResponse{}, fmt.Errorf("onbekende actie %q voor google_ads (kies uit: %s)",
				req.Action, strings.Join(sortedAdsActions(), ", "))
		}
		switch action {
		case "pause_campaign", "enable_campaign":
			status := "PAUSED"
			if action == "enable_campaign" {
				status = "ENABLED"
			}
			campaignID := stringParam(req.Params, "campaign_id", "campaignId")
			if err := client.SetCampaignStatus(ctx, req.Account, campaignID, status, req.LoginCustomerID); err != nil {
				return ActionResponse{}, err
			}
			return ActionResponse{Status: "ok", Action: action, Affected: 1, Message: "campagne " + campaignID + " → " + status}, nil

		case "push_negative_keywords":
			campaignID := stringParam(req.Params, "campaign_id", "campaignId")
			level := stringParam(req.Params, "level")
			keywords := negativeKeywordsParam(req.Params)
			matchType := stringParam(req.Params, "match_type", "matchType")
			n, err := client.PushNegativeKeywords(ctx, req.Account, campaignID, level, keywords, matchType, req.LoginCustomerID)
			if err != nil {
				return ActionResponse{}, err
			}
			return ActionResponse{Status: "ok", Action: action, Affected: n, Message: fmt.Sprintf("%d negative keywords toegevoegd", n)}, nil

		case "create_campaign":
			micros, err := amountMicrosParam(req.Params, "budget_amount_micros")
			if err != nil {
				return ActionResponse{}, err
			}
			id, err := client.CreateCampaign(ctx, req.Account,
				stringParam(req.Params, "name"),
				stringParam(req.Params, "channel_type", "channelType"),
				stringParam(req.Params, "bidding_strategy", "biddingStrategy"),
				micros,
				stringParam(req.Params, "status"),
				req.LoginCustomerID)
			if err != nil {
				return ActionResponse{}, err
			}
			return ActionResponse{Status: "ok", Action: action, Affected: 1, Message: "campagne " + id + " aangemaakt", CampaignID: id}, nil

		case "create_ad_group":
			micros, err := amountMicrosParam(req.Params, "cpc_bid_micros")
			if err != nil {
				return ActionResponse{}, err
			}
			id, err := client.CreateAdGroup(ctx, req.Account,
				stringParam(req.Params, "campaign_id", "campaignId"),
				stringParam(req.Params, "name"),
				micros,
				stringParam(req.Params, "status"),
				req.LoginCustomerID)
			if err != nil {
				return ActionResponse{}, err
			}
			return ActionResponse{Status: "ok", Action: action, Affected: 1, Message: "ad group " + id + " aangemaakt", AdGroupID: id}, nil

		case "set_campaign_budget":
			budget := stringParam(req.Params, "budget_resource_name", "budgetResourceName")
			if budget == "" {
				resolved, err := client.CampaignBudgetResourceName(ctx, req.Account,
					stringParam(req.Params, "campaign_id", "campaignId"), req.LoginCustomerID)
				if err != nil {
					return ActionResponse{}, err
				}
				budget = resolved
			}
			micros, err := intParamMicros(req.Params)
			if err != nil {
				return ActionResponse{}, err
			}
			opts := mutateOptionsParam(req.Params)
			res, err := client.AdsMutate(ctx, req.Account, []analytics.MutateOperation{{
				Action:       "update",
				Resource:     "campaign_budget",
				ResourceName: budget,
				Object:       map[string]any{"amount_micros": strconv.FormatInt(micros, 10)},
				UpdateMask:   []string{"amount_micros"},
			}}, opts, req.LoginCustomerID)
			if err != nil {
				return ActionResponse{}, err
			}
			return ActionResponse{
				Status: "ok", Action: action, Affected: 1,
				ResourceNames: res.ResourceNames, Validated: res.Validated,
				Message: "dagbudget → " + strconv.FormatFloat(float64(micros)/1e6, 'f', 2, 64) + mutateSuffix(res),
			}, nil

		case "mutate":
			ops, opts, err := mutateParams(req.Params)
			if err != nil {
				return ActionResponse{}, err
			}
			res, err := client.AdsMutate(ctx, req.Account, ops, opts, req.LoginCustomerID)
			if err != nil {
				return ActionResponse{}, err
			}
			return ActionResponse{
				Status: "ok", Action: action, Affected: len(ops),
				ResourceNames: res.ResourceNames, Validated: res.Validated,
				Message: mutateMessage(res),
			}, nil

		case "add_keywords":
			keywords := keywordInputsParam(req.Params)
			opts := mutateOptionsParam(req.Params)
			res, err := client.AddKeywords(ctx, req.Account,
				stringParam(req.Params, "ad_group_id", "adGroupId"), keywords, opts, req.LoginCustomerID)
			if err != nil {
				return ActionResponse{}, err
			}
			return ActionResponse{
				Status: "ok", Action: action, Affected: len(keywords),
				ResourceNames: res.ResourceNames, Validated: res.Validated,
				Message: mutateMessage(res),
			}, nil

		case "set_keyword_status", "remove_keyword":
			name, err := analytics.AdsMutationResourceName(req.Account, "ad_group_criterion",
				stringParam(req.Params, "ad_group_id", "adGroupId"),
				stringParam(req.Params, "criterion_id", "criterionId"))
			if err != nil {
				return ActionResponse{}, err
			}
			opts := mutateOptionsParam(req.Params)
			var res analytics.MutateResult
			if action == "remove_keyword" {
				res, err = client.RemoveAdsResource(ctx, req.Account, "ad_group_criterion", name, opts, req.LoginCustomerID)
			} else {
				res, err = client.SetAdsStatus(ctx, req.Account, "ad_group_criterion", name,
					stringParam(req.Params, "status"), opts, req.LoginCustomerID)
			}
			if err != nil {
				return ActionResponse{}, err
			}
			return ActionResponse{
				Status: "ok", Action: action, Affected: 1,
				ResourceNames: res.ResourceNames, Validated: res.Validated,
				Message: mutateMessage(res),
			}, nil

		case "set_ad_group_status", "set_ad_status":
			resource := "ad_group"
			itemID := stringParam(req.Params, "ad_group_id", "adGroupId")
			parentID := ""
			if action == "set_ad_status" {
				resource, parentID, itemID = "ad_group_ad", itemID, stringParam(req.Params, "ad_id", "adId")
			}
			name, err := analytics.AdsMutationResourceName(req.Account, resource, parentID, itemID)
			if err != nil {
				return ActionResponse{}, err
			}
			opts := mutateOptionsParam(req.Params)
			res, err := client.SetAdsStatus(ctx, req.Account, resource, name,
				stringParam(req.Params, "status"), opts, req.LoginCustomerID)
			if err != nil {
				return ActionResponse{}, err
			}
			return ActionResponse{
				Status: "ok", Action: action, Affected: 1,
				ResourceNames: res.ResourceNames, Validated: res.Validated,
				Message: mutateMessage(res),
			}, nil

		case "set_conversion_action":
			opts := mutateOptionsParam(req.Params)
			res, err := client.SetConversionActionSettings(ctx, req.Account,
				stringParam(req.Params, "conversion_action_id", "conversionActionId", "id"),
				boolPtrParam(req.Params, "primary_for_goal", "primaryForGoal"),
				boolPtrParam(req.Params, "include_in_conversions_metric", "includeInConversionsMetric"),
				stringParam(req.Params, "status"), opts, req.LoginCustomerID)
			if err != nil {
				return ActionResponse{}, err
			}
			return ActionResponse{
				Status: "ok", Action: action, Affected: 1,
				ResourceNames: res.ResourceNames, Validated: res.Validated,
				Message: mutateMessage(res),
			}, nil
		}
		return ActionResponse{}, fmt.Errorf("onbekende actie %q voor google_ads", req.Action)
	}
	return ActionResponse{}, fmt.Errorf("onbekende connector %q voor acties", req.Connector)
}

// mutateOptionsParam reads the safety switches of a write: validate_only asks
// Google to check the mutation without applying it, partial_failure keeps going
// when one operation in a batch is rejected.
func mutateOptionsParam(params map[string]any) analytics.MutateOptions {
	return analytics.MutateOptions{
		ValidateOnly:   boolParam(params, "validate_only", "validateOnly"),
		PartialFailure: boolParam(params, "partial_failure", "partialFailure"),
	}
}

// mutateMessage turns the engine's result into one readable line.
func mutateMessage(res analytics.MutateResult) string {
	parts := make([]string, 0, 3)
	if res.Validated {
		parts = append(parts, "alleen gevalideerd (validate_only): er is niets gewijzigd")
	}
	if res.Partial {
		parts = append(parts, "partial_failure staat aan")
	}
	if len(res.Warnings) > 0 {
		parts = append(parts, "waarschuwing: "+strings.Join(res.Warnings, "; "))
	}
	return strings.Join(parts, " · ")
}

// mutateSuffix lists only the "nothing was changed" / warning notes, for
// messages that have their own text already.
func mutateSuffix(res analytics.MutateResult) string {
	if suffix := mutateMessage(res); suffix != "" {
		return " (" + suffix + ")"
	}
	return ""
}

// mutateParams accepts one operation in the params itself, or a list under
// "operations" for bulk work (zoekwoorden toevoegen in één call).
func mutateParams(params map[string]any) ([]analytics.MutateOperation, analytics.MutateOptions, error) {
	opts := mutateOptionsParam(params)
	raw, hasList := params["operations"]
	if !hasList {
		op, err := mutateOperation(params)
		if err != nil {
			return nil, opts, err
		}
		return []analytics.MutateOperation{op}, opts, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, opts, fmt.Errorf("operations moet een lijst met operaties zijn")
	}
	ops := make([]analytics.MutateOperation, 0, len(items))
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, opts, fmt.Errorf("operatie %d is geen object", i+1)
		}
		op, err := mutateOperation(m)
		if err != nil {
			return nil, opts, err
		}
		ops = append(ops, op)
	}
	return ops, opts, nil
}

func mutateOperation(params map[string]any) (analytics.MutateOperation, error) {
	op := analytics.MutateOperation{
		Resource:     stringParam(params, "resource"),
		Action:       stringParam(params, "op", "operation", "mutate_action"),
		ResourceName: stringParam(params, "resource_name", "resourceName"),
		UpdateMask:   stringListParam(params, "update_mask"),
	}
	for _, key := range []string{"object", "fields", "values"} {
		if m := mapParam(params, key); m != nil {
			op.Object = m
			break
		}
	}
	if op.Action == "" {
		return op, fmt.Errorf("op is verplicht (create, update of remove)")
	}
	if op.Resource == "" {
		return op, fmt.Errorf("resource is verplicht (kies uit: %s)", strings.Join(analytics.AdsMutationResources(), ", "))
	}
	return op, nil
}

// keywordInputsParam accepts ["term", …] or [{"text": …, "match_type": …}, …],
// with shared defaults for match type, status and cpc bid.
func keywordInputsParam(params map[string]any) []analytics.KeywordInput {
	raw, ok := params["keywords"]
	if !ok || raw == nil {
		return nil
	}
	defaultMatch := stringParam(params, "match_type", "matchType")
	defaultStatus := stringParam(params, "status")
	bidMicros := int64Param(params, "cpc_bid_micros", "cpcBidMicros")
	if bidMicros == 0 {
		bidMicros = int64(floatParam(params, "cpc_bid", "cpcBid") * 1e6)
	}

	out := []analytics.KeywordInput{}
	add := func(keyword analytics.KeywordInput) {
		if keyword.Text == "" {
			return
		}
		if keyword.MatchType == "" {
			keyword.MatchType = defaultMatch
		}
		if keyword.Status == "" {
			keyword.Status = defaultStatus
		}
		if keyword.CPCBidMicros == 0 {
			keyword.CPCBidMicros = bidMicros
		}
		out = append(out, keyword)
	}

	switch value := raw.(type) {
	case string:
		for _, part := range strings.Split(value, ",") {
			add(analytics.KeywordInput{Text: strings.TrimSpace(part)})
		}
	case []any:
		for _, item := range value {
			switch keyword := item.(type) {
			case string:
				add(analytics.KeywordInput{Text: strings.TrimSpace(keyword)})
			case map[string]any:
				add(analytics.KeywordInput{
					Text:         stringParam(keyword, "text", "keyword", "term"),
					MatchType:    stringParam(keyword, "match_type", "matchType"),
					Status:       stringParam(keyword, "status"),
					CPCBidMicros: int64Param(keyword, "cpc_bid_micros", "cpcBidMicros"),
				})
			}
		}
	}
	return out
}

// boolParam reads a boolean, tolerating the string and numeric spellings a
// client may send.
func boolParam(params map[string]any, keys ...string) bool {
	for _, key := range keys {
		v, ok := params[key]
		if !ok {
			continue
		}
		switch value := v.(type) {
		case bool:
			return value
		case float64:
			return value != 0
		case string:
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "true", "1", "yes", "ja":
				return true
			}
		}
	}
	return false
}

// boolPtrParam distinguishes "not sent" from "false": only a field the caller
// actually sent may end up in an update.
func boolPtrParam(params map[string]any, keys ...string) *bool {
	for _, key := range keys {
		if _, ok := params[key]; !ok {
			continue
		}
		value := boolParam(params, key)
		return &value
	}
	return nil
}

func mapParam(params map[string]any, key string) map[string]any {
	v, ok := params[key]
	if !ok || v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func int64Param(params map[string]any, keys ...string) int64 {
	for _, key := range keys {
		v, ok := params[key]
		if !ok {
			continue
		}
		if f, err := toFloat(v); err == nil {
			return int64(f)
		}
	}
	return 0
}

func floatParam(params map[string]any, keys ...string) float64 {
	for _, key := range keys {
		v, ok := params[key]
		if !ok {
			continue
		}
		if f, err := toFloat(v); err == nil {
			return f
		}
	}
	return 0
}

func stringParam(params map[string]any, keys ...string) string {
	for _, key := range keys {
		if params == nil {
			return ""
		}
		if v, ok := params[key]; ok {
			switch value := v.(type) {
			case string:
				return strings.TrimSpace(value)
			case float64:
				return strconv.FormatFloat(value, 'f', -1, 64)
			}
		}
	}
	return ""
}

// negativeKeywordsParam accepts both shapes a client may send:
// ["term", …] or [{"text": "term", "match_type": "BROAD"}, …].
func negativeKeywordsParam(params map[string]any) []analytics.NegativeKeyword {
	v, ok := params["keywords"]
	if !ok || v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		out := make([]analytics.NegativeKeyword, 0, 1)
		for _, part := range strings.Split(s, ",") {
			out = append(out, analytics.NegativeKeyword{Text: strings.TrimSpace(part)})
		}
		return out
	}
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]analytics.NegativeKeyword, 0, len(items))
	for _, item := range items {
		switch value := item.(type) {
		case string:
			out = append(out, analytics.NegativeKeyword{Text: strings.TrimSpace(value)})
		case map[string]any:
			out = append(out, analytics.NegativeKeyword{
				Text:      stringParam(value, "text", "keyword", "term"),
				MatchType: stringParam(value, "match_type", "matchType"),
			})
		}
	}
	return out
}

// amountMicrosParam reads a micros amount, falling back to whole currency units
// when the caller sends `amount` instead.
func amountMicrosParam(params map[string]any, key string) (int64, error) {
	if v, ok := params[key]; ok {
		f, err := toFloat(v)
		if err != nil {
			return 0, fmt.Errorf("ongeldig bedrag in %s: %w", key, err)
		}
		return int64(f), nil
	}
	for _, fallback := range []string{"amount_micros", "amount", "budget_amount"} {
		v, ok := params[fallback]
		if !ok {
			continue
		}
		f, err := toFloat(v)
		if err != nil {
			return 0, fmt.Errorf("ongeldig bedrag in %s: %w", fallback, err)
		}
		if fallback == "amount" || fallback == "budget_amount" {
			return int64(f * 1e6), nil
		}
		return int64(f), nil
	}
	return 0, fmt.Errorf("%s is verplicht", key)
}

func stringListParam(params map[string]any, key string) []string {
	v, ok := params[key]
	if !ok || v == nil {
		return nil
	}
	switch value := v.(type) {
	case []string:
		return value
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			out = append(out, strings.TrimSpace(fmt.Sprint(item)))
		}
		return out
	case string:
		var parts []string
		for _, p := range strings.Split(value, ",") {
			parts = append(parts, strings.TrimSpace(p))
		}
		return parts
	}
	return nil
}

// intParamMicros reads an amount as micros, accepting either amount_micros or a
// human amount in whole units (amount / amount_euros).
func intParamMicros(params map[string]any) (int64, error) {
	if v, ok := params["amount_micros"]; ok {
		if f, err := toFloat(v); err == nil {
			return int64(f), nil
		}
	}
	if v, ok := params["amount"]; ok {
		f, err := toFloat(v)
		if err != nil {
			return 0, fmt.Errorf("ongeldig bedrag: %w", err)
		}
		return int64(f * 1e6), nil
	}
	if v, ok := params["amount_euros"]; ok {
		f, err := toFloat(v)
		if err != nil {
			return 0, fmt.Errorf("ongeldig bedrag: %w", err)
		}
		return int64(f * 1e6), nil
	}
	return 0, fmt.Errorf("amount of amount_micros is verplicht")
}

func toFloat(v any) (float64, error) {
	switch value := v.(type) {
	case float64:
		return value, nil
	case int:
		return float64(value), nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(value), 64)
	}
	return 0, fmt.Errorf("kan %v niet als getal lezen", v)
}

// QueryHandler serves POST /query.
func QueryHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req QueryRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "ongeldige JSON: "+err.Error())
			return
		}
		resp, err := RunQuery(r.Context(), req)
		if err != nil {
			logger.Warn("query failed", "connector", req.Connector, "error", err)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// ActionHandler serves POST /action.
func ActionHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ActionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "ongeldige JSON: "+err.Error())
			return
		}
		resp, err := RunAction(r.Context(), req)
		if err != nil {
			logger.Warn("action failed", "action", req.Action, "error", err)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		logger.Info("action executed", "action", resp.Action, "affected", resp.Affected)
		writeJSON(w, http.StatusOK, resp)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"status": "error", "error": message})
}
