package globals

import (
	"context"
	"errors"

	"github.com/PretendoNetwork/super-mario-maker/store"

	pb "github.com/PretendoNetwork/grpc/go/account"
	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	"github.com/PretendoNetwork/nex-protocols-go/v2/globals"
	"google.golang.org/grpc/metadata"
)

func PasswordFromPID(pid types.PID) (string, uint32) {
	if LocalAccounts {
		account, err := Store.GetAccountByPID(uint32(pid))
		if err != nil {
			if !errors.Is(err, store.ErrAccountNotFound) {
				globals.Logger.Error(err.Error())
			}

			return "", nex.ResultCodes.RendezVous.InvalidUsername
		}

		return account.NEXPassword, 0
	}

	ctx := metadata.NewOutgoingContext(context.Background(), GRPCAccountCommonMetadata)

	response, err := GRPCAccountClient.GetNEXPassword(ctx, &pb.GetNEXPasswordRequest{Pid: uint32(pid)})
	if err != nil {
		globals.Logger.Error(err.Error())
		return "", nex.ResultCodes.RendezVous.InvalidUsername
	}

	return response.Password, 0
}
