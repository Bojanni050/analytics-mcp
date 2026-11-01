# analytics-mcp

Self-hosted MCP + flat JSON API voor **Google Analytics 4**, **Google Search
Console** en **Google Ads** (lezen én schrijven), als vervanging van Windsor.ai
in Adlytica.

- MCP-endpoint: `POST /` — tools `ga4_report`, `gsc_query`, `ads_search`,
  `ads_keyword_ideas`, `ads_set_campaign_status`, `ads_push_negative_keywords`
- JSON-API: `POST /query`, `POST /action` — Windsor-vormige request/response
- Auth: Google OAuth (scopes `analytics.readonly`, `webmasters.readonly`, `adwords`),
  DCR/CIMD voor desktop-clients, bestandstokenstore in een docker volume
- Opzet, status en de stappen die op de server en in de Cloud-console nodig zijn:
  zie `SELF-HOSTING-notes.md`; checks: `./verify.sh [--local]`

De auth-laag komt uit [gtm-mcp-server](https://github.com/paolobietolini/gtm-mcp-server)
(BSD-3-Clause, zie `LICENSE`); de API-clients en tools zijn nieuw.
