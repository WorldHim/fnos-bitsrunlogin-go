package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Mmx233/BitSrunLoginGo/internal/config"
	"github.com/Mmx233/BitSrunLoginGo/internal/config/flags"
	"github.com/Mmx233/BitSrunLoginGo/internal/config/keys"
	"github.com/Mmx233/BitSrunLoginGo/internal/server"
)

// runServer starts the control server and blocks until the process receives
// SIGINT or SIGTERM.
//
// The package lifetime scripts stop the app with a signal, so an orderly
// shutdown here is what stops the login loop and closes the listener; exiting
// on a bare os.Exit would leave the loop's goroutine mid-request.
func runServer() {
	logger := config.Logger

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := server.New(server.Options{
		Addr:       flags.ServerAddr,
		ConfigPath: flags.Path,
		Logger:     logger.WithField(keys.LogComponent, "server"),
		AutoStart:  true,
	})
	if err != nil {
		logger.Fatalln("failed to start control server:", err)
	}

	if err := srv.ListenAndServe(ctx); err != nil {
		logger.Fatalln("control server stopped:", err)
	}
	logger.Infoln("control server stopped")
}
