package main

import (
	"context"
	"github.com/example/avito-kitchen/internal/kitchen/client"
	"github.com/example/avito-kitchen/internal/kitchen/config"
	"github.com/example/avito-kitchen/internal/kitchen/handler"
	"github.com/example/avito-kitchen/internal/kitchen/repository"
	"github.com/example/avito-kitchen/internal/kitchen/service"
	"log/slog"
	"net/http"
	"os"
)

func main() {
	c := config.Load()
	l := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	r, e := repository.New(context.Background(), c.DatabaseURL)
	if e != nil {
		l.Error("database", "error", e)
		os.Exit(1)
	}
	defer r.Pool.Close()
	if e = r.Migrate(context.Background()); e != nil {
		l.Error("migration", "error", e)
		os.Exit(1)
	}
	a := handler.New(r, service.NewOrders(r, client.NewWebhook()), l)
	l.Info("started", "port", c.Port)
	if e = http.ListenAndServe(":"+c.Port, a.Router()); e != nil {
		l.Error("server", "error", e)
	}
}
