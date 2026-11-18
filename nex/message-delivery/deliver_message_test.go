package nex_message_delivery

import (
	"errors"
	"testing"

	nex "github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	message_delivery "github.com/PretendoNetwork/nex-protocols-go/v2/message-delivery"
	"github.com/PretendoNetwork/plogger-go"
	"github.com/PretendoNetwork/super-mario-maker/globals"
)

func TestAMessageThatCouldNotBeReadIsAnsweredWithAnError(t *testing.T) {
	globals.Logger = plogger.NewLogger()

	response, nexError := DeliverMessage(errors.New("the message could not be read"), nil, 7, types.NewDataHolder())

	if response != nil || nexError == nil {
		t.Fatalf("got %v and %v, want only an error", response, nexError)
	}

	want := nex.NewError(nex.ResultCodes.DataStore.Unknown, "")
	if nexError.ResultCode != want.ResultCode || nexError.Message != "the message could not be read" {
		t.Errorf("got %v, want DataStore Unknown with the reason", nexError)
	}
}

// * What the message says is not looked at. The game is told it was delivered
func TestADeliveredMessageIsAnsweredWithSuccess(t *testing.T) {
	response, nexError := DeliverMessage(nil, nil, 7, types.NewDataHolder())
	if nexError != nil {
		t.Fatal(nexError)
	}

	if !response.IsSuccess || response.IsRequest || response.CallID != 7 {
		t.Errorf("got success %v, request %v, call %d; want a successful answer to call 7", response.IsSuccess, response.IsRequest, response.CallID)
	}

	if response.ProtocolID != message_delivery.ProtocolID || response.MethodID != message_delivery.MethodDeliverMessage {
		t.Errorf("answered protocol %d method %d, want DeliverMessage", response.ProtocolID, response.MethodID)
	}

	if len(response.Parameters) != 0 {
		t.Errorf("answered with %x, want nothing", response.Parameters)
	}
}
