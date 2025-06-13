# Wavie Claude Bot

A scalable Slack bot powered by Anthropic's Claude AI with Bitwave documentation knowledge base.

## 🏗️ Architecture

- **Slack Events Listener**: Handles @wavie mentions and Slack webhook verification
- **Claude Agent Proxy**: Claude API integration with Bitwave docs knowledge base
- **Broadcast Bot**: Posts interaction logs to monitoring channels

## 🚀 Quick Deploy

1. Set your environment variables
2. Run: `./deploy.sh`
3. Update Slack webhook URL
4. Test with `@wavie hello`

## 📋 Required Environment Variables

```bash
# Get from Anthropic Console
ANTHROPIC_API_KEY=sk-ant-your-key-here

# Your monitoring channel ID
BROADCAST_CHANNEL_ID=C1234567890
```

## 🧪 Testing

```bash
# Health checks
curl https://slack-events-listener-xxxx.run.app/health
curl https://claude-agent-proxy-xxxx.run.app/health
curl https://broadcast-bot-xxxx.run.app/health

# Slack test
@wavie hello world
```