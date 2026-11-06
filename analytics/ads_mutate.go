package analytics

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Generieke Google Ads-mutate: create/update/remove op een vaste set resources.
//
// Twee ontwerpkeuzes die afwijken van "stuur maar wat naar de API":
//
//  1. Alleen resources in adsResourceRules zijn toegestaan. Een onbekende
//     resource geeft een fout met de lijst erbij in plaats van een willekeurige
//     :mutate-call.
//  2. bo's afspraak: zoekwoorden, negatieve zoekwoorden en advertenties mogen
//     verwijderd worden; campagnes en ad groups alleen naar PAUSED. Dat wordt
//     hier afgedwongen, niet in de UI van een client.
//
// Standaard gaat er niets naar buiten zonder dat de caller bevestigt; het
// bevestigen zelf gebeurt één laag hoger (tools.go / api.RunAction).
// ---------------------------------------------------------------------------

type adsResourceRule struct {
	endpoint      string // camelCase segment in de mutate-URL
	parentKey     string // id-veld voor child-resources (adhoc: ad_group_id)
	create        bool
	update        bool
	remove        bool
	removeBlocked string // uitleg waarom remove niet mag
}

var adsResourceRules = map[string]adsResourceRule{
	"campaign":              {endpoint: "campaigns", create: true, update: true, removeBlocked: "campagnes pauzeren we (status PAUSED) in plaats van verwijderen"},
	"campaign_budget":       {endpoint: "campaignBudgets", create: true, update: true, removeBlocked: "budgetten verwijderen we niet via deze tool"},
	"ad_group":              {endpoint: "adGroups", create: true, update: true, removeBlocked: "ad groups pauzeren we (status PAUSED) in plaats van verwijderen"},
	"ad_group_criterion":    {endpoint: "adGroupCriteria", parentKey: "ad_group_id", create: true, update: true, remove: true},
	"campaign_criterion":    {endpoint: "campaignCriteria", parentKey: "campaign_id", create: true, update: true, remove: true},
	"ad_group_ad":           {endpoint: "adGroupAds", parentKey: "ad_group_id", create: true, update: true, remove: true},
	"ad_group_bid_modifier": {endpoint: "adGroupBidModifiers", parentKey: "ad_group_id", create: true, update: true, remove: true},
	"conversion_action":     {endpoint: "conversionActions", update: true, removeBlocked: "conversieacties maken/verwijderen gaat via de Ads-UI"},
	// CustomerConversionGoal kent geen create: je upsert een doel met
	// category~origin als naam. Bestaat het doel nog niet, dan moet de Ads-UI
	// het aanmaken — de API geeft dan RESOURCE_NOT_FOUND.
	"customer_conversion_goal": {endpoint: "customerConversionGoals", update: true, removeBlocked: "accountdoelen verwijderen we niet via deze tool — zet biddable op false"},
	"asset":                    {endpoint: "assets", create: true, update: true, removeBlocked: "assets laten we staan zolang ze aan een campagne hangen"},
	"campaign_asset":           {endpoint: "campaignAssets", parentKey: "campaign_id", create: true, update: true, remove: true},
	"shared_set":               {endpoint: "sharedSets", create: true, update: true, removeBlocked: "gedeelde sets verwijderen we niet via deze tool"},
	"shared_criterion":         {endpoint: "sharedCriteria", parentKey: "shared_set_id", create: true, update: true, remove: true},
}

// AdsMutationResources lists the resources the generic mutate accepts.
func AdsMutationResources() []string {
	names := make([]string, 0, len(adsResourceRules))
	for name := range adsResourceRules {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// MutateOperation is één create, update of remove.
type MutateOperation struct {
	Action       string         // create | update | remove
	Resource     string         // resource uit adsResourceRules
	Object       map[string]any // velden (snake_case of camelCase mogen door elkaar)
	UpdateMask   []string       // alleen bij update; leeg = afgeleid uit Object
	ResourceName string         // update/remove: volledige resourceName (of laat hem afleiden uit Object)
}

// MutateOptions: validateOnly laat Google de mutatie keuren zonder hem toe te
// passen, partialFailure gaat door bij deels mislukte operaties.
type MutateOptions struct {
	ValidateOnly   bool
	PartialFailure bool
}

// MutateResult rapporteert wat de API teruggaf.
type MutateResult struct {
	ResourceNames []string `json:"resource_names"`
	Operations    int      `json:"operations"`
	Validated     bool     `json:"validated_only"`
	Partial       bool     `json:"partial_failure"`
	Warnings      []string `json:"warnings,omitempty"`
}

// AdsMutate voert de operaties uit, gegroepeerd per resource (één API-call per
// resource, want een :mutate-endpoint hoort bij precies één resource).
func (c *Client) AdsMutate(ctx context.Context, customerID string, ops []MutateOperation, opts MutateOptions, loginCustomerID string) (MutateResult, error) {
	customerID = normaliseCustomerID(customerID)
	if customerID == "" {
		return MutateResult{}, fmt.Errorf("google_ads: customer id is verplicht")
	}
	grouped, order, err := buildAdsMutateOperations(customerID, ops)
	if err != nil {
		return MutateResult{}, err
	}

	res := MutateResult{Operations: len(ops), Validated: opts.ValidateOnly, Partial: opts.PartialFailure}
	for _, resource := range order {
		body := map[string]any{"operations": grouped[resource]}
		if opts.ValidateOnly {
			body["validateOnly"] = true
		}
		if opts.PartialFailure {
			body["partialFailure"] = true
		}
		var resp struct {
			Results []struct {
				ResourceName string `json:"resourceName"`
			} `json:"results"`
			PartialFailureError struct {
				Message string `json:"message"`
			} `json:"partialFailureError"`
		}
		u := fmt.Sprintf("%s/customers/%s/%s:mutate", adsBaseURL, customerID, adsResourceRules[resource].endpoint)
		if err := c.postJSON(ctx, u, adsHeaders(loginCustomerID), body, &resp); err != nil {
			return res, err
		}
		if resp.PartialFailureError.Message != "" {
			res.Warnings = append(res.Warnings, truncate(resp.PartialFailureError.Message, 400))
		}
		for _, r := range resp.Results {
			if r.ResourceName != "" {
				res.ResourceNames = append(res.ResourceNames, r.ResourceName)
			}
		}
	}
	return res, nil
}

// buildAdsMutateOperations valideert de operaties en bouwt de request-bodies.
// Los van AdsMutate zodat de tests geen HTTP nodig hebben.
func buildAdsMutateOperations(customerID string, ops []MutateOperation) (map[string][]map[string]any, []string, error) {
	if len(ops) == 0 {
		return nil, nil, fmt.Errorf("google_ads: minstens één operatie is verplicht")
	}
	if len(ops) > 1000 {
		return nil, nil, fmt.Errorf("google_ads: maximaal 1000 operaties per call (kreeg %d)", len(ops))
	}

	grouped := map[string][]map[string]any{}
	order := make([]string, 0, 1)
	for i, op := range ops {
		resource := strings.ToLower(strings.TrimSpace(op.Resource))
		rule, ok := adsResourceRules[resource]
		if !ok {
			return nil, nil, fmt.Errorf("google_ads: operatie %d: onbekende resource %q (kies uit: %s)",
				i+1, op.Resource, strings.Join(AdsMutationResources(), ", "))
		}
		action := strings.ToLower(strings.TrimSpace(op.Action))
		switch action {
		case "create", "update", "remove":
		case "":
			return nil, nil, fmt.Errorf("google_ads: operatie %d: action is verplicht (create, update of remove)", i+1)
		default:
			return nil, nil, fmt.Errorf("google_ads: operatie %d: onbekende action %q (kies uit create, update of remove)", i+1, op.Action)
		}

		operation := map[string]any{}
		switch action {
		case "create":
			if !rule.create {
				return nil, nil, fmt.Errorf("google_ads: operatie %d: %s kan niet via deze tool aangemaakt worden", i+1, resource)
			}
			if len(op.Object) == 0 {
				return nil, nil, fmt.Errorf("google_ads: operatie %d: create op %s vereist een object met velden", i+1, resource)
			}
			operation["create"] = normaliseAdsObject(op.Object)

		case "update":
			if !rule.update {
				return nil, nil, fmt.Errorf("google_ads: operatie %d: %s kan niet via deze tool bijgewerkt worden", i+1, resource)
			}
			object := normaliseAdsObject(op.Object)
			name := strings.TrimSpace(op.ResourceName)
			if name == "" {
				name = composeAdsResourceName(customerID, resource, object)
			}
			if name == "" {
				return nil, nil, fmt.Errorf("google_ads: operatie %d: update op %s vereist een resource_name (of de ids waarmee we die kunnen opbouwen)", i+1, resource)
			}
			// De ids dienden alleen om resourceName te bouwen; ze zijn niet
			// updatebaar en horen dus niet in de body of de update mask.
			stripAdsIDHelpers(object)
			object["resourceName"] = name
			if status, ok := object["status"].(string); ok && strings.EqualFold(status, "REMOVED") {
				if resource == "campaign" || resource == "ad_group" {
					return nil, nil, fmt.Errorf("google_ads: operatie %d: %s zetten we op PAUSED, niet op REMOVED", i+1, resource)
				}
			}
			mask := toStringSlice(op.UpdateMask)
			if len(mask) == 0 {
				mask = deriveAdsUpdateMask(object)
			}
			if len(mask) == 0 {
				return nil, nil, fmt.Errorf("google_ads: operatie %d: update op %s heeft velden nodig (of een expliciete update_mask)", i+1, resource)
			}
			operation["updateMask"] = strings.Join(mask, ",")
			operation["update"] = object

		case "remove":
			if !rule.remove {
				hint := rule.removeBlocked
				if hint == "" {
					hint = "deze resource is niet verwijderbaar via deze tool"
				}
				return nil, nil, fmt.Errorf("google_ads: operatie %d: %s niet verwijderen — %s", i+1, resource, hint)
			}
			name := strings.TrimSpace(op.ResourceName)
			if name == "" {
				name = composeAdsResourceName(customerID, resource, normaliseAdsObject(op.Object))
			}
			if name == "" {
				return nil, nil, fmt.Errorf("google_ads: operatie %d: remove op %s vereist een resource_name", i+1, resource)
			}
			operation["remove"] = name
		}

		if _, seen := grouped[resource]; !seen {
			order = append(order, resource)
		}
		grouped[resource] = append(grouped[resource], operation)
	}
	return grouped, order, nil
}

// AdsMutationResourceName builds a full resource name for update/remove.
// Top-level resources take one id, criteria and ads a parent id plus the item id
// (the API joins those with a tilde).
func AdsMutationResourceName(customerID, resource, parentID, itemID string) (string, error) {
	rule, ok := adsResourceRules[resource]
	if !ok {
		return "", fmt.Errorf("google_ads: onbekende resource %q (kies uit: %s)", resource, strings.Join(AdsMutationResources(), ", "))
	}
	customerID = normaliseCustomerID(customerID)
	if customerID == "" {
		return "", fmt.Errorf("google_ads: customer id is verplicht")
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return "", fmt.Errorf("google_ads: id ontbreekt voor %s", resource)
	}
	if rule.parentKey == "" {
		return fmt.Sprintf("customers/%s/%s/%s", customerID, rule.endpoint, itemID), nil
	}
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return "", fmt.Errorf("google_ads: %s vereist een %s", resource, rule.parentKey)
	}
	return fmt.Sprintf("customers/%s/%s/%s~%s", customerID, rule.endpoint, parentID, itemID), nil
}

// composeAdsResourceName leest de ids uit het object als de caller geen
// resource_name meegaf. Onbekend/onvolledig betekent: zelf resource_name sturen.
func composeAdsResourceName(customerID, resource string, object map[string]any) string {
	rule := adsResourceRules[resource]
	if rule.parentKey == "" {
		return ""
	}
	parent := adsObjectString(object, rule.parentKey)
	item := adsObjectString(object, "id", "criterion_id", "criterionId", "ad_id", "adId", "asset_id", "assetId")
	name, err := AdsMutationResourceName(customerID, resource, parent, item)
	if err != nil {
		return ""
	}
	return name
}

// deriveAdsUpdateMask lists the leaf paths of an update object, snake_cased and
// dotted, so Google updates exactly the fields we send and nothing else.
func deriveAdsUpdateMask(object map[string]any) []string {
	mask := make([]string, 0, len(object))
	for key, value := range object {
		if key == "resourceName" || key == "resource_name" {
			continue
		}
		path := toSnakePath(key)
		if nested, ok := value.(map[string]any); ok {
			for _, sub := range deriveAdsUpdateMask(nested) {
				mask = append(mask, path+"."+sub)
			}
			continue
		}
		mask = append(mask, path)
	}
	sort.Strings(mask)
	return mask
}

// normaliseAdsObject camelCases every key recursively (Google's REST-JSON is
// camelCase; GAQL and its docs are snake_case) and drops nil values. A flat
// dotted key like "keyword.text" is expanded into nesting, so that form works
// too instead of landing as an unknown field at Google.
func normaliseAdsObject(object map[string]any) map[string]any {
	out := make(map[string]any, len(object))
	for key, value := range object {
		if value == nil {
			continue
		}
		path := strings.Split(toCamelPath(strings.TrimSpace(key)), ".")
		if path[0] == "" {
			continue
		}
		assignAdsPath(out, path, normaliseAdsValue(value))
	}
	return out
}

// assignAdsPath sets a (possibly nested) field in target.
func assignAdsPath(target map[string]any, path []string, value any) {
	if len(path) == 0 {
		return
	}
	if len(path) == 1 {
		target[path[0]] = value
		return
	}
	nested, ok := target[path[0]].(map[string]any)
	if !ok {
		nested = map[string]any{}
		target[path[0]] = nested
	}
	assignAdsPath(nested, path[1:], value)
}

func normaliseAdsValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return normaliseAdsObject(v)
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, normaliseAdsValue(item))
		}
		return out
	case []map[string]any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, normaliseAdsObject(item))
		}
		return out
	default:
		return value
	}
}

// stripAdsIDHelpers removes the id fields that were only there to compose a
// resource name; Google rejects them inside an update body.
func stripAdsIDHelpers(object map[string]any) {
	for _, key := range []string{"id", "criterionId", "adId", "assetId", "adGroupId", "campaignId", "sharedSetId"} {
		delete(object, key)
	}
}

// adsObjectString reads the first present key, accepting camelCase too.
func adsObjectString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		for _, candidate := range []string{key, toCamelPath(key)} {
			if value, ok := object[candidate]; ok {
				switch v := value.(type) {
				case string:
					if strings.TrimSpace(v) != "" {
						return strings.TrimSpace(v)
					}
				case float64:
					return fmt.Sprintf("%.0f", v)
				}
			}
		}
	}
	return ""
}

// toSnakePath converts a camelCase key back to the snake_case spelling used in
// update masks (cpcBidMicros -> cpc_bid_micros).
func toSnakePath(key string) string {
	var b strings.Builder
	for i, r := range key {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// CampaignBudgetResourceName looks up the budget resource of one campaign. The
// budget is a resource of its own, so a budget change needs this name first.
func (c *Client) CampaignBudgetResourceName(ctx context.Context, customerID, campaignID, loginCustomerID string) (string, error) {
	customerID = normaliseCustomerID(customerID)
	campaignID = strings.TrimSpace(campaignID)
	if customerID == "" || campaignID == "" {
		return "", fmt.Errorf("google_ads: customer id en campaign id zijn verplicht")
	}
	query := fmt.Sprintf("SELECT campaign.campaign_budget FROM campaign WHERE campaign.id = %s LIMIT 1", campaignID)
	rows, err := c.adsSearch(ctx, customerID, query, loginCustomerID)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("google_ads: campagne %s niet gevonden", campaignID)
	}
	campaign, ok := rows[0]["campaign"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("google_ads: geen campagne-object in de respons")
	}
	for _, key := range []string{"campaignBudget", "campaign_budget"} {
		if name, ok := campaign[key].(string); ok && strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name), nil
		}
	}
	return "", fmt.Errorf("google_ads: campagne %s heeft geen budget-resource", campaignID)
}

// ---------------------------------------------------------------------------
// Ingekorte operaties voor de veelvoorkomende gevallen
// ---------------------------------------------------------------------------

// KeywordInput is één zoekwoord voor AddKeywords.
type KeywordInput struct {
	Text         string
	MatchType    string // BROAD (default), PHRASE, EXACT
	Status       string // ENABLED (default) of PAUSED
	CPCBidMicros int64  // optioneel maximum-cpc-bod
}

// AddKeywords voegt zoekwoorden toe aan één ad group.
func (c *Client) AddKeywords(ctx context.Context, customerID, adGroupID string, keywords []KeywordInput, opts MutateOptions, loginCustomerID string) (MutateResult, error) {
	customerID = normaliseCustomerID(customerID)
	adGroupID = strings.TrimSpace(adGroupID)
	if customerID == "" || adGroupID == "" {
		return MutateResult{}, fmt.Errorf("google_ads: customer id en ad_group_id zijn verplicht")
	}
	if len(keywords) == 0 {
		return MutateResult{}, fmt.Errorf("google_ads: minstens één zoekwoord is verplicht")
	}

	ops := make([]MutateOperation, 0, len(keywords))
	for _, keyword := range keywords {
		text := strings.TrimSpace(keyword.Text)
		if text == "" {
			return MutateResult{}, fmt.Errorf("google_ads: leeg zoekwoord in de lijst")
		}
		matchType := keyword.MatchType
		if strings.TrimSpace(matchType) == "" {
			matchType = "BROAD"
		}
		matchType = adsMatchType(matchType)
		if matchType == "" {
			return MutateResult{}, fmt.Errorf("google_ads: onbekend match_type %q bij %q", keyword.MatchType, text)
		}
		status := strings.ToUpper(strings.TrimSpace(keyword.Status))
		switch status {
		case "":
			status = "ENABLED"
		case "ENABLED", "PAUSED":
		default:
			return MutateResult{}, fmt.Errorf("google_ads: status moet ENABLED of PAUSED zijn, kreeg %q", keyword.Status)
		}
		criterion := map[string]any{
			"ad_group": fmt.Sprintf("customers/%s/adGroups/%s", customerID, adGroupID),
			"status":   status,
			"keyword":  map[string]any{"text": text, "match_type": matchType},
		}
		if keyword.CPCBidMicros > 0 {
			criterion["cpc_bid_micros"] = fmt.Sprintf("%d", keyword.CPCBidMicros)
		}
		ops = append(ops, MutateOperation{Action: "create", Resource: "ad_group_criterion", Object: criterion})
	}
	return c.AdsMutate(ctx, customerID, ops, opts, loginCustomerID)
}

// SetAdsStatus flips the status of one existing item. REMOVED is only accepted
// for resources bo allows removing; campaigns and ad groups stop at PAUSED.
func (c *Client) SetAdsStatus(ctx context.Context, customerID, resource, resourceName, status string, opts MutateOptions, loginCustomerID string) (MutateResult, error) {
	resource = strings.ToLower(strings.TrimSpace(resource))
	rule, ok := adsResourceRules[resource]
	if !ok {
		return MutateResult{}, fmt.Errorf("google_ads: onbekende resource %q (kies uit: %s)", resource, strings.Join(AdsMutationResources(), ", "))
	}
	status = strings.ToUpper(strings.TrimSpace(status))
	switch status {
	case "ENABLED", "PAUSED":
	case "REMOVED":
		if !rule.remove {
			hint := rule.removeBlocked
			if hint == "" {
				hint = "deze resource verwijderen we niet via deze tool"
			}
			return MutateResult{}, fmt.Errorf("google_ads: %s niet op REMOVED zetten — %s", resource, hint)
		}
	case "":
		return MutateResult{}, fmt.Errorf("google_ads: status is verplicht (ENABLED, PAUSED of REMOVED)")
	default:
		return MutateResult{}, fmt.Errorf("google_ads: status moet ENABLED, PAUSED of REMOVED zijn, kreeg %q", status)
	}
	if strings.TrimSpace(resourceName) == "" {
		return MutateResult{}, fmt.Errorf("google_ads: resource_name is verplicht")
	}
	return c.AdsMutate(ctx, customerID, []MutateOperation{{
		Action:       "update",
		Resource:     resource,
		ResourceName: resourceName,
		Object:       map[string]any{"status": status},
		UpdateMask:   []string{"status"},
	}}, opts, loginCustomerID)
}

// RemoveAdsResource removes one item. Only resources with remove=true in the
// registry get here, so a campaign cannot be deleted by accident.
func (c *Client) RemoveAdsResource(ctx context.Context, customerID, resource, resourceName string, opts MutateOptions, loginCustomerID string) (MutateResult, error) {
	return c.AdsMutate(ctx, customerID, []MutateOperation{{
		Action:       "remove",
		Resource:     resource,
		ResourceName: resourceName,
	}}, opts, loginCustomerID)
}

// SetConversionActionSettings changes how a conversion action counts. Beware:
// the API refuses fields a GA4-imported action does not allow — validateOnly
// first, and read the action back afterwards.
func (c *Client) SetConversionActionSettings(ctx context.Context, customerID, conversionActionID string, primaryForGoal, includeInConversionsMetric *bool, status string, opts MutateOptions, loginCustomerID string) (MutateResult, error) {
	customerID = normaliseCustomerID(customerID)
	if customerID == "" {
		return MutateResult{}, fmt.Errorf("google_ads: customer id is verplicht")
	}
	resourceName, err := AdsMutationResourceName(customerID, "conversion_action", "", conversionActionID)
	if err != nil {
		return MutateResult{}, err
	}
	object := map[string]any{}
	mask := make([]string, 0, 3)
	if primaryForGoal != nil {
		object["primary_for_goal"] = *primaryForGoal
		mask = append(mask, "primary_for_goal")
	}
	if includeInConversionsMetric != nil {
		object["include_in_conversions_metric"] = *includeInConversionsMetric
		mask = append(mask, "include_in_conversions_metric")
	}
	if s := strings.ToUpper(strings.TrimSpace(status)); s != "" {
		switch s {
		case "ENABLED", "HIDDEN":
		case "REMOVED":
			return MutateResult{}, fmt.Errorf("google_ads: conversieacties verwijderen we niet via deze tool — zet hem op HIDDEN")
		default:
			return MutateResult{}, fmt.Errorf("google_ads: status moet ENABLED of HIDDEN zijn, kreeg %q", status)
		}
		object["status"] = s
		mask = append(mask, "status")
	}
	if len(mask) == 0 {
		return MutateResult{}, fmt.Errorf("google_ads: minstens één van primary_for_goal, include_in_conversions_metric of status is verplicht")
	}
	return c.AdsMutate(ctx, customerID, []MutateOperation{{
		Action:       "update",
		Resource:     "conversion_action",
		ResourceName: resourceName,
		Object:       object,
		UpdateMask:   mask,
	}}, opts, loginCustomerID)
}
