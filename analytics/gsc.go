package analytics

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

const gscBaseURL = "https://searchconsole.googleapis.com/webmasters/v3"

// gscDimensions maps the app's field names onto Search Console API dimensions.
var gscDimensions = map[string]string{
	"query":             "query",
	"page":              "page",
	"date":              "date",
	"country":           "country",
	"device":            "device",
	"search_appearance": "searchAppearance",
}

// gscMetrics are the only metrics the Search Console API exposes.
var gscMetrics = map[string]bool{
	"clicks":      true,
	"impressions": true,
	"ctr":         true,
	"position":    true,
}

// GSCQuery runs a Search Analytics query for one site.
// `site` is the property exactly as it appears in Search Console, e.g.
// "https://white-vision.nl/" or "sc-domain:white-vision.nl".
func (c *Client) GSCQuery(ctx context.Context, site string, fields, groupBy []string, from, to string, limit int) ([]map[string]any, error) {
	site = strings.TrimSpace(site)
	if site == "" {
		return nil, fmt.Errorf("searchconsole: account (site URL) is required")
	}
	dr, err := newDateRange(from, to)
	if err != nil {
		return nil, err
	}

	requested := toStringSlice(append(append([]string{}, fields...), groupBy...))
	if len(requested) == 0 {
		return nil, fmt.Errorf("searchconsole: at least one field is required")
	}

	var dims []string
	for _, f := range requested {
		if gscMetrics[f] {
			continue
		}
		api, ok := gscDimensions[f]
		if !ok {
			return nil, fmt.Errorf("searchconsole: onbekend veld %q", f)
		}
		dims = append(dims, api)
	}

	body := map[string]any{
		"startDate": dr.from,
		"endDate":   dr.to,
	}
	if len(dims) > 0 {
		body["dimensions"] = dims
	}
	if limit > 0 {
		body["rowLimit"] = limit
	} else {
		body["rowLimit"] = 25000
	}

	var resp struct {
		Rows []struct {
			Keys        []string `json:"keys"`
			Clicks      float64  `json:"clicks"`
			Impressions float64  `json:"impressions"`
			CTR         float64  `json:"ctr"`
			Position    float64  `json:"position"`
		} `json:"rows"`
	}
	u := fmt.Sprintf("%s/sites/%s/searchAnalytics/query", gscBaseURL, url.PathEscape(site))
	if err := c.postJSON(ctx, u, nil, body, &resp); err != nil {
		return nil, err
	}

	// Search Console returns metric values positionally in `keys`, in the same
	// order as the requested dimensions.
	rows := make([]map[string]any, 0, len(resp.Rows))
	for _, r := range resp.Rows {
		row := map[string]any{}
		for i, dim := range dims {
			if i < len(r.Keys) {
				for field, api := range gscDimensions {
					if api == dim {
						row[field] = r.Keys[i]
					}
				}
			}
		}
		// Only include the metrics the caller asked for.
		for _, f := range requested {
			switch f {
			case "clicks":
				row["clicks"] = r.Clicks
			case "impressions":
				row["impressions"] = r.Impressions
			case "ctr":
				row["ctr"] = r.CTR
			case "position":
				row["position"] = r.Position
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
