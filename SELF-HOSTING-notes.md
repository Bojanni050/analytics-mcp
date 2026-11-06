# analytics-mcp — self-hosted (bo)

Vervangt de Windsor.ai-datalaag van **Adlytica** door directe Google-API's.
Eén service naast `gtm-mcp-server` (8095) en `google-ads-mcp` (8096).

## Waar staat het

| | |
|---|---|
| Code | `/home/hermes/apps/analytics-mcp` (nieuw; auth-laag uit `gtm-mcp-server`, BSD-3) |
| Image | `analytics-mcp:local` |
| Compose | `docker-compose.yml` in de root |
| Env | `.env` (mode 600, gitignored) |
| Tokens | named volume `analytics-mcp-tokens` → `TOKEN_STORE_PATH=/data/tokens.json` |
| Container | `analytics-mcp`, `restart: unless-stopped` |
| Luistert op | `127.0.0.1:8097` + `100.64.144.93:8097` (tailnet) → container 8080 |
| Publieke URL | `https://analytics.studiovanderheide.nl` (= `BASE_URL`) |
| Checks | `./verify.sh` (of `--local`) |

## Wat de service levert

Twee oppervlakken op dezelfde OAuth-stack:

- **MCP** (`POST /`): `ping`, `auth_status`, `ga4_report`, `gsc_query`, `ads_search`,
  `ads_keyword_ideas`, `ads_set_campaign_status`, `ads_push_negative_keywords`, `ads_mutate`,
  `ads_add_keywords`, `ads_set_keyword_status`, `ads_remove_keyword`, `ads_set_ad_group_status`,
  `ads_set_ad_status`, `ads_set_campaign_budget`, `ads_conversion_action_update`
  (16 tools; elke write eist `confirm: true`).
- **Flat JSON-API** voor Adlytica: `POST /query` en `POST /action`, met de vorm die de app
  al kende (`connector`, `account`, `fields`, `date_from`, `date_to`, `group_by`, `limit`
  → `{status, data, total_rows}`).

### Ads-schrijfacties (sinds 6 nov 2026)

`ads_mutate` is de generieke route: `{resource, op: create|update|remove, object, update_mask}`
voor `campaign`, `campaign_budget`, `ad_group`, `ad_group_criterion` (zoekwoorden),
`campaign_criterion` (negatieve zoekwoorden), `ad_group_ad`, `conversion_action`,
`customer_conversion_goal`, `ad_group_bid_modifier`, `asset`, `campaign_asset`, `shared_set`,
`shared_criterion`. Daarnaast ingekorte tools voor zoekwoorden, statussen, budget en
conversieacties. Vaste afspraken:

- Zonder `confirm: true` gebeurt er niets; `validate_only: true` laat Google de mutatie keuren
  zonder hem toe te passen (gebruik dat eerst).
- Campagnes en ad groups gaan alleen naar `PAUSED`, nooit `REMOVED`; zoekwoorden, advertenties
  en criteria mogen wel weg.
- **`primary_for_goal` en `include_in_conversions_metric` zijn immutabel** op een
  conversieactie (Google: `IMMUTABLE_FIELD`). "Meetellen voor bieden" zit op doelniveau:
  `customer_conversion_goal.biddable`, per `category~origin`. Dat doel bestaat alleen als
  Google het heeft gematerialiseerd — zet de conversieactie op `ENABLED` en het doel
  (`SUBMIT_LEAD_FORM~WEBSITE` voor GA4-leadimports) verschijnt meteen; een doel *aanmaken*
  kan niet via de API (`create` bestaat niet, `update` geeft `RESOURCE_NOT_FOUND`).
- Na elke mutatie hoort een read-back: `ads_mutate` geeft de resourceNames terug, en met
  `ads_search` lees je de velden opnieuw.
- `mcp-check.py` in de repo-root praat rechtstreeks met de MCP (initialize + tools/list +
  optioneel tools/call) en is de snelste manier om een write end-to-end te controleren.

Connectors: `googleanalytics4` (property-id), `searchconsole` (property-URL), `google_ads`
(customer-id). `google_ads` + `options.keyword_seeds` gaat naar de KeywordPlanIdeaService
en levert daar échte biedranges — iets wat Windsor nooit teruggaf.

## Wat al werkt (gemeten, 1 nov 2026)

```
container healthy (docker compose ps)
GET  /health                                   -> {"service":"analytics-mcp","status":"healthy","version":"0.1.0"}
GET  /.well-known/oauth-authorization-server    -> scopes: analytics.readonly, webmasters.readonly, adwords
POST /            (zonder token)               -> 401
POST /query       (zonder token)               -> 401
POST /action      (zonder token)               -> 401
POST /register    (loopback-redirect)          -> 201 + client_id (Adlytica-flow, DCR)
go vet ./... + go test ./analytics/            -> schoon; unit-test ving een echte unit-bug (average_cpc is micros)
```

## Wat bo nog moet doen (ik kan er niet bij)

### 1. Google Cloud (project `pivotal-stacker-497620-q7`, client `626693586365-…`)

1. APIs enablen: **Google Analytics Data API**, **Google Analytics Admin API**,
   **Google Search Console API** en **Google Ads API**.
2. Op de bestaande OAuth-client de redirect-URI toevoegen:
   `https://analytics.studiovanderheide.nl/oauth/callback`
   (meerdere redirect-URI's op één client mag; `/oauth/callback` voor deze server,
   `/auth/callback` voor de Ads-server).
3. Op de consent screen de drie scopes toevoegen:
   `.../auth/analytics.readonly`, `.../auth/webmasters.readonly`, `.../auth/adwords`.
4. Ads-toegangsniveau: sinds de developer tokens op 9 sept 2026 zijn gesunset hangt dat aan
   dit Cloud-project — de Ads-server werkt hier al mee, dus Explorer/brand-verified is geregeld.

### 2. Plesk

Subdomein `analytics.studiovanderheide.nl` + Let's Encrypt, en dezelfde proxy-directives
als GTM/Ads maar naar poort **8097**:

```nginx
location / {
    proxy_pass http://127.0.0.1:8097;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    proxy_read_timeout 300s;
}
```

### 3. Eenmalig inloggen

`GET /authorize` met de drie scopes → Google-consent → tokens in het volume.
Daarna is `auth_status` in MCP `authenticated: true`, en werkt `/query` met de bearer-token.

## Updaten

```bash
cd /home/hermes/apps/analytics-mcp
git pull                                   # zodra dit een repo is
docker compose build && docker compose up -d
./verify.sh
```

## Live sinds 1 november 2026 (gemeten)

Ingelogd via `https://analytics.studiovanderheide.nl/authorize` (redirect naar
`/dashboard/`, geen SSH-tunnel nodig; helper: `/home/hermes/apps/oauth-login-url.py`).
Het token staat in het volume: `tokens.json` (839 bytes, appuser, mode 600) — een
containerherstart behoudt de sessie, bevestigd door na de herstart opnieuw te queryen.

Vergelijking eigen service vs. de oude aggregator, periode 30-09 t/m 29-10-2026:

| Bron | Eigen service | Oude aggregator |
|---|---|---|
| Search Console | `white vision` 32 kliks / 851 vert. / CTR 0,0376 / pos. 3,3067 | identiek |
| GA4 (bronnen) | google 599, (direct) 456, ads.google.com 16 — 16 bronnen | identiek |
| Google Ads | €333,55 · 347 klikken · 9.027 vertoningen over 6 campagnes | identiek |

Onderweg gevonden en gefixt (allemaal met een test erbij): de advertentierespons komt als
één JSON-array (niet als NDJSON, dus niet regel-voor-regel parsen); Google-REST gebruikt
camelCase (`metrics.costMicros`), dus GAQL-paden moeten vertaald worden; en niet-metrics
mogen nooit door een getalparser (campagnenamen werden `null`).

### Keyword Planner: geblokkeerd door Google

`generateKeywordIdeas` geeft `403 DEVELOPER_TOKEN_NOT_APPROVED`: *"This method is not allowed
for use with explorer access. Please apply for basic or standard access."* De service vertaalt
dat nu naar één begrijpelijke regel. Zolang het Cloud-project op **Explorer** staat kan
Adlytica's tab **Keyword Search → ideeën** (volume, competitie, biedranges) dus niet via deze
service; de oude aggregator kon het wel omdat die zijn eigen goedgekeurde token heeft. Oplossing:
Basic- of Standard-access aanvragen (brand verification) op de Google Ads API-pagina van het
project. Alle andere Ads-lees- en schrijfoperaties werken wel.

## Openstaand

- **Nog niet end-to-end getest**: GA4/GSC/Ads-calls naar de echte API's wachten op stap 1–3.
  Daarna per view de cijfers vergelijken met de Windsor-trekking over dezelfde periode.
- Adlytica's `src-tauri/src/windsor.rs` moet nog vervangen worden door een client op deze API
  (`POST /query`, `POST /action`), en `DEFAULT_ACCOUNTS` blijft gelijk (property-id, site-URL, customer-id).
- Schrijfacties zijn geïmplementeerd maar nog niet tegen een live campagne uitgevoerd.
