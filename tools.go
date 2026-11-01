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
