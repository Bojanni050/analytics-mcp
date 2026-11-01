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

// ActionResponse reports what a write did.
type ActionResponse struct {
	Status   string `json:"status"`
	Action   string `json:"action"`
	Affected int    `json:"affected"`
	Message  string `json:"message,omitempty"`
}

// SupportedConnectors are the connector ids this facade understands.
var SupportedConnectors = []string{"googleanalytics4", "searchconsole", "google_ads"}

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
			keywords := stringListParam(req.Params, "keywords")
			matchType := stringParam(req.Params, "match_type", "matchType")
			n, err := client.PushNegativeKeywords(ctx, req.Account, campaignID, keywords, matchType, req.LoginCustomerID)
			if err != nil {
				return ActionResponse{}, err
			}
			return ActionResponse{Status: "ok", Action: action, Affected: n, Message: fmt.Sprintf("%d negative keywords toegevoegd", n)}, nil

		case "set_campaign_budget":
			budget := stringParam(req.Params, "budget_resource_name", "budgetResourceName")
			micros, err := intParamMicros(req.Params)
			if err != nil {
				return ActionResponse{}, err
			}
			if err := client.SetCampaignBudget(ctx, req.Account, budget, micros, req.LoginCustomerID); err != nil {
				return ActionResponse{}, err
			}
			return ActionResponse{Status: "ok", Action: action, Affected: 1, Message: "dagbudget → " + strconv.FormatFloat(float64(micros)/1e6, 'f', 2, 64)}, nil
		}
		return ActionResponse{}, fmt.Errorf("onbekende actie %q voor google_ads", req.Action)
	}
	return ActionResponse{}, fmt.Errorf("onbekende connector %q voor acties", req.Connector)
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
