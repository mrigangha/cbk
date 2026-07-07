# CBK Backend Project Context

## Overview

CBK is a Go backend that provides authentication, AI provider management, and AI text generation APIs.

The backend is designed to support multiple AI providers in the future. Currently only Google's Gemini API is implemented.

The frontend will be built using SvelteKit.

---

# Tech Stack

* Language: Go
* Router: Chi
* Database: SQLite (modernc.org/sqlite)
* Authentication: JWT
* Password Hashing: bcrypt
* AI Provider: Google Gemini
* Database Driver: modernc.org/sqlite

---

# Project Structure

```
internals/
    api/
    auth/
    ai/
        cloud/
            provider.go
            generate.go
            config.go

core/
    api.go
    auth.go
    middleware.go
    jwt.go
```

---

# Authentication

Authentication uses two JWTs.

## Access Token

* Lifetime: 1 hour
* Returned in login response
* Stored only in frontend memory (Svelte store)
* Sent as

```
Authorization: Bearer <token>
```

Used by every authenticated endpoint.

---

## Refresh Token

Lifetime:

30 days

Returned as

HttpOnly Cookie

Properties

* HttpOnly
* Secure (false during local development)
* SameSite=Strict

Frontend JavaScript cannot access this cookie.

When the access token expires the frontend calls

```
POST /auth/refresh
```

which returns a new access token.

---

# JWT

Current JWT payload

```
{
    "email": "...",
    "iat": ...,
    "exp": ...
}
```

There are currently two helper functions.

```
NewJWT(email)
```

Creates access token.

```
NewJWTRefresh(email)
```

Creates refresh token.

```
DecodeJWT(token)
```

Verifies and decodes tokens.

No JWT structs are used.

Only jwt.MapClaims.

---

# Database

## users

```
id
username
email
hashed_password
created_at
updated_at
```

---

## providers

```
id
user_id
provider_name
api_key
created_at
```

Current usage

provider_name temporarily stores the model name.

Example

```
gemini-3.5-flash
```

Later this field may become

```
model_name
```

or be redesigned.

---

# AI Provider

Current implementation

Google Gemini only.

Provider constructor

```
cloud.NewProvider(
    apiKey,
    modelName,
    "https://generativelanguage.googleapis.com/v1beta",
)
```

Generation

```
GenerateText(prompt)
```

The provider stores

* API Key
* Model Name
* Base URL

---

# API Endpoints

## Public

### Register

```
POST /auth/register
```

Creates user.

Returns

```
201 Created
```

Does NOT automatically log the user in.

---

### Login

```
POST /auth/login
```

Input

```
email
password
```

Response

```
{
    "access_token": "...",
    "expires_in": 3600
}
```

Also sets

```
refresh_token
```

as an HttpOnly cookie.

---

### Refresh

```
POST /auth/refresh
```

Uses

```
refresh_token
```

cookie.

Returns

```
{
    "access_token":"..."
}
```

---

# Protected Endpoints

Require

```
Authorization: Bearer <access token>
```

---

## GET /me

Returns authenticated user.

---

## POST /providers

Stores a provider for the authenticated user.

Request

```
{
    "model_name":"gemini-3.5-flash",
    "api_key":"AIza..."
}
```

Internally

```
model_name
```

is stored inside

```
provider_name
```

column.

---

## POST /ai/generate

Request

```
{
    "model_name":"gemini-3.5-flash",
    "prompt":"Explain transformers"
}
```

Flow

1. Read authenticated user.
2. Find provider belonging to that user.
3. Retrieve API key.
4. Create Gemini provider.
5. Call GenerateText().
6. Return response.

The frontend never sends API keys during generation.

---

# Security

Passwords

bcrypt

JWT

HS256

Refresh token

HttpOnly cookie

Access token

Authorization header

API keys

Stored only in database.

Never returned after creation.

Never exposed to frontend except when initially configuring.

---

# Frontend

Framework

SvelteKit

Authentication

Access token

Stored only inside a Svelte writable store.

Never use localStorage.

Refresh handled automatically through

```
POST /auth/refresh
```

using HttpOnly cookie.

---

# Current AI Support

Supported

Google Gemini

Current model

```
gemini-3.5-flash
```

Current endpoint

```
https://generativelanguage.googleapis.com/v1beta
```

---

# Future Plans

Planned support

* OpenAI
* Anthropic
* Groq
* OpenRouter
* Local models

The Provider interface should make switching providers easy.

---

# Coding Style

Use

* Small handlers
* Helper methods for database access
* Parameterized SQL
* Proper HTTP status codes
* Chi router
* JSON APIs only
* Keep business logic outside handlers whenever possible

---

# Important Notes

* Never hardcode API keys.
* Always load provider credentials from the database.
* provider_name currently represents the model name.
* The backend is designed to support multiple AI providers in the future.
* Authentication uses access tokens and refresh tokens.
* Registration never returns an access token.
* Login is the only way to obtain an access token.
* Refresh endpoint issues new access tokens using the refresh cookie.
* AI generation must always use the authenticated user's stored provider configuration.
