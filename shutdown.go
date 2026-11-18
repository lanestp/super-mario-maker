package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/PretendoNetwork/super-mario-maker/globals"
)

// * Stops the server cleanly when asked to (systemd and docker send
// * SIGTERM, a terminal sends SIGINT), rather than being cut off. An
// * exit through os.Exit also lets a build made with -cover write how
// * much of the code ran, which the end to end tests read
func init() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		received := <-signals

		if globals.Logger != nil {
			globals.Logger.Infof("Stopping, %s received", received)
		}

		if globals.Store != nil {
			globals.Store.Close()
		}

		os.Exit(0)
	}()
}
