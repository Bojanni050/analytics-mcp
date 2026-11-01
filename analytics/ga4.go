package analytics

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

const ga4BaseURL = "https://analyticsdata.googleapis.com/v1beta"

// ga4Dimensions maps the field names the app asks for onto GA4 API names.
var ga4Dimensions = map[string]string{
	"date":             "date",
	"source":           "sessionSource",
	"session_source":   "sessionSource",
	"medium":           "sessionMedium",
	"session_medium":   "sessionMedium",
	"source_medium":    "sessionSourceMedium",
	"campaign":         "sessionCampaignName",
	"session_campaign": "sessionCampaignName",
	"channel":          "sessionDefaultChannelGroup",
	"session_channel":  "sessionDefaultChannelGroup",
	"page":             "pagePath",
	"page_path":        "pagePath",
	"landing_page":     "landingPagePlusQueryString",
	"device":           "deviceCategory",
	"country":          "country",
	"city":             "city",
	"event_name":       "eventName",
	"hostname":         "hostName",
}

// ga4Metrics maps app field names onto GA4 API metric names.
var ga4Metrics = map[string]string{
	"sessions":                 "sessions",
	"users":                    "totalUsers",
	"total_users":              "totalUsers",
	"new_users":                "newUsers",
	"active_users":             "activeUsers",
	"screen_page_views":        "screenPageViews",
	"page_views":               "screenPageViews",
	"views":                    "screenPageViews",
	"engaged_sessions":         "engagedSessions",
	"engagement_rate":          "engagementRate",
	"bounce_rate":              "bounceRate",
	"average_session_duration": "averageSessionDuration",
	"event_count":              "eventCount",
	"conversions":              "conversions",
	"total_revenue":            "totalRevenue",
	"transactions":             "transactions",
	"key_events":               "keyEvents",
}

// GA4Query runs a GA4 report. `fields` is the full requested field list; any
// field that is not a known metric is treated as a dimension.
func (c *Client) GA4Query(ctx context.Context, propertyID string, fields, groupBy []string, from, to string, limit int) ([]map[string]any, error) {
	propertyID = strings.TrimSpace(strings.TrimPrefix(propertyID, "properties/"))
	if propertyID == "" {
		return nil, fmt.Errorf("googleanalytics4: account (property id) is required")
	}
	dr, err := newDateRange(from, to)
	if err != nil {
		return nil, err
	}

	requested := toStringSlice(append(append([]string{}, fields...), groupBy...))
	if len(requested) == 0 {
		return nil, fmt.Errorf("googleanalytics4: at least one field is required")
	}

	var dimNames, metNames []string
	for _, f := range requested {
		if api, ok := ga4Metrics[f]; ok {
			metNames = append(metNames, api)
			continue
		}
		api, ok := ga4Dimensions[f]
		if !ok {
			return nil, fmt.Errorf("googleanalytics4: onbekend veld %q", f)
		}
		dimNames = append(dimNames, api)
	}
	if len(metNames) == 0 {
		return nil, fmt.Errorf("googleanalytics4: minstens één metric nodig (bv. sessions)")
	}

	body := map[string]any{
		"dateRanges": []map[string]string{{"startDate": dr.from, "endDate": dr.to}},
	}
	if len(dimNames) > 0 {
		dims := make([]map[string]string, 0, len(dimNames))
		for _, d := range dimNames {
			dims = append(dims, map[string]string{"name": d})
		}
		body["dimensions"] = dims
	}
	mets := make([]map[string]string, 0, len(metNames))
	for _, m := range metNames {
		mets = append(mets, map[string]string{"name": m})
	}
	body["metrics"] = mets
	if limit > 0 {
		body["limit"] = limit
	}

	var resp struct {
		DimensionHeaders []struct {
			Name string `json:"name"`
		} `json:"dimensionHeaders"`
		MetricHeaders []struct {
			Name string `json:"name"`
		} `json:"metricHeaders"`
		Rows []struct {
			DimensionValues []struct {
				Value string `json:"value"`
			} `json:"dimensionValues"`
			MetricValues []struct {
				Value string `json:"value"`
			} `json:"metricValues"`
		} `json:"rows"`
	}
	u := fmt.Sprintf("%s/properties/%s:runReport", ga4BaseURL, url.PathEscape(propertyID))
	if err := c.postJSON(ctx, u, nil, body, &resp); err != nil {
		return nil, err
	}

	// Reverse lookup: GA4 API name -> requested field name.
	back := map[string]string{}
	for _, f := range requested {
		if api, ok := ga4Metrics[f]; ok {
			back[api] = f
		} else if api, ok := ga4Dimensions[f]; ok {
			back[api] = f
		}
	}

	rows := make([]map[string]any, 0, len(resp.Rows))
	for _, r := range resp.Rows {
		row := map[string]any{}
		for i, h := range resp.DimensionHeaders {
			if i < len(r.DimensionValues) {
				row[back[h.Name]] = r.DimensionValues[i].Value
			}
		}
		for i, h := range resp.MetricHeaders {
			if i < len(r.MetricValues) {
				row[back[h.Name]] = num(r.MetricValues[i].Value)
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
