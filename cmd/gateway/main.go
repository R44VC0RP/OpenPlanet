package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/wish/v2"
	"charm.land/wish/v2/activeterm"
	wishtea "charm.land/wish/v2/bubbletea"
	"charm.land/wish/v2/logging"
	"github.com/charmbracelet/ssh"

	"gamegateway/internal/activity"
	"gamegateway/internal/chat"
	"gamegateway/internal/config"
	"gamegateway/internal/identity"
	"gamegateway/internal/store"
	"gamegateway/internal/ui"
)

func main() {
	cfg := config.Load()
	if len(os.Args) > 1 && os.Args[1] == "list-image-games" {
		listImageGames(cfg)
		return
	}
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
	sampleMaxPlayers := 1
	if cfg.GGPSessionSecret != "" {
		sampleMaxPlayers = 16
	}
	if err := db.SeedSampleGame(ctx, cfg.SampleGameURL, sampleMaxPlayers, cfg.GGPSessionSecret); err != nil {
		fatal("seed sample game", err)
	}
	if err := db.SeedBlobfieldGame(ctx, cfg.BlobfieldGameURL, sampleMaxPlayers, cfg.GGPSessionSecret); err != nil {
		fatal("seed blobfield game", err)
	}
	if err := db.SeedTetrisGame(ctx); err != nil {
		fatal("seed tetris game", err)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.HostKeyPath), 0o700); err != nil {
		fatal("create host key directory", err)
	}

	hub := chat.NewHub()
	activityTracker := activity.NewTracker()
	srv, err := wish.NewServer(
		wish.WithAddress(net.JoinHostPort(cfg.Host, cfg.Port)),
		wish.WithHostKeyPath(cfg.HostKeyPath),
		wish.WithPublicKeyAuth(func(_ ssh.Context, key ssh.PublicKey) bool {
			return key != nil
		}),
		wish.WithMiddleware(
			wishtea.Middleware(sessionHandler(db, hub, activityTracker, cfg)),
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

func listImageGames(cfg config.Config) {
	if cfg.DatabaseURL == "" {
		fatal("load configuration", errors.New("DATABASE_URL is required"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal("connect database", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		fatal("migrate database", err)
	}
	games, err := db.ListRunnableImageGames(ctx)
	if err != nil {
		fatal("list image games", err)
	}
	for _, game := range games {
		secret := game.SessionSecret
		if secret == "" {
			secret = "-"
		}
		fields := []string{game.ID, game.ImageRef, fmt.Sprintf("%d", game.ContainerPort), secret, game.Status}
		for i := range fields {
			fields[i] = strings.ReplaceAll(fields[i], "\t", " ")
		}
		fmt.Println(strings.Join(fields, "\t"))
	}
}

func sessionHandler(db *store.Store, hub *chat.Hub, activityTracker *activity.Tracker, cfg config.Config) wishtea.Handler {
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
			Player:           player,
			Games:            games,
			Store:            db,
			Hub:              hub,
			Activity:         activityTracker,
			Width:            pty.Window.Width,
			Height:           pty.Window.Height,
			GGPIssuer:        cfg.GGPIssuer,
			GGPSessionSecret: cfg.GGPSessionSecret,
		}), wishtea.MakeOptions(s)
	}
}

func fatal(action string, err error) {
	fmt.Fprintf(os.Stderr, "failed to %s: %v\n", action, err)
	os.Exit(1)
}
