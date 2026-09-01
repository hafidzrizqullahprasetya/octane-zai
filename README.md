# autoclawpi

> Unofficial AutoClaw proxy — OpenAI-compatible API + web management panel

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-Linux-333?logo=linux)]()

**autoclawpi** is a self-hosted proxy for AutoClaw that provides an OpenAI-compatible API endpoint with multi-account round-robin, auto token refresh, daily check-in automation, and a full-featured web management panel.

---

## Features

### 🤖 OpenAI-Compatible API
- Drop-in replacement for OpenAI client — use any OpenAI library
- Multiple models: `auto`, `glm-5-turbo`, `glm-5.3`, `glm-5.3-flash`, `zaicoding_glm-5.3`
- Round-robin load balancing across accounts
- Auto token refresh on 401
- Token usage tracking & cost estimation
- API key authentication
- WAF prefix stripping — clean JSON responses
- Standard OpenAI format — strips `reasoning_content`, `completion_tokens_details`

### 🌐 Web Management Panel (Mobile-Friendly)
- **Dashboard** — account overview, points balance, API usage stats, cost tracking
- **Accounts** — OAuth login, import tokens, view details, JWT decode, health check
- **Check-In** — daily reward claiming for all accounts
- **Logs** — real-time API request log with terminal-style view
- **Settings** — password, multiple API keys, round-robin strategy
- **Health** — test & refresh all account tokens
- **API Docs** — endpoint reference with curl examples
- **Responsive** — mobile-friendly with hamburger menu, collapsible sidebar

### 🔐 Security
- Web panel password protection
- API key authentication for OpenAI endpoint
- JWT token auto-decode (user info extracted from token)
- Session-based panel login with 30-day expiry

---

## Quick Start

### Prerequisites
- Go 1.26+ (for building from source)
- Linux (tested on Zorin OS 18.1)

### Install

```bash
git clone https://github.com/hirotomasato/autoclawpi.git
cd autoclawpi
go build -o ~/.local/bin/autoclawpi ./cmd/autoclawpi
```

### Run

```bash
# Start server with password
autoclawpi serve --port 8787 --web-password yourpassword

# With API key protection
autoclawpi serve --port 8787 --web-password yourpassword --api-key sk-your-key
```

Open **http://localhost:8787** in your browser.

---

## Usage

### CLI Commands

```bash
# Login (OAuth via browser with captcha)
autoclawpi login

# Account management
autoclawpi account list
autoclawpi account show 1
autoclawpi account add --token "Bearer eyJ..."
autoclawpi account remove 1

# Check-in all accounts
autoclawpi checkin

# Start server
autoclawpi serve --port 8787 --web-password yourpassword --api-key sk-your-key
```

### API

```bash
# List models
curl http://localhost:8787/v1/models

# Chat completion
curl -X POST http://localhost:8787/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model": "glm-5-turbo", "messages": [{"role": "user", "content": "Hello!"}]}'

# With API key
curl -X POST http://localhost:8787/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-your-key" \
  -d '{"model": "glm-5.3-flash", "messages": [{"role": "user", "content": "Hello!"}]}'

# Python SDK
python3 -c "
from openai import OpenAI
client = OpenAI(base_url='http://localhost:8787/v1', api_key='sk-your-key')
r = client.chat.completions.create(model='glm-5-turbo', messages=[{'role':'user','content':'halo'}])
print(r.choices[0].message.content)
"
```

### Web Panel Routes

| Route | Description |
|-------|-------------|
| `/` | Dashboard with stats |
| `/accounts` | Account management |
| `/accounts/login` | OAuth login |
| `/checkin` | Daily check-in |
| `/logs` | API request logs (terminal view) |
| `/settings` | Configuration, API keys |
| `/health` | Account health check |
| `/docs` | API documentation |
| `/login` | Panel login |

---

## Architecture

```
autoclawpi
├── cmd/autoclawpi/       # CLI entry point
│   ├── main.go           # Command routing, server startup
│   ├── login.go          # OAuth login (captcha widget)
│   ├── account.go        # Account management commands
│   └── checkin.go        # Check-in command
├── internal/
│   ├── client/           # AutoClaw HTTP API client
│   ├── config/           # Configuration loading
│   ├── db/               # SQLite database layer
│   ├── server/           # OpenAI-compatible proxy
│   ├── sign/             # X-Auth signature generation
│   ├── store/            # Credential storage
│   └── web/              # Web management panel
│       └── templates/    # HTML templates (glassmorphism design)
└── go.mod
```

### Data Flow

```
Client (OpenAI SDK) → autoclawpi (port 8787)
  ├── /v1/chat/completions → Round-robin account selection
  │   ├── Account #1 → AutoClaw API → Response → Token usage logged
  │   ├── Account #2 → AutoClaw API → Response → Token usage logged
  │   └── ... (auto failover on 401)
  └── Web Panel → SQLite → Account management, logs, config
```

---

## Configuration

### Server Options

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `8787` | Listen port |
| `--host` | `127.0.0.1` | Listen host |
| `--web-password` | `""` | Panel password (required) |
| `--api-key` | `""` | API key for OpenAI endpoint |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `AUTOCLAWPI_DIR` | Data directory (default: `~/.autoclawpi/`) |

---

## Storage

All data is stored in SQLite at `~/.autoclawpi/autoclawpi.db`:

| Table | Description |
|-------|-------------|
| `accounts` | OAuth tokens, user info, points balance |
| `checkin_log` | Daily check-in history |
| `logs` | API request logs (tokens, cost, timestamps) |
| `config` | Key-value settings |

---

## Models

| Model | Description | Status |
|-------|-------------|--------|
| `auto` | Default routing | ✅ |
| `glm-5-turbo` | Fast & capable | ✅ |
| `glm-5.3` | Latest GLM | ✅ |
| `glm-5.3-flash` | Lightweight, reasoning | ✅ |
| `zaicoding_glm-5.3` | Code-optimized | ✅ |

---

## Screenshots

```
┌─────────────────────────────────────────────┐
│  Dashboard                                   │
│  ┌──────┐ ┌──────┐ ┌──────┐                 │
│  │Accts │ │Active│ │Points│                 │
│  │  2   │ │  2   │ │ 73K  │                 │
│  └──────┘ └──────┘ └──────┘                 │
│  ┌──────┐ ┌──────┐ ┌──────┐                 │
│  │ Req  │ │Tokens│ │ Cost │                 │
│  │  10  │ │ 1.2K │ │¥0.00 │                 │
│  └──────┘ └──────┘ └──────┘                 │
│  ─── Accounts ─────────────────────────      │
│  ✓ non618999@gmail.com  36,954 pts           │
│  ✓ Account #6           36,200 pts           │
└─────────────────────────────────────────────┘
```

---

## Security

- **Panel access**: Password-protected (`--web-password`)
- **API access**: Optional API key (`--api-key`)
- **Token storage**: Encrypted at rest using AES-GCM
- **Session**: Cookie-based with 30-day expiry
- **JWT decode**: Auto-extract user info from tokens

---

## Why autoclawpi?

- **Self-hosted** — full control over your data
- **OpenAI-compatible** — use any existing OpenAI tooling
- **Multi-account** — round-robin with auto failover
- **Token management** — auto-refresh, never lose access
- **Monitoring** — real-time logs, cost tracking, health checks
- **Mobile-friendly** — responsive panel design

---

## License

MIT

---

*Built with ❤️ for the AutoClaw community*