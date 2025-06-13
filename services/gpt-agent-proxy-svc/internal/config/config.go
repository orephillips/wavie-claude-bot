package config

type Config struct {
	LogLevel string `envconfig:"LOG_LEVEL" default:"info"`
	Port     int    `envconfig:"PORT" default:"8081"`

	OpenAIAPIKey string `envconfig:"OPENAI_API_KEY" required:"true"`
	OpenAIModel  string `envconfig:"OPENAI_MODEL" default:"gpt-4"`
}
