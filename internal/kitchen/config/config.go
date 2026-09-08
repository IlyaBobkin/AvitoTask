package config

import "os"

type Config struct{ DatabaseURL, Port string }

func Load() Config {
	p := os.Getenv("PORT")
	if p == "" {
		p = "8080"
	}
	return Config{DatabaseURL: os.Getenv("DATABASE_URL"), Port: p}
}
