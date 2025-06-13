package config

type Config struct {
	LogLevel string `envconfig:"LOG_LEVEL" default:"info"`
	Port     int    `envconfig:"PORT" default:"8082"`

	SlackBotToken      string `envconfig:"SLACK_BOT_TOKEN" required:"true"`
	BroadcastChannelID string `envconfig:"BROADCAST_CHANNEL_ID" required:"true"`
}
