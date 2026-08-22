# CLAUDE.md

Complete context for AI coding agents working on CBK. This document reflects the CURRENT state of the codebase and supersedes `Agents.md` (which describes an early prototype).

---

## What CBK Is

CBK ("Marketing OS") is an AI-powered advertising management platform:

1. Users connect their **Meta Ads** accounts via OAuth.
2. They add **AI providers** (Google Gemini or OpenRouter) with per-user keys.
3. An autonomous **agent** answers questions and executes ad operations through function calling — always behind human approval for anything that changes real campaigns.
4. An **analytics intelligence layer** converts raw Meta insights into normalized metrics, scores, health classifications, anomalies and goal progress.
5. A **decision engine** turns that intelligence into concrete recommended actions (`PAUSE_CAMPAIGN`, `DECREASE_BUDGET`, …) gated by explicit human approval and hard guardrails.

**Golden rule:** the system may PROPOSE anything but may only EXECUTE what an APPROVED proposal authorizes — and even then only through the guardrail-checked executor.

---

## Tech Stack

| Layer | Choice |
|---|---|
| Language | Go 1.26 |
| Router | `go-chi/chi/v5` (+ cors, middleware) |
| Database | SQLite via `modernc.org/sqlite` (pure Go, no cgo) |
| Auth | JWT HS256 (`golang-jwt/v5`): 1h access token + 30d refresh in HttpOnly cookie |
| Passwords | bcrypt |
| AI providers | Google Gemini (`generativelanguage.googleapis.com/v1beta`) and OpenRouter (`openrouter.ai/api/v1`, OpenAI-compatible) |
| Frontend | SvelteKit 5 + Tailwind 4 (git submodule at `ui/ui`, branch `dev`) |
| Config | `.env` via `joho/godotenv` |

### Environment variables (`.env`, gitignored)

```
GEMINI_API_KEY        # used by seed scripts/tests only; user keys live in DB
JWT_SECRET            # HS256 signing key
META_APP_ID
META_APP_SECRET       # never leaves the server
META_CALLBACK_URL     # e.g. http://localhost:5173/api/meta/callback
```

---

## Running Things

```bash
# apply migrations v1→v8 to app.db (idempotent)
go run ./migrations

# backend on :3000
go run ./cmd/serve

# frontend on :5173 (SvelteKit dev server)
cd ui/ui && npm install && npm run dev

# quality gates
go build ./... && go vet ./... && go test ./...

# frontend checks
cd ui/ui && npm run check && npm run build

# live read-only validation of Meta tools against the connected account
go run ./cmd/toolcheck
```

CORS allows origin `http://localhost:5173` with credentials. There is NO global request timeout middleware (agent runs and SSE streams are long-lived); client disconnect is the cancellation signal.

NOTE: `app.db` is currently tracked in git (historical choice). `cmd/db/main.go` is a scratch playground, not a migration runner.

---

## Directory Map

```
cmd/
  serve/          main.go — HTTP server bootstrap
  db/             scratch playground (not migrations)
  toolcheck/      live read-only validator for Meta tools
migrations/
  1.go            entry point; runs migrateV2..V8 in order
  2.go..8.go      one migration step per file
internals/
  core/           HTTP handlers + wiring (chi routes live here)
    api.go            router construction, CORS, middleware
    auth.go           register/login/refresh/logout/me
    ai.go             provider CRUD + plain text generation
    agent.go          /agent/chat + /agent/chat/stream (shared prepareAgentRun)
    agent_history.go  sessions/messages persistence API
    goals.go          marketing goal CRUD + lifecycle
    analytics.go      /analytics/* handlers
    optimization.go   /optimization/* handlers + approval workflow
    executor.go       executes APPROVED actions via Meta tools
    guardrails.go     hard safety limits + Validate()
    meta.go           dashboard REST handlers for Meta data
    ads_manager.go    campaign details handler
    helpers.go        user lookup, SaveAdsAccounts, graphGet
  agent/          agent runtime (provider-agnostic ReAct loop)
  ai/cloud/       AI provider abstraction (Gemini + OpenRouter adapters)
  analytics/      insights normalization + intelligence engine
  optimization/   decision engine + rule set
  metaclient/     shared Graph API choke point (rate-limit protection)
  ads/meta/       small legacy helper package
  tools/          the agent tool registry + Meta CRUD implementations
ui/ui/            SvelteKit frontend (submodule)
```

---

## Database Schema (after all migrations)

```
users(id, username UNIQUE, email UNIQUE, hashed_password,
      created_at, updated_at)

providers(id, user_id→users, provider_name /*model name*/,
      api_key, created_at, provider_type DEFAULT 'GEMINI')

meta_ads_accounts(id, user_id→users, ad_account_id, business_id,
      account_name, access_token, token_expires_at, currency,
      timezone_name, created_at, updated_at,
      UNIQUE(user_id, ad_account_id))

agent_sessions(id, user_id→users, title, goal_id NULL→marketing_goals,
      created_at, updated_at)          -- many sessions may share one goal

agent_messages(id, session_id→agent_sessions, role CHECK(user|assistant),
      content, steps JSON, trace JSON, model_name, created_at)

marketing_goals(id, user_id→users, name, description, objective,
      status CHECK(ACTIVE|PAUSED|COMPLETED|ARCHIVED),
      target_cpl, target_cpa, target_roas, target_conversions,
      budget, daily_budget, start_date, end_date,
      created_at, updated_at)

optimization_actions(id, user_id→users, object_type CHECK(CAMPAIGN|ADSET|AD),
      object_id, object_name, action, reason, rule_id, confidence,
      suggested_change JSON, status CHECK(PENDING|APPROVED|REJECTED|EXECUTED|FAILED),
      date_preset, created_at, decided_at, executed_at, error, result)
```

Migration convention: new file `migrations/N.go` with `migrateVN(db *sql.DB) error`; register it in `1.go`'s `migrators` slice. Use `strings.Contains(err.Error(), "duplicate column name")` when ALTERing (modernc wraps errors). NEVER edit old migrations.

---

## Authentication Flow

- `POST /auth/register` → creates user (bcrypt), returns 201, does NOT log in
- `POST /auth/login` → `{access_token, expires_in}` + refresh cookie (`refresh_token`, HttpOnly, Lax, 30d)
- `POST /auth/refresh` → rotates BOTH token and cookie
- `POST /auth/logout` → clears cookie
- `GET /me` → `{id, username, email}`
- Protected routes require `Authorization: Bearer <access>`; `AuthMiddleware` stores `map[string]any{"email": …}` under `UserContextKey`
- Handlers resolve the full user via `a.GetUser(r)` (DB hit per call) or `getUserID(r)`

JWT helpers: `NewJWT(email)`, `NewJWTRefresh(email)`, `DecodeJWT(token)` — MapClaims only, no structs.

---

## API Surface

### Auth & profile
```
POST /auth/register · POST /auth/login · POST /auth/refresh · POST /auth/logout
GET  /me
```

### AI providers (per-user; keys never returned after creation)
```
POST   /providers                 {model_name, api_key, provider_type: GEMINI|OPENROUTER}
GET    /providers                 → [{id, model_name, provider_type, created_at}]
DELETE /providers/{id}
POST   /ai/generate               {model_name, prompt} → plain text completion
```

### Meta integration
```
GET    /auth/meta/credential      → {app_id, callback_url} (app secret NEVER returned)
GET    /auth/meta/callback        → OAuth code exchange, saves accounts (POST body variant exists)
GET    /auth/meta/accounts        → connected accounts (access_token stripped, json:"-")
DELETE /auth/meta/accounts
GET    /meta/rate-limit/status    → metaclient protection counters
```

### Meta dashboard REST (human-paced reads)
```
GET /meta/campaigns?ad_account_id=
GET /meta/campaign/details?ad_account_id=&ad_campaign_id=
GET /meta/campaign/insights?ad_account_id=&ad_campaign_id=
GET /meta/adsets?ad_account_id=&ad_campaign_id=
GET /meta/ads?ad_account_id=&ad_set_id=
```

### Agent
```
POST /agent/chat          REST: full response
POST /agent/chat/stream   SSE: iteration/plan/action/observation events then final
POST /test                deprecated alias of /agent/chat

Request:  {model_name, prompt|message, messages?, session_id?, goal_id?}
Response: {session_id, status: completed|max_iterations_reached, iterations,
           response, goal, plan[], steps[], trace[], active_goal?}

GET    /agent/sessions                    list (updated_at DESC)
POST   /agent/sessions                    {title?}
GET    /agent/sessions/{id}/messages      stored history incl. steps + trace
DELETE /agent/sessions/{id}               cascades messages
```

All agent endpoints are user-scoped; session ownership enforced; goal resolution order = explicit `goal_id` → session's linked goal → none; only ACTIVE goals drive runs.

### Marketing goals
```
POST /goals                       {name*, objective*, description?, targets…}
GET  /goals[?status=ACTIVE]
GET/PATCH/DELETE /goals/{id}      PATCH = partial; status NOT patchable
POST /goals/{id}/pause | activate | complete
```
Structured metric fields are the contract for the future optimization engine; `description` is prose for humans/LLMs.

### Analytics intelligence (params: ad_account_id?, date_preset?, goal_id?)
```
GET /analytics/overview            account rollup + per-campaign scores + trends + anomalies
GET /analytics/campaigns           per-campaign analysis (fatigue included)
GET /analytics/adsets[?campaign_id=]
GET /analytics/ads[?ad_set_id=]
GET /analytics/trends[&campaign_id=|adset_id=|ad_id=]   daily series + directions
GET /analytics/anomalies           ≥2σ z-score spikes/drops on spend/ctr/cpc/cpa
GET /analytics/compare?ids=a,b,c[&scope=campaign|adset|ad]  ranked vs best
GET /analytics/recommendations     decision-engine output (not persisted)
```

### Optimization loop
```
POST /optimization/generate?level=campaigns|adsets   engine → PENDING proposals (deduped; NO_ACTION skipped)
GET  /optimization/actions?status=PENDING|APPROVED|…
POST /optimization/actions/{id}/approve              PENDING → APPROVED
POST /optimization/actions/{id}/reject               PENDING → REJECTED
POST /optimization/actions/{id}/execute              APPROVED → guardrails → Meta API → EXECUTED/FAILED
```

---

## Agent Runtime (`internals/agent`)

Provider-agnostic loop over a Gemini-style request format:

```
GOAL → PLAN(update_plan) → ACTION(tools) → OBSERVATION → DECISION(act|complete) ×≤15 → COMPLETION
```

- `Runtime{Provider ChatProvider, Tools *tools.ToolHandler, ToolCtx, MaxIterations, ContextBlocks []string, Events func(Event)}`
- `ChatProvider` interface = `Chat(tools.GenerateContentRequest) (*tools.GenerateContentResponse, error)` — satisfied by both cloud adapters; stub it in tests
- System prompt enforces: never invent data, budgets in minor units, destructive deletes need explicit user request (prefer pause), plan maintenance via internal `update_plan` tool (handled BY the runtime, not Meta)
- `ContextBlocks`: core injects the MARKETING GOAL block (built by `core.goalContextBlock`) which teaches goal ≠ instruction
- Every iteration recorded as a `TraceEntry` (thought/actions/observations/decision); flattened into `steps[]` for storage/UI; full trace JSON persisted too
- SSE events: `iteration`, `plan`, `action`, `observation`, then a `final` frame carrying the whole response payload; errors stream as `{type:"error"}`

## AI Provider Abstraction (`internals/ai/cloud`)

```go
type Provider interface {
    Chat(tools.GenerateContentRequest) (*tools.GenerateContentResponse, error)
    GenerateText(prompt string) (string, error)
}
func New(providerType, apiKey, model string) (Provider, error) // GEMINI|OPENROUTER
```

OpenRouter adapter translates bidirectionally: system instruction → `system` message, uppercase Gemini schema types → lowercase JSON-schema, `functionCall`/`functionResponse` parts ↔ assistant `tool_calls`/`tool` messages with FIFO synthetic tool-call-ID pairing. Adding a provider = implement two methods + register in factory + whitelist in `normalizeProviderType`.

## Tools (`internals/tools`) — 28 registered

Meta CRUD (each has list/get/create/update/delete/activate/pause variants):
- Campaigns: `list_campaigns, get_campaign, create_campaign, update_campaign, delete_campaign, activate_campaign, pause_campaign`
- Ad sets: same 7 with `*adset*` names (`create_adset` validates goal↔billing pairing and requires `promoted_object` pixel for OFFSITE_CONVERSIONS/QUALITY_LEAD/VALUE)
- Ads: same 7 (`create_ad` wraps `creative_id`)
- Insights: `get_campaign_insights, get_adset_insights, get_ad_insights, compare_campaigns` (batch ≤10)
- Intelligence: `get_analytics` (normalized metrics + scores + health + fatigue + optional goal progress)
- Decisions: `get_recommendations` (decision-engine plan), `optimize_campaign` (**dry_run default true**; false stores PENDING proposals via `OptimizationPersister` hook)

Registry invariant: `TestAllTools_Execute` asserts every registered tool executes through `ExecuteTool` AND that the test table count equals registry size — adding a tool without coverage fails CI. Update `content_request_test.go` expected-name list too.

Error handling: `metaRequest` enriches failures with `error_user_msg`, `error_user_title`, `blame_field_specs`, codes/subcodes so the model can self-correct.

## Analytics Engine (`internals/analytics`)

- `NormalizeInsight(rawRow) Metrics` → 13-field normalized set (spend, impressions, reach, clicks, CTR%, CPC, CPM, frequency, leads, purchases, conversions, conversion_value, CVR%, CPL, CPA, ROAS). Action-type classification: contains "lead" → lead; contains "purchase" → purchase; conversions = purchases else leads. Ratios recomputed for consistency.
- `DeriveRatios(m)` fills missing derived fields — intelligence functions call this first so hand-built Metrics behave like fetched ones (never clobbers explicit values).
- Intelligence: `ScorePerformance` (0–100 composite vs benchmarks/goal targets, with reasons), `AssessHealth` (HEALTHY/WATCH/CRITICAL/NOT_DELIVERING/PAUSED), `AssessBudgetEfficiency` (share-of-spend vs share-of-results, pacing), `AssessFatigue` (last-7d vs prior-7d CTR decay × frequency rise), `DetectAnomalies` (≥2σ z-scores), `TrendDirections` (first-half vs second-half), `AssessGoalProgress(m, GoalSnapshot, days)` → on_track/at_risk/off_track + human summary.
- `GoalSnapshot` is the neutral bridge from `core.MarketingGoal` — analytics must not import core.

## Decision Engine (`internals/optimization`) — proposals ONLY

Rules (highest-confidence fired decision wins; KEEP is deliberately lowest confidence):
| Rule | Trigger → Action |
|---|---|
| not_delivering | active, 0 impressions ≥7d → PAUSE_* (0.9) |
| cpa/cpl_above_target | ≥125% of target → cut −20/−30%; ≥200% → PAUSE |
| roas_below_target | <85% target → cut; <50% → PAUSE |
| zero_result_spend | spend ≥100, ≥7d, 0 conversions → −30% |
| creative_fatigue | moderate/high at >5k impressions → REFRESH_CREATIVE |
| high_performer_underpaced | efficiency ≥1.3, score ≥70, pacing <80% → +20% |
| healthy_keep | fallback (0.45 — anything actionable outranks it) |

Action vocabulary is scope-aware: `PAUSE_CAMPAIGN|PAUSE_ADSET|PAUSE_AD`, plus `RESUME_CAMPAIGN` (reserved for executor), `INCREASE_BUDGET`, `DECREASE_BUDGET`, `REFRESH_CREATIVE`, `NO_ACTION`.

**Nothing here executes.** Execution requires: proposal APPROVED by human → `ExecuteOptimizationAction` → guardrails → Meta tools.

## Guardrails (`core/guardrails.go`, `DefaultGuardrails()`)

```
MaxIncreasePct 20 · MaxDecreasePct 30 · MinConversionsForScale 5
MinDataDays 7 · Cooldown 24h (per object, from execution history)
```
Also: never budget-change a paused campaign, pause-on-paused rejected, resume-only-from-paused, REFRESH_CREATIVE is manual-only, increases clamp to cap (never reject downward intent), decreases clamp to −30%. Validation reads LIVE status/budget from Meta before acting. Outcome persisted (`result` JSON with before/after).

## Rate-Limit Protection (`internals/metaclient`)

Single choke point for ALL Graph traffic (tools + analytics + dashboard + OAuth):

1. Token bucket: 60 calls/min, burst 10 — keyed per access token
2. Global concurrency cap: 4 in-flight
3. Usage tracking from `x-business-use-case-usage`/`x-app-usage` headers; slow lane >70% usage, hard self-block ≥90%
4. Circuit breaker per token on 429 / codes 4·17·32·613: 30s doubling to 15min, fail-FAST while open, honors Retry-After
5. Retries: ≤3 extra attempts, exponential backoff + jitter; 429/throttle-codes always retryable, 5xx/network only for GET/DELETE (ambiguous writes are NOT retried)
6. GET cache (45s TTL, ≤500 entries) + singleflight dedupe; any successful write flushes cache

Observability: `metaclient.StatusSnapshot()` → served at `/meta/rate-limit/status`.

When touching any code that calls Meta: route through `metaclient.Request` (or `tools.metaRequest`/`analytics.get`/`core.graphGet`). Direct `http.Client` calls to graph.facebook.com are forbidden outside the client itself.

---

## Frontend (`ui/ui`, SvelteKit 5)

Route groups:
```
src/routes/
  (public)/            landing (/), auth/login (tabbed sign-in/register)
  (app)/               auth-guarded shell (AppShell.svelte nav + user info + logout)
    dashboard/         overview: user card, stat tiles, accounts, recent chats
    dashboard/campaigns/  account selector → campaign grid → details + insights
    chat/              agent: SSE streaming, history sidebar, goal bar, model selector
    settings/ai/       add/remove AI models (type-aware hints, masked keys)
    auth/meta/         integrations (connect/disconnect Meta OAuth)
  api/meta/callback/+server.js   OAuth redirect target (stays OUTSIDE groups)
```

Conventions:
- Svelte 5 runes (`$state`, `$derived`, `$props`); JSDoc types in `src/lib/types.js` drive svelte-check — keep them in sync with Go responses
- API access via `apiFetch(path, init)` (auto bearer + credentials); streaming uses raw `fetch` + `ReadableStream` parsing `data:` frames
- Tailwind tokens: `bg-background/text-foreground/border-border/bg-card/bg-muted/text-primary` etc.
- Streaming UI: pending tool bubbles show "running…" until the matching observation arrives; `final` event is authoritative (drops unresolved pendings)
- Checks: `npm run check` (0 errors required), `npm run build`

---

## Testing

- Go: table-driven + httptest mocks; NEVER call real Meta/Gemini in tests. Mock servers swap base URLs via `tools.SetGraphBaseURL`, `analytics.SetGraphBaseURL`, `metaclient.SetBaseURL` (always restore with defer)
- `internals/tools`: registry completeness test (count must equal table), per-handler success/validation/Meta-error tests
- `internals/agent`: stub `ChatProvider` replays scripted model responses (plan → act → complete; error feedback; max iterations; context blocks)
- `internals/core`: `goalContextBlock` content tests; guardrail matrix tests
- `internals/metaclient`: cache dedupe, retry-on-throttle, breaker fail-fast, usage hard block, write-flushes-cache, counters
- Live smoke pattern (manual): start server, mint a temp user + JWT via sqlite/python, exercise endpoints, DELETE temp user (FK cascades clean up), kill server by port PID — never `pkill -f` patterns that match your own command line

---

## Security Invariants (non-negotiable)

1. Meta access tokens and app secrets NEVER appear in any API response (`json:"-"`; credential endpoint returns app_id + callback_url only)
2. User API keys stored plaintext-in-DB (acceptable today), never returned after creation, resolved server-side at call time
3. Every object-level operation verifies ownership (`user_id` scoping in SQL, not just auth)
4. No automatic execution of optimization actions — approval gate is architectural, not cosmetic
5. Parameterized SQL everywhere; no string-built queries with user input

## Code Style

- Small handlers; business logic in packages (`analytics`, `optimization`, `agent`), handlers only wire auth+parsing+dispatch
- Comments explain WHY, not WHAT; keep them sparse
- Go: `gofmt`, `go vet` clean, no unused imports (build fails)
- Commit style: short imperative subject + bullet body (see `git log`)
