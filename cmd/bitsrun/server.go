package main

import (
	"context"
	"fmt"
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

	// The pid file has to name this process, so it is written after the
	// process exists and before anything can fail to start. A wrapper script
	// cannot do this: it records the pid of the shell or of the tool that
	// switched users, and that process is gone by the time anyone looks.
	if path := flags.ServerPIDFile; path != "" {
		if err := writePIDFile(path); err != nil {
			logger.Fatalln("cannot write pid file:", err)
		}
		defer os.Remove(path)
	}

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

// writePIDFile records the current process id so the lifetime scripts can tell
// whether the service is running.
func writePIDFile(path string) error {
	pid := fmt.Sprintf("%d\n", os.Getpid())
	return os.WriteFile(path, []byte(pid), 0o600)
}
