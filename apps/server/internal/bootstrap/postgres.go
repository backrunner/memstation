package bootstrap

import (
	"context"
	"database/sql"

	"github.com/XSAM/otelsql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"go.opentelemetry.io/otel"
)

func OpenPostgres(ctx context.Context, cfg Config) (*sql.DB, error) {
	if cfg.DatabaseURL == "" {
		return nil, nil
	}
	db, err := otelsql.Open(
		"pgx",
		cfg.DatabaseURL,
		otelsql.WithTracerProvider(otel.GetTracerProvider()),
		otelsql.WithMeterProvider(otel.GetMeterProvider()),
	)
	if err != nil {
		return nil, err
	}
	_, _ = otelsql.RegisterDBStatsMetrics(
		db,
		otelsql.WithMeterProvider(otel.GetMeterProvider()),
	)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
