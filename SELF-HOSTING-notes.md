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
  `ads_keyword_ideas`, `ads_set_campaign_status` (confirm), `ads_push_negative_keywords` (confirm).
- **Flat JSON-API** voor Adlytica: `POST /query` en `POST /action`, met de vorm die de app
  al kende (`connector`, `account`, `fields`, `date_from`, `date_to`, `group_by`, `limit`
  → `{status, data, total_rows}`).

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

## Openstaand

- **Nog niet end-to-end getest**: GA4/GSC/Ads-calls naar de echte API's wachten op stap 1–3.
  Daarna per view de cijfers vergelijken met de Windsor-trekking over dezelfde periode.
- Adlytica's `src-tauri/src/windsor.rs` moet nog vervangen worden door een client op deze API
  (`POST /query`, `POST /action`), en `DEFAULT_ACCOUNTS` blijft gelijk (property-id, site-URL, customer-id).
- Schrijfacties zijn geïmplementeerd maar nog niet tegen een live campagne uitgevoerd.
