#!/bin/bash

set -e

# Configuration
PROJECT_ID=$(gcloud config get-value project)
REGION="us-central1"

if [ -z "$PROJECT_ID" ]; then
    echo "❌ Please set your GCP project: gcloud config set project YOUR-PROJECT-ID"
    exit 1
fi

echo "🚀 Deploying Wavie Claude Bot to Google Cloud Run"
echo "📋 Project: $PROJECT_ID"
echo "🌍 Region: $REGION"
echo ""

# Check required environment variables
if [ -z "$ANTHROPIC_API_KEY" ]; then
    echo "❌ ANTHROPIC_API_KEY is required. Set it in your environment or .env file"
    exit 1
fi

if [ -z "$BROADCAST_CHANNEL_ID" ]; then
    echo "❌ BROADCAST_CHANNEL_ID is required. Set it in your environment or .env file"
    exit 1
fi

# Load .env file if it exists
if [ -f .env ]; then
    echo "📁 Loading environment variables from .env file"
    export $(cat .env | grep -v '^#' | xargs)
fi

echo "🔧 Starting deployment..."
echo ""

# Deploy Claude Agent Proxy
echo "📦 Deploying Claude Agent Proxy..."
gcloud run deploy claude-agent-proxy \
  --source ./services/claude-agent-proxy \
  --region=$REGION \
  --platform=managed \
  --allow-unauthenticated \
  --memory=1Gi \
  --cpu=1 \
  --min-instances=0 \
  --max-instances=100 \
  --concurrency=1000 \
  --timeout=90s \
  --set-env-vars=ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY" \
  --set-env-vars=CLAUDE_MODEL="claude-3-sonnet-20240229" \
  --set-env-vars=DOCS_ZIP_PATH="./docs.zip" \
  --set-env-vars=MAX_CONTEXT_CHUNKS="5" \
  --set-env-vars=CHUNK_SIZE="1000" \
  --quiet

CLAUDE_PROXY_URL=$(gcloud run services describe claude-agent-proxy --region=$REGION --format="value(status.url)")
echo "✅ Claude Proxy deployed: $CLAUDE_PROXY_URL"
echo ""

# Deploy Broadcast Bot
echo "📦 Deploying Broadcast Bot..."
gcloud run deploy broadcast-bot \
  --source ./services/broadcast-bot \
  --region=$REGION \
  --platform=managed \
  --allow-unauthenticated \
  --memory=512Mi \
  --cpu=1 \
  --min-instances=0 \
  --max-instances=50 \
  --concurrency=1000 \
  --set-env-vars=BROADCASTER_SLACK_BOT_TOKEN="$BROADCASTER_SLACK_BOT_TOKEN" \
  --set-env-vars=BROADCAST_CHANNEL_ID="$BROADCAST_CHANNEL_ID" \
  --quiet

BROADCAST_SERVICE_URL=$(gcloud run services describe broadcast-bot --region=$REGION --format="value(status.url)")
echo "✅ Broadcast Bot deployed: $BROADCAST_SERVICE_URL"
echo ""

# Deploy Slack Events Listener
echo "📦 Deploying Slack Events Listener..."
gcloud run deploy slack-events-listener \
  --source ./services/slack-events-listener \
  --region=$REGION \
  --platform=managed \
  --allow-unauthenticated \
  --memory=512Mi \
  --cpu=1 \
  --min-instances=1 \
  --max-instances=100 \
  --concurrency=1000 \
  --set-env-vars=WAVIE_SLACK_BOT_TOKEN="$WAVIE_SLACK_BOT_TOKEN" \
  --set-env-vars=WAVIE_SLACK_SIGNING_SECRET="$WAVIE_SLACK_SIGNING_SECRET" \
  --set-env-vars=CLAUDE_PROXY_URL="$CLAUDE_PROXY_URL" \
  --set-env-vars=BROADCAST_SERVICE_URL="$BROADCAST_SERVICE_URL" \
  --quiet

SLACK_EVENTS_URL=$(gcloud run services describe slack-events-listener --region=$REGION --format="value(status.url)")
echo "✅ Slack Events Listener deployed: $SLACK_EVENTS_URL"
echo ""

echo "🎉 All services deployed successfully!"
echo ""
echo "📋 Service URLs:"
echo "  Slack Events Listener: $SLACK_EVENTS_URL"
echo "  Claude Agent Proxy:    $CLAUDE_PROXY_URL"
echo "  Broadcast Bot:         $BROADCAST_SERVICE_URL"
echo ""
echo "🔧 Next Steps:"
echo "  1. Update your Slack app's Event Subscriptions URL to:"
echo "     $SLACK_EVENTS_URL/slack/events"
echo ""
echo "  2. Test the deployment:"
echo "     curl $SLACK_EVENTS_URL/health"
echo ""
echo "  3. Test in Slack:"
echo "     @wavie hello world"
echo ""
echo "✨ Wavie is ready to help your team!"