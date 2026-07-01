package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"meta-business-mcp/pkg/config"
)

type DB struct {
	Pool  *pgxpool.Pool
	Redis *redis.Client
}

func Connect(ctx context.Context, cfg *config.Config) (*DB, error) {
	// 1. Connect to PostgreSQL
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.Database.User,
		cfg.Database.Password,
		cfg.Database.Host,
		cfg.Database.Port,
		cfg.Database.DBName,
		cfg.Database.SSLMode,
	)

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pgx pool config: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres: %w", err)
	}

	// Ping postgres to ensure connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	// 2. Connect to Redis
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	// Ping redis to ensure connection
	if err := rdb.Ping(ctx).Err(); err != nil {
		pool.Close()
		rdb.Close()
		return nil, fmt.Errorf("failed to ping redis: %w", err)
	}

	return &DB{
		Pool:  pool,
		Redis: rdb,
	}, nil
}

func (db *DB) Close() {
	if db.Pool != nil {
		db.Pool.Close()
	}
	if db.Redis != nil {
		_ = db.Redis.Close()
	}
}
