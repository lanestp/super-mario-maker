package main

import (
	"os"
	"sync"

	"github.com/PretendoNetwork/super-mario-maker/globals"
	"github.com/PretendoNetwork/super-mario-maker/grpc"
	"github.com/PretendoNetwork/super-mario-maker/nex"
)

var wg sync.WaitGroup

func main() {
	wg.Add(2)

	go grpc.StartGRPCServer()
	go nex.StartAuthenticationServer()
	go nex.StartSecureServer()

	if globals.AccountAPI != nil {
		go func() {
			globals.Logger.Successf("Serving the account API on port %d", globals.AccountAPI.Port)

			if err := globals.AccountAPI.ListenAndServe(); err != nil {
				globals.Logger.Criticalf("The account API stopped: %v", err)
				os.Exit(1)
			}
		}()
	}

	wg.Wait()
}
