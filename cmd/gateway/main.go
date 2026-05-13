package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/wish/v2"
	"charm.land/wish/v2/activeterm"
	wishtea "charm.land/wish/v2/bubbletea"
	"charm.land/wish/v2/logging"
	"github.com/charmbracelet/ssh"

	"gamegateway/internal/chat"
	"gamegateway/internal/config"
	"gamegateway/internal/identity"
	"gamegateway/internal/store"
	"gamegateway/internal/ui"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()
	if cfg.DatabaseURL == "" {
		fatal("load configuration", errors.New("DATABASE_URL is required; set it in .env or the process environment"))
	}

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal("connect database", err)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		fatal("migrate database", err)
	}
	if err := db.SeedSampleGame(ctx, cfg.SampleGameURL); err != nil {
		fatal("seed sample game", err)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.HostKeyPath), 0o700); err != nil {
		fatal("create host key directory", err)
	}

	hub := chat.NewHub()
	srv, err := wish.NewServer(
		wish.WithAddress(net.JoinHostPort(cfg.Host, cfg.Port)),
		wish.WithHostKeyPath(cfg.HostKeyPath),
		wish.WithPublicKeyAuth(func(_ ssh.Context, key ssh.PublicKey) bool {
			return key != nil
		}),
		wish.WithMiddleware(
			wishtea.Middleware(sessionHandler(db, hub)),
			activeterm.Middleware(),
			logging.Middleware(),
		),
	)
	if err != nil {
		fatal("create ssh server", err)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	fmt.Printf("gamegateway listening on ssh://%s:%s\n", cfg.Host, cfg.Port)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			fatal("serve ssh", err)
		}
	}()

	<-done
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
		fatal("shutdown ssh", err)
	}
}

func sessionHandler(db *store.Store, hub *chat.Hub) wishtea.Handler {
	return func(s ssh.Session) (tea.Model, []tea.ProgramOption) {
		pty, _, _ := s.Pty()
		keyInfo, err := identity.FromSession(s)
		if err != nil {
			return ui.NewErrorModel("SSH key identity failed", err), wishtea.MakeOptions(s)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		player, err := db.EnsurePlayer(ctx, keyInfo)
		if err != nil {
			return ui.NewErrorModel("Player lookup failed", err), wishtea.MakeOptions(s)
		}

		games, err := db.ListGames(ctx)
		if err != nil {
			return ui.NewErrorModel("Game registry lookup failed", err), wishtea.MakeOptions(s)
		}

		return ui.NewModel(ui.ModelConfig{
			Player: player,
			Games:  games,
			Store:  db,
			Hub:    hub,
			Width:  pty.Window.Width,
			Height: pty.Window.Height,
		}), wishtea.MakeOptions(s)
	}
}

func fatal(action string, err error) {
	fmt.Fprintf(os.Stderr, "failed to %s: %v\n", action, err)
	os.Exit(1)
}
