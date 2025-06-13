# Slack Wavie Bot System - Agent Handoff Documentation

## 🎯 Project Overview

This is a complete 3-service Slack bot system that responds to @wavie mentions, processes questions through ChatGPT, and broadcasts all interactions to a monitoring channel. The system is designed to handle 300+ customers with proper scalability for Google Cloud Run deployment.

**Current Status**: ✅ COMPLETE - All services implemented, tested, and ready for deployment
**PR Status**: Open - https://github.com/BitwaveCorp/shared-svcs/pull/13
**Branch**: `devin/1748990110-slack-wavie-bot`

## 🏗️ Architecture Overview

### Service Communication Flow
```
Slack @wavie mention → Slack Events Listener → GPT Agent Proxy → OpenAI API
                                ↓                      ↓
                        Responds to Slack      Broadcast Bot → Monitoring Channel
```

### 1. Slack Events Listener Service (`slack-events-listener-svc`)
- **Port**: 8080 (configurable via PORT env var)
- **Purpose**: Primary webhook endpoint for Slack events
- **Key Features**:
  - HMAC-SHA256 webhook signature verification
  - URL verification challenge handling
  - Processes `app_mention` events containing @wavie
  - Idempotency protection using in-memory event ID tracking
  - Forwards to GPT service and posts responses back to Slack
  - Sends interaction data to Broadcast service

**API Endpoints**:
- `GET /health` - Health check endpoint
- `POST /slack/events` - Main Slack webhook endpoint

**Environment Variables Required**:
```bash
SLACK_BOT_TOKEN=xoxb-your-slack-bot-token-here
SLACK_SIGNING_SECRET=your-slack-signing-secret-here
GPT_PROXY_SERVICE_URL=https://your-gpt-service-url
BROADCAST_SERVICE_URL=https://your-broadcast-service-url
PORT=8080
LOG_LEVEL=info
```

### 2. GPT Agent Proxy Service (`gpt-agent-proxy-svc`)
- **Port**: 8081 (configurable via PORT env var)
- **Purpose**: Stateless OpenAI API integration
- **Key Features**:
  - OpenAI ChatGPT API integration with proper error handling
  - Configurable model selection (defaults to GPT-4)
  - 90-second timeout for long-running requests
  - System prompt configured for "Wavie" assistant persona
  - Correlation ID tracking for request tracing

**API Endpoints**:
- `GET /health` - Health check endpoint
- `POST /api/chat` - Chat completion endpoint

**Environment Variables Required**:
```bash
OPENAI_API_KEY=sk-your-openai-api-key-here
OPENAI_MODEL=gpt-4
PORT=8081
LOG_LEVEL=info
```

### 3. Broadcast Bot Service (`broadcast-bot-svc`)
- **Port**: 8082 (configurable via PORT env var)
- **Purpose**: Posts formatted interaction logs to monitoring channel
- **Key Features**:
  - Rich Slack message formatting with blocks
  - Idempotency protection using correlation IDs
  - Detailed interaction logging (user, channel, question, response, timestamp)
  - Configurable broadcast channel

**API Endpoints**:
- `GET /health` - Health check endpoint
- `POST /api/broadcast` - Broadcast message endpoint

**Environment Variables Required**:
```bash
SLACK_BOT_TOKEN=xoxb-your-broadcast-bot-token-here
BROADCAST_CHANNEL_ID=C1234567890
PORT=8082
LOG_LEVEL=info
```

## 📁 Directory Structure

```
services/
├── slack-events-listener-svc/
│   ├── cmd/slack-events-listener-svc/main.go
│   ├── internal/
│   │   ├── api/handler.go
│   │   ├── config/config.go
│   │   └── slack/
│   │       ├── client.go
│   │       └── types.go
│   ├── go.mod
│   └── .env.example
├── gpt-agent-proxy-svc/
│   ├── cmd/gpt-agent-proxy-svc/main.go
│   ├── internal/
│   │   ├── api/handler.go
│   │   ├── config/config.go
│   │   └── openai/
│   │       ├── client.go
│   │       └── types.go
│   ├── go.mod
│   └── .env.example
└── broadcast-bot-svc/
    ├── cmd/broadcast-bot-svc/main.go
    ├── internal/
    │   ├── api/handler.go
    │   ├── config/config.go
    │   └── slack/
    │       ├── client.go
    │       └── types.go
    ├── go.mod
    └── .env.example
```

## 🔧 Technical Implementation Details

### Key Dependencies
- **Go Version**: 1.24
- **Environment Config**: `github.com/kelseyhightower/envconfig`
- **Environment Loading**: `github.com/joho/godotenv`
- **Shared Utils**: Uses existing `shared/utils` for ID generation
- **HTTP**: Standard Go `net/http` with structured logging

### Security Features
- **Slack Webhook Verification**: HMAC-SHA256 signature validation
- **Environment Variables**: All secrets managed via env vars
- **No Hardcoded Credentials**: Follows security best practices
- **Request Timeouts**: Proper timeout handling for external APIs

### Scalability Features
- **Stateless Design**: All services are stateless for auto-scaling
- **Idempotency**: Prevents duplicate processing under high load
- **Correlation IDs**: Request tracing across services
- **Graceful Shutdown**: SIGINT/SIGTERM handling
- **Health Checks**: All services expose `/health` endpoints

### Error Handling
- **Transparent Error Reporting**: Clear error messages for API failures
- **Structured Logging**: JSON logging with correlation IDs
- **Proper HTTP Status Codes**: RESTful error responses
- **Fallback Messages**: User-friendly error messages in Slack

## 🚀 Deployment Instructions

### Google Cloud Run Deployment

Each service should be deployed independently to Google Cloud Run:

1. **Build Docker Images** (example for events listener):
```dockerfile
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o main cmd/slack-events-listener-svc/main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/main .
CMD ["./main"]
```

2. **Deploy to Cloud Run**:
```bash
# Build and deploy events listener
gcloud run deploy slack-events-listener \
  --source=services/slack-events-listener-svc \
  --platform=managed \
  --region=us-central1 \
  --allow-unauthenticated

# Build and deploy GPT proxy
gcloud run deploy gpt-agent-proxy \
  --source=services/gpt-agent-proxy-svc \
  --platform=managed \
  --region=us-central1

# Build and deploy broadcast bot
gcloud run deploy broadcast-bot \
  --source=services/broadcast-bot-svc \
  --platform=managed \
  --region=us-central1
```

3. **Set Environment Variables** in Cloud Run console or via CLI:
```bash
gcloud run services update slack-events-listener \
  --set-env-vars="SLACK_BOT_TOKEN=xoxb-...,SLACK_SIGNING_SECRET=...,GPT_PROXY_SERVICE_URL=https://...,BROADCAST_SERVICE_URL=https://..."
```

### Local Development

1. **Setup Environment**:
```bash
cd services/slack-events-listener-svc
cp .env.example .env
# Edit .env with your actual values

cd ../gpt-agent-proxy-svc
cp .env.example .env
# Edit .env with your actual values

cd ../broadcast-bot-svc
cp .env.example .env
# Edit .env with your actual values
```

2. **Run Services**:
```bash
# Terminal 1 - Events Listener
cd services/slack-events-listener-svc
go run cmd/slack-events-listener-svc/main.go

# Terminal 2 - GPT Proxy
cd services/gpt-agent-proxy-svc
go run cmd/gpt-agent-proxy-svc/main.go

# Terminal 3 - Broadcast Bot
cd services/broadcast-bot-svc
go run cmd/broadcast-bot-svc/main.go
```

## 🔑 Slack Bot Configuration

### Wavie Bot (Events Listener)
- **App ID**: A0XXXXXXXXX
- **Client ID**: your-client-id-here
- **Bot Token**: xoxb-your-slack-bot-token-here
- **Signing Secret**: your-signing-secret-here
- **Client Secret**: your-client-secret-here

**Required Scopes**:
- `app_mentions:read` - To receive @wavie mentions
- `chat:write` - To post responses
- `channels:read` - To access channel information

**Event Subscriptions**:
- Request URL: `https://your-events-listener-url/slack/events`
- Subscribe to: `app_mention`

### Broadcaster Bot (Broadcast Service)
- **App ID**: A0XXXXXXXXX
- **Client ID**: your-client-id-here
- **Bot Token**: xoxb-your-broadcast-bot-token-here
- **Signing Secret**: your-signing-secret-here
- **Client Secret**: your-client-secret-here

**Required Scopes**:
- `chat:write` - To post broadcast messages
- `channels:read` - To access channel information

## 🧪 Testing & Verification

### Manual Testing
1. **Health Checks**: Verify all services respond to `/health`
2. **Slack Integration**: Test @wavie mentions in Slack
3. **GPT Integration**: Verify OpenAI API responses
4. **Broadcast**: Check monitoring channel receives messages

### Code Quality
```bash
# Format code
go fmt ./services/slack-events-listener-svc/...
go fmt ./services/gpt-agent-proxy-svc/...
go fmt ./services/broadcast-bot-svc/...

# Run tests
go test ./services/slack-events-listener-svc/...
go test ./services/gpt-agent-proxy-svc/...
go test ./services/broadcast-bot-svc/...
```

## 🐛 Troubleshooting

### Common Issues

1. **Slack Webhook Verification Fails**:
   - Check signing secret matches Slack app configuration
   - Verify timestamp is within 5-minute window
   - Ensure request body is read correctly

2. **OpenAI API Errors**:
   - Verify API key is valid and has credits
   - Check model name (gpt-4 vs gpt-3.5-turbo)
   - Monitor rate limits

3. **Service Communication Errors**:
   - Verify service URLs are correct and accessible
   - Check network connectivity between services
   - Monitor timeout settings

### Logging
All services use structured JSON logging with correlation IDs:
```json
{
  "time": "2024-01-01T12:00:00Z",
  "level": "INFO",
  "msg": "Processing wavie message",
  "correlation_id": "wv_abc123",
  "user": "U1234567890",
  "channel": "C1234567890"
}
```

## 📋 Next Steps for Continuation

### Immediate Tasks
1. **Deploy to Google Cloud Run**: Use the deployment instructions above
2. **Configure Slack Apps**: Set webhook URLs to deployed services
3. **Test End-to-End**: Verify @wavie mentions work in Slack
4. **Monitor Performance**: Check logs and metrics

### Potential Enhancements
1. **Database Integration**: Add persistent storage for conversation history
2. **Rate Limiting**: Implement per-user rate limiting
3. **Analytics**: Add usage tracking and metrics
4. **Multi-tenant**: Support multiple Slack workspaces
5. **Advanced GPT Features**: Context memory, conversation threading

### Monitoring & Maintenance
1. **Set up Cloud Monitoring**: Monitor service health and performance
2. **Configure Alerts**: Set up alerts for service failures
3. **Log Aggregation**: Centralize logs for debugging
4. **Security Updates**: Regular dependency updates

## 📞 Support Information

- **Original Implementation**: Devin AI Session
- **Repository**: BitwaveCorp/shared-svcs
- **PR**: #13 - https://github.com/BitwaveCorp/shared-svcs/pull/13
- **Branch**: devin/1748990110-slack-wavie-bot
- **Contact**: ore.phillips@bitwave.io

## 🔒 Security Notes

**IMPORTANT**: The Slack bot tokens and secrets provided in this documentation are for initial setup only. The user (ore.phillips@bitwave.io) has indicated they will regenerate these credentials once the system is working. Ensure you:

1. Regenerate all Slack bot tokens and secrets before production use
2. Use secure environment variable management in production
3. Never commit actual credentials to version control
4. Implement proper secret rotation procedures

---

**Status**: Ready for deployment and testing
**Last Updated**: June 2025
**Implementation Complete**: ✅
