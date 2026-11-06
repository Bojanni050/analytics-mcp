package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"analytics-mcp/api"
	"analytics-mcp/auth"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerTools adds every MCP tool this server exposes.
func registerTools(server *mcp.Server) {
	registerUtilityTools(server)
	registerGA4Report(server)
	registerGSCQuery(server)
	registerAdsSearch(server)
	registerAdsKeywordIdeas(server)
	registerAdsSetCampaignStatus(server)
	registerAdsNegativeKeywords(server)
	registerAdsMutate(server)
	registerAdsKeywords(server)
	registerAdsStatusTools(server)
	registerAdsBudget(server)
	registerAdsConversionAction(server)
}

// --- utility ---------------------------------------------------------------

type PingInput struct {
	Message string `json:"message,omitempty" jsonschema:"Optional message to echo back"`
}

type PingOutput struct {
	Reply     string `json:"reply"`
	Timestamp string `json:"timestamp"`
}

type AuthStatusOutput struct {
	Authenticated bool     `json:"authenticated"`
	Scopes        []string `json:"scopes"`
	Message       string   `json:"message"`
	Connectors    []string `json:"connectors"`
}

func registerUtilityTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ping",
		Description: "Test connectivity to the analytics MCP server",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input PingInput) (*mcp.CallToolResult, PingOutput, error) {
		reply := "pong"
		if input.Message != "" {
			reply = fmt.Sprintf("pong: %s", input.Message)
		}
		return nil, PingOutput{Reply: reply, Timestamp: time.Now().UTC().Format(time.RFC3339)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "auth_status",
		Description: "Check whether this server holds a Google token for the current session, and which APIs it can call",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, AuthStatusOutput, error) {
		info := auth.GetTokenInfo(ctx)
		ok := info != nil && info.GoogleToken != nil
		message := "not authenticated - open /authorize to link a Google account"
		if ok {
			message = "authenticated with Google"
		}
		return nil, AuthStatusOutput{
			Authenticated: ok,
			Scopes:        auth.GoogleScopes,
			Message:       message,
			Connectors:    api.SupportedConnectors,
		}, nil
	})
}

// --- GA4 -------------------------------------------------------------------

type GA4ReportInput struct {
	PropertyID string   `json:"property_id" jsonschema:"GA4 property ID, e.g. 368911252"`
	Fields     []string `json:"fields" jsonschema:"Fields to return, e.g. [\"date\",\"sessions\",\"users\"]"`
	GroupBy    []string `json:"group_by,omitempty" jsonschema:"Optional extra dimensions to group by"`
	DateFrom   string   `json:"date_from" jsonschema:"Start date, YYYY-MM-DD"`
	DateTo     string   `json:"date_to" jsonschema:"End date, YYYY-MM-DD"`
	Limit      int      `json:"limit,omitempty" jsonschema:"Maximum number of rows"`
}

type RowsOutput struct {
	Status    string           `json:"status"`
	Data      []map[string]any `json:"data"`
	TotalRows int              `json:"total_rows"`
	Query     string           `json:"query,omitempty"`
}

func registerGA4Report(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ga4_report",
		Description: "Run a Google Analytics 4 report (sessions, users, page views, sources) for a property and period",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GA4ReportInput) (*mcp.CallToolResult, RowsOutput, error) {
		out, err := api.RunQuery(ctx, api.QueryRequest{
			Connector: "googleanalytics4",
			Account:   input.PropertyID,
			Fields:    input.Fields,
			GroupBy:   input.GroupBy,
			DateFrom:  input.DateFrom,
			DateTo:    input.DateTo,
			Limit:     input.Limit,
		})
		if err != nil {
			return nil, RowsOutput{}, err
		}
		return nil, RowsOutput{Status: out.Status, Data: out.Data, TotalRows: out.TotalRows}, nil
	})
}

// --- Search Console --------------------------------------------------------

type GSCQueryInput struct {
	Site     string   `json:"site" jsonschema:"Search Console property, e.g. https://white-vision.nl/ or sc-domain:white-vision.nl"`
	Fields   []string `json:"fields" jsonschema:"Fields to return, e.g. [\"query\",\"clicks\",\"impressions\",\"ctr\",\"position\"]"`
	GroupBy  []string `json:"group_by,omitempty" jsonschema:"Optional extra dimensions to group by"`
	DateFrom string   `json:"date_from" jsonschema:"Start date, YYYY-MM-DD"`
	DateTo   string   `json:"date_to" jsonschema:"End date, YYYY-MM-DD"`
	Limit    int      `json:"limit,omitempty" jsonschema:"Maximum number of rows"`
}

func registerGSCQuery(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "gsc_query",
		Description: "Query Search Console search analytics: queries, pages, clicks, impressions, CTR and average position",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GSCQueryInput) (*mcp.CallToolResult, RowsOutput, error) {
		out, err := api.RunQuery(ctx, api.QueryRequest{
			Connector: "searchconsole",
			Account:   input.Site,
			Fields:    input.Fields,
			GroupBy:   input.GroupBy,
			DateFrom:  input.DateFrom,
			DateTo:    input.DateTo,
			Limit:     input.Limit,
		})
		if err != nil {
			return nil, RowsOutput{}, err
		}
		return nil, RowsOutput{Status: out.Status, Data: out.Data, TotalRows: out.TotalRows}, nil
	})
}

// --- Google Ads ------------------------------------------------------------

type AdsSearchInput struct {
	CustomerID      string   `json:"customer_id" jsonschema:"Google Ads customer ID, e.g. 307-043-6491"`
	Fields          []string `json:"fields" jsonschema:"Fields to return, e.g. [\"campaign_name\",\"spend\",\"clicks\",\"conversions\"]"`
	Conditions      []string `json:"conditions,omitempty" jsonschema:"GAQL conditions, e.g. [\"segments.date BETWEEN '2026-10-01' AND '2026-10-31'\"]"`
	Orderings       []string `json:"orderings,omitempty" jsonschema:"GAQL orderings, e.g. [\"metrics.cost_micros DESC\"]"`
	Limit           int      `json:"limit,omitempty" jsonschema:"Maximum number of rows"`
	LoginCustomerID string   `json:"login_customer_id,omitempty" jsonschema:"Optional manager account ID (login-customer-id)"`
}

func registerAdsSearch(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ads_search",
		Description: "Run a Google Ads report: campaigns, ad groups, keywords or search terms with spend, clicks and conversions",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AdsSearchInput) (*mcp.CallToolResult, RowsOutput, error) {
		out, err := api.RunQuery(ctx, api.QueryRequest{
			Connector:       "google_ads",
			Account:         input.CustomerID,
			Fields:          input.Fields,
			Conditions:      input.Conditions,
			Orderings:       input.Orderings,
			Limit:           input.Limit,
			LoginCustomerID: input.LoginCustomerID,
		})
		if err != nil {
			return nil, RowsOutput{}, err
		}
		return nil, RowsOutput{Status: out.Status, Data: out.Data, TotalRows: out.TotalRows, Query: out.Query}, nil
	})
}

type AdsKeywordIdeasInput struct {
	CustomerID string `json:"customer_id" jsonschema:"Google Ads customer ID"`
	Seeds      string `json:"keyword_seeds" jsonschema:"Comma-separated seed keywords, e.g. \"dj bruiloft,dj huren\""`
	Geo        string `json:"geo_target_constants,omitempty" jsonschema:"Geo target, default geoTargetConstants/2840 (Netherlands)"`
	Language   string `json:"language,omitempty" jsonschema:"Language constant, default languageConstants/1010 (Dutch)"`
	PageURL    string `json:"page_url,omitempty" jsonschema:"Optional URL seed"`
}

func registerAdsKeywordIdeas(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ads_keyword_ideas",
		Description: "Keyword Planner ideas with search volume, competition and top-of-page bid ranges",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AdsKeywordIdeasInput) (*mcp.CallToolResult, RowsOutput, error) {
		out, err := api.RunQuery(ctx, api.QueryRequest{
			Connector: "google_ads",
			Account:   input.CustomerID,
			Fields:    []string{"keyword_text", "monthly_search_volume", "competition", "low_top_of_page_bid", "high_top_of_page_bid"},
			Options: api.Options{
				KeywordSeeds: input.Seeds,
				Geo:          input.Geo,
				Language:     input.Language,
				PageURL:      input.PageURL,
			},
		})
		if err != nil {
			return nil, RowsOutput{}, err
		}
		return nil, RowsOutput{Status: out.Status, Data: out.Data, TotalRows: out.TotalRows}, nil
	})
}

type AdsCampaignStatusInput struct {
	CustomerID string `json:"customer_id" jsonschema:"Google Ads customer ID"`
	CampaignID string `json:"campaign_id" jsonschema:"Campaign ID to pause or enable"`
	Status     string `json:"status" jsonschema:"PAUSED or ENABLED"`
	Confirm    bool   `json:"confirm" jsonschema:"Must be true - this changes a live campaign"`
}

func registerAdsSetCampaignStatus(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ads_set_campaign_status",
		Description: "Pause or enable a Google Ads campaign (requires confirm: true)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AdsCampaignStatusInput) (*mcp.CallToolResult, api.ActionResponse, error) {
		if !input.Confirm {
			return nil, api.ActionResponse{}, fmt.Errorf("confirm must be true - pausing or enabling a campaign changes live delivery")
		}
		action := "pause_campaign"
		if strings.EqualFold(input.Status, "ENABLED") {
			action = "enable_campaign"
		}
		out, err := api.RunAction(ctx, api.ActionRequest{
			Connector: "google_ads",
			Account:   input.CustomerID,
			Action:    action,
			Params:    map[string]any{"campaign_id": input.CampaignID},
		})
		if err != nil {
			return nil, api.ActionResponse{}, err
		}
		return nil, out, nil
	})
}

type AdsNegativeKeywordsInput struct {
	CustomerID string   `json:"customer_id" jsonschema:"Google Ads customer ID"`
	CampaignID string   `json:"campaign_id" jsonschema:"Campaign to add the negative keywords to"`
	Keywords   []string `json:"keywords" jsonschema:"Negative keywords to add"`
	MatchType  string   `json:"match_type,omitempty" jsonschema:"BROAD (default), PHRASE or EXACT"`
	Confirm    bool     `json:"confirm" jsonschema:"Must be true - this changes a live campaign"`
}

func registerAdsNegativeKeywords(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ads_push_negative_keywords",
		Description: "Add negative keywords to a Google Ads campaign (requires confirm: true)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AdsNegativeKeywordsInput) (*mcp.CallToolResult, api.ActionResponse, error) {
		if !input.Confirm {
			return nil, api.ActionResponse{}, fmt.Errorf("confirm must be true - negative keywords change live delivery")
		}
		out, err := api.RunAction(ctx, api.ActionRequest{
			Connector: "google_ads",
			Account:   input.CustomerID,
			Action:    "push_negative_keywords",
			Params: map[string]any{
				"campaign_id": input.CampaignID,
				"keywords":    input.Keywords,
				"match_type":  input.MatchType,
			},
		})
		if err != nil {
			return nil, api.ActionResponse{}, err
		}
		return nil, out, nil
	})
}

// --- Google Ads writes -----------------------------------------------------
//
// Elke write loopt door runAdsWrite: zonder confirm: true gebeurt er niets, en
// met validate_only: true keurt Google de mutatie zonder hem toe te passen. Doe
// die droogloop eerst bij twijfel — de Ads API weigert sommige velden (bij een
// GA4-import bijvoorbeeld) en dan zie je dat vóór er iets live staat.

func runAdsWrite(ctx context.Context, confirm bool, customerID, action string, params map[string]any, impact string) (api.ActionResponse, error) {
	if !confirm {
		return api.ActionResponse{}, fmt.Errorf("confirm must be true - %s", impact)
	}
	return api.RunAction(ctx, api.ActionRequest{
		Connector: "google_ads",
		Account:   customerID,
		Action:    action,
		Params:    params,
	})
}

type AdsMutateInput struct {
	CustomerID     string           `json:"customer_id" jsonschema:"Google Ads customer ID"`
	Operation      map[string]any   `json:"operation,omitempty" jsonschema:"Eén operatie: {resource, op, object of resource_name, update_mask}"`
	Operations     []map[string]any `json:"operations,omitempty" jsonschema:"Meerdere operaties in één call (max 1000)"`
	ValidateOnly   bool             `json:"validate_only,omitempty" jsonschema:"true = alleen laten valideren, er wijzigt niets"`
	PartialFailure bool             `json:"partial_failure,omitempty" jsonschema:"true = doorgaan als één operatie wordt afgewezen"`
	Confirm        bool             `json:"confirm" jsonschema:"Must be true - dit wijzigt live advertenties"`
}

func registerAdsMutate(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "ads_mutate",
		Description: "Wijzig elke ondersteunde Google Ads-resource (create/update/remove): campaign, " +
			"campaign_budget, ad_group, ad_group_criterion (zoekwoorden), campaign_criterion (negatieve " +
			"zoekwoorden), ad_group_ad, conversion_action, ad_group_bid_modifier, asset, campaign_asset, " +
			"shared_set, shared_criterion. Vereist confirm: true; gebruik validate_only voor een droogloop. " +
			"Campagnes en ad groups kunnen alleen op PAUSED, niet verwijderd.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AdsMutateInput) (*mcp.CallToolResult, api.ActionResponse, error) {
		params := map[string]any{
			"validate_only":   input.ValidateOnly,
			"partial_failure": input.PartialFailure,
		}
		switch {
		case len(input.Operations) > 0:
			list := make([]any, 0, len(input.Operations))
			for _, op := range input.Operations {
				list = append(list, op)
			}
			params["operations"] = list
		case len(input.Operation) > 0:
			for key, value := range input.Operation {
				params[key] = value
			}
		default:
			return nil, api.ActionResponse{}, fmt.Errorf("geef een operation of een operations-lijst mee")
		}
		out, err := runAdsWrite(ctx, input.Confirm, input.CustomerID, "mutate", params,
			"een Ads-mutatie wijzigt live advertenties")
		if err != nil {
			return nil, api.ActionResponse{}, err
		}
		return nil, out, nil
	})
}

type AdsAddKeywordsInput struct {
	CustomerID   string   `json:"customer_id" jsonschema:"Google Ads customer ID"`
	AdGroupID    string   `json:"ad_group_id" jsonschema:"Ad group waarin de zoekwoorden komen"`
	Keywords     []string `json:"keywords" jsonschema:"Zoekwoorden als platte tekst, zonder match-type-haken"`
	MatchType    string   `json:"match_type,omitempty" jsonschema:"BROAD (default), PHRASE of EXACT"`
	Status       string   `json:"status,omitempty" jsonschema:"ENABLED (default) of PAUSED"`
	CPCBidMicros int64    `json:"cpc_bid_micros,omitempty" jsonschema:"Optioneel maximum-cpc-bod in micros (1500000 = 1,50)"`
	ValidateOnly bool     `json:"validate_only,omitempty" jsonschema:"true = alleen laten valideren, er wijzigt niets"`
	Confirm      bool     `json:"confirm" jsonschema:"Must be true - dit voegt live zoekwoorden toe"`
}

type AdsKeywordStatusInput struct {
	CustomerID   string `json:"customer_id" jsonschema:"Google Ads customer ID"`
	AdGroupID    string `json:"ad_group_id" jsonschema:"Ad group van het zoekwoord"`
	CriterionID  string `json:"criterion_id" jsonschema:"Criterium-id van het zoekwoord (ad_group_criterion.id)"`
	Status       string `json:"status" jsonschema:"ENABLED, PAUSED of REMOVED"`
	ValidateOnly bool   `json:"validate_only,omitempty" jsonschema:"true = alleen laten valideren, er wijzigt niets"`
	Confirm      bool   `json:"confirm" jsonschema:"Must be true - dit wijzigt live zoekwoorden"`
}

type AdsRemoveKeywordInput struct {
	CustomerID  string `json:"customer_id" jsonschema:"Google Ads customer ID"`
	AdGroupID   string `json:"ad_group_id" jsonschema:"Ad group van het zoekwoord"`
	CriterionID string `json:"criterion_id" jsonschema:"Criterium-id van het zoekwoord (ad_group_criterion.id)"`
	Confirm     bool   `json:"confirm" jsonschema:"Must be true - dit verwijdert het zoekwoord definitief"`
}

func registerAdsKeywords(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ads_add_keywords",
		Description: "Voeg zoekwoorden toe aan één ad group (vereist confirm: true; validate_only eerst bij twijfel)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AdsAddKeywordsInput) (*mcp.CallToolResult, api.ActionResponse, error) {
		keywords := make([]any, 0, len(input.Keywords))
		for _, keyword := range input.Keywords {
			if trimmed := strings.TrimSpace(keyword); trimmed != "" {
				keywords = append(keywords, trimmed)
			}
		}
		params := map[string]any{
			"ad_group_id":   input.AdGroupID,
			"keywords":      keywords,
			"match_type":    input.MatchType,
			"status":        input.Status,
			"validate_only": input.ValidateOnly,
		}
		if input.CPCBidMicros > 0 {
			params["cpc_bid_micros"] = input.CPCBidMicros
		}
		out, err := runAdsWrite(ctx, input.Confirm, input.CustomerID, "add_keywords", params,
			"zoekwoorden toevoegen verandert live advertenties")
		if err != nil {
			return nil, api.ActionResponse{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ads_set_keyword_status",
		Description: "Zet één zoekwoord op ENABLED, PAUSED of REMOVED (vereist confirm: true)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AdsKeywordStatusInput) (*mcp.CallToolResult, api.ActionResponse, error) {
		params := map[string]any{
			"ad_group_id":   input.AdGroupID,
			"criterion_id":  input.CriterionID,
			"status":        input.Status,
			"validate_only": input.ValidateOnly,
		}
		out, err := runAdsWrite(ctx, input.Confirm, input.CustomerID, "set_keyword_status", params,
			"de status van een zoekwoord verandert live advertenties")
		if err != nil {
			return nil, api.ActionResponse{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ads_remove_keyword",
		Description: "Verwijder één zoekwoord uit een ad group (vereist confirm: true)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AdsRemoveKeywordInput) (*mcp.CallToolResult, api.ActionResponse, error) {
		params := map[string]any{
			"ad_group_id":  input.AdGroupID,
			"criterion_id": input.CriterionID,
		}
		out, err := runAdsWrite(ctx, input.Confirm, input.CustomerID, "remove_keyword", params,
			"een zoekwoord verwijderen kan niet teruggedraaid worden")
		if err != nil {
			return nil, api.ActionResponse{}, err
		}
		return nil, out, nil
	})
}

type AdsStatusInput struct {
	CustomerID   string `json:"customer_id" jsonschema:"Google Ads customer ID"`
	AdGroupID    string `json:"ad_group_id" jsonschema:"Ad group id"`
	AdID         string `json:"ad_id,omitempty" jsonschema:"Advertentie-id (alleen bij ad_status, ad_group_ad.ad.id)"`
	Status       string `json:"status" jsonschema:"ENABLED of PAUSED"`
	ValidateOnly bool   `json:"validate_only,omitempty" jsonschema:"true = alleen laten valideren, er wijzigt niets"`
	Confirm      bool   `json:"confirm" jsonschema:"Must be true - dit wijzigt live advertenties"`
}

func registerAdsStatusTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ads_set_ad_group_status",
		Description: "Pauzeer of activeer een ad group (vereist confirm: true)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AdsStatusInput) (*mcp.CallToolResult, api.ActionResponse, error) {
		params := map[string]any{
			"ad_group_id":   input.AdGroupID,
			"status":        input.Status,
			"validate_only": input.ValidateOnly,
		}
		out, err := runAdsWrite(ctx, input.Confirm, input.CustomerID, "set_ad_group_status", params,
			"de status van een ad group verandert live advertenties")
		if err != nil {
			return nil, api.ActionResponse{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ads_set_ad_status",
		Description: "Pauzeer of activeer één advertentie (vereist confirm: true)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AdsStatusInput) (*mcp.CallToolResult, api.ActionResponse, error) {
		params := map[string]any{
			"ad_group_id":   input.AdGroupID,
			"ad_id":         input.AdID,
			"status":        input.Status,
			"validate_only": input.ValidateOnly,
		}
		out, err := runAdsWrite(ctx, input.Confirm, input.CustomerID, "set_ad_status", params,
			"de status van een advertentie verandert live advertenties")
		if err != nil {
			return nil, api.ActionResponse{}, err
		}
		return nil, out, nil
	})
}

type AdsBudgetInput struct {
	CustomerID         string  `json:"customer_id" jsonschema:"Google Ads customer ID"`
	CampaignID         string  `json:"campaign_id,omitempty" jsonschema:"Campagne waarvan het dagbudget wijzigt (het budget wordt erbij gezocht)"`
	BudgetResourceName string  `json:"campaign_budget_resource_name,omitempty" jsonschema:"Alternatief: volledige budget-resourceName"`
	AmountMicros       int64   `json:"amount_micros,omitempty" jsonschema:"Nieuw dagbudget in micros"`
	Amount             float64 `json:"amount,omitempty" jsonschema:"Nieuw dagbudget in euro's (bv. 15.00)"`
	ValidateOnly       bool    `json:"validate_only,omitempty" jsonschema:"true = alleen laten valideren, er wijzigt niets"`
	Confirm            bool    `json:"confirm" jsonschema:"Must be true - dit wijzigt live budgetten"`
}

func registerAdsBudget(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ads_set_campaign_budget",
		Description: "Wijzig het dagbudget van een campagne (vereist confirm: true)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AdsBudgetInput) (*mcp.CallToolResult, api.ActionResponse, error) {
		params := map[string]any{
			"campaign_id":   input.CampaignID,
			"validate_only": input.ValidateOnly,
		}
		if input.BudgetResourceName != "" {
			params["budget_resource_name"] = input.BudgetResourceName
		}
		if input.AmountMicros > 0 {
			params["amount_micros"] = input.AmountMicros
		}
		if input.Amount > 0 {
			params["amount"] = input.Amount
		}
		out, err := runAdsWrite(ctx, input.Confirm, input.CustomerID, "set_campaign_budget", params,
			"een budgetwijziging beïnvloedt direct de besteding")
		if err != nil {
			return nil, api.ActionResponse{}, err
		}
		return nil, out, nil
	})
}

type AdsConversionActionInput struct {
	CustomerID                 string `json:"customer_id" jsonschema:"Google Ads customer ID"`
	ConversionActionID         string `json:"conversion_action_id" jsonschema:"Conversieactie-id (bv. 7835486623)"`
	PrimaryForGoal             *bool  `json:"primary_for_goal,omitempty" jsonschema:"true = primaire conversie voor de accountdoelen"`
	IncludeInConversionsMetric *bool  `json:"include_in_conversions_metric,omitempty" jsonschema:"true = meetellen in de kolom Conversies"`
	Status                     string `json:"status,omitempty" jsonschema:"ENABLED of HIDDEN"`
	ValidateOnly               bool   `json:"validate_only,omitempty" jsonschema:"true = alleen laten valideren, er wijzigt niets"`
	Confirm                    bool   `json:"confirm" jsonschema:"Must be true - dit wijzigt live conversiemeting"`
}

func registerAdsConversionAction(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ads_conversion_action_update",
		Description: "Wijzig een conversieactie: primair doel, meetellen in Conversies, of ENABLED/HIDDEN (vereist confirm: true; validate_only eerst)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AdsConversionActionInput) (*mcp.CallToolResult, api.ActionResponse, error) {
		params := map[string]any{
			"conversion_action_id": input.ConversionActionID,
			"status":               input.Status,
			"validate_only":        input.ValidateOnly,
		}
		if input.PrimaryForGoal != nil {
			params["primary_for_goal"] = *input.PrimaryForGoal
		}
		if input.IncludeInConversionsMetric != nil {
			params["include_in_conversions_metric"] = *input.IncludeInConversionsMetric
		}
		out, err := runAdsWrite(ctx, input.Confirm, input.CustomerID, "set_conversion_action", params,
			"dit verandert hoe conversies geteld en geboden worden")
		if err != nil {
			return nil, api.ActionResponse{}, err
		}
		return nil, out, nil
	})
}
