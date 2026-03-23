package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"memstation/apps/server/internal/app"
)

func main() {
	rootCmd := newRootCommand()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "memstation: %v\n", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "memstation",
		Short: "MemStation server runtime",
	}

	rootCmd.AddCommand(newServeCommand())
	rootCmd.AddCommand(newWorkerCommand())
	rootCmd.AddCommand(newSchedulerCommand())
	rootCmd.AddCommand(newMigrateCommand())
	rootCmd.AddCommand(newSeedCommand())

	return rootCmd
}

func newServeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run HTTP gateway, GraphQL, memory and admin APIs",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return app.RunServe(ctx)
		},
	}
}

func newWorkerCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "worker",
		Short: "Run asynq worker consumers",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return app.RunWorker(ctx)
		},
	}
}

func newSchedulerCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "scheduler",
		Short: "Run scheduler and outbox relay",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return app.RunScheduler(ctx)
		},
	}
}

func newMigrateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Run versioned SQL migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return app.RunMigrate(ctx)
		},
	}
}

func newSeedCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "seed",
		Short: "Seed default admin and model capability registry metadata",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return app.RunSeed(ctx)
		},
	}
}
