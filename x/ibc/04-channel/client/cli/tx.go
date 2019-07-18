package cli

import (
	"io/ioutil"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/cosmos/cosmos-sdk/client/context"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/store/state"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/auth/client/utils"
	"github.com/cosmos/cosmos-sdk/x/ibc"
	"github.com/cosmos/cosmos-sdk/x/ibc/02-client"
	"github.com/cosmos/cosmos-sdk/x/ibc/03-connection"
	"github.com/cosmos/cosmos-sdk/x/ibc/04-channel"
	"github.com/cosmos/cosmos-sdk/x/ibc/23-commitment"
	"github.com/cosmos/cosmos-sdk/x/ibc/23-commitment/merkle"
)

/*
func GetTxCmd(storeKey string, cdc *codec.Codec) *cobra.Command {

}
*/
const (
	FlagNode1 = "node1"
	FlagNode2 = "node2"
	FlagFrom1 = "from1"
	FlagFrom2 = "from2"
)

func handshake(ctx context.CLIContext, cdc *codec.Codec, storeKey string, version int64, connid, chanid string) channel.CLIHandshakeObject {
	prefix := []byte(strconv.FormatInt(version, 10) + "/")
	path := merkle.NewPath([][]byte{[]byte(storeKey)}, prefix)
	base := state.NewBase(cdc, sdk.NewKVStoreKey(storeKey), prefix)
	climan := client.NewManager(base)
	connman := connection.NewManager(base, climan)
	man := channel.NewHandshaker(channel.NewManager(base, connman))
	return man.CLIQuery(ctx, path, connid, chanid)
}

func lastheight(ctx context.CLIContext) (uint64, error) {
	node, err := ctx.GetNode()
	if err != nil {
		return 0, err
	}

	info, err := node.ABCIInfo()
	if err != nil {
		return 0, err
	}

	return uint64(info.Response.LastBlockHeight), nil
}

func GetCmdChannelHandshake(storeKey string, cdc *codec.Codec) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "handshake",
		Short: "initiate channel handshake between two chains",
		Args:  cobra.ExactArgs(4),
		// Args: []string{connid1, chanid1, chanfilepath1, connid2, chanid2, chanfilepath2}
		RunE: func(cmd *cobra.Command, args []string) error {
			txBldr := auth.NewTxBuilderFromCLI().WithTxEncoder(utils.GetTxEncoder(cdc))
			ctx1 := context.NewCLIContext().
				WithCodec(cdc).
				WithNodeURI(viper.GetString(FlagNode1)).
				WithFrom(viper.GetString(FlagFrom1))

			ctx2 := context.NewCLIContext().
				WithCodec(cdc).
				WithNodeURI(viper.GetString(FlagNode2)).
				WithFrom(viper.GetString(FlagFrom2))

			conn1id := args[0]
			chan1id := args[1]
			conn1bz, err := ioutil.ReadFile(args[2])
			if err != nil {
				return err
			}
			var conn1 channel.Channel
			if err := cdc.UnmarshalJSON(conn1bz, &conn1); err != nil {
				return err
			}

			obj1 := handshake(ctx1, cdc, storeKey, ibc.Version, conn1id, chan1id)

			conn2id := args[3]
			chan2id := args[4]
			conn2bz, err := ioutil.ReadFile(args[5])
			if err != nil {
				return err
			}
			var conn2 channel.Channel
			if err := cdc.UnmarshalJSON(conn2bz, &conn2); err != nil {
				return err
			}

			obj2 := handshake(ctx2, cdc, storeKey, ibc.Version, conn1id, chan1id)

			// TODO: check state and if not Idle continue existing process
			height, err := lastheight(ctx2)
			if err != nil {
				return err
			}
			nextTimeout := height + 1000 // TODO: parameterize
			msginit := channel.MsgOpenInit{
				ConnectionID: conn1id,
				ChannelID:    chan1id,
				Channel:      conn1,
				NextTimeout:  nextTimeout,
				Signer:       ctx1.GetFromAddress(),
			}

			err = utils.GenerateOrBroadcastMsgs(ctx1, txBldr, []sdk.Msg{msginit})
			if err != nil {
				return err
			}

			timeout := nextTimeout
			height, err = lastheight(ctx1)
			if err != nil {
				return err
			}
			nextTimeout = height + 1000
			_, pconn, err := obj1.Channel(ctx1)
			if err != nil {
				return err
			}
			_, pstate, err := obj1.State(ctx1)
			if err != nil {
				return err
			}
			_, ptimeout, err := obj1.NextTimeout(ctx1)
			if err != nil {
				return err
			}

			msgtry := channel.MsgOpenTry{
				ConnectionID: conn2id,
				ChannelID:    chan2id,
				Channel:      conn2,
				Timeout:      timeout,
				NextTimeout:  nextTimeout,
				Proofs:       []commitment.Proof{pconn, pstate, ptimeout},
				Signer:       ctx2.GetFromAddress(),
			}

			err = utils.GenerateOrBroadcastMsgs(ctx2, txBldr, []sdk.Msg{msgtry})
			if err != nil {
				return err
			}

			timeout = nextTimeout
			height, err = lastheight(ctx2)
			if err != nil {
				return err
			}
			nextTimeout = height + 1000
			_, pconn, err = obj2.Channel(ctx2)
			if err != nil {
				return err
			}
			_, pstate, err = obj2.State(ctx2)
			if err != nil {
				return err
			}
			_, ptimeout, err = obj2.NextTimeout(ctx2)
			if err != nil {
				return err
			}

			msgack := channel.MsgOpenAck{
				ConnectionID: conn1id,
				ChannelID:    chan1id,
				Timeout:      timeout,
				Proofs:       []commitment.Proof{pconn, pstate, ptimeout},
				Signer:       ctx1.GetFromAddress(),
			}

			err = utils.GenerateOrBroadcastMsgs(ctx1, txBldr, []sdk.Msg{msgack})
			if err != nil {
				return err
			}

			timeout = nextTimeout
			_, pstate, err = obj1.State(ctx1)
			if err != nil {
				return err
			}
			_, ptimeout, err = obj1.NextTimeout(ctx1)
			if err != nil {
				return err
			}

			msgconfirm := channel.MsgOpenConfirm{
				ConnectionID: conn2id,
				ChannelID:    chan2id,
				Timeout:      timeout,
				Proofs:       []commitment.Proof{pstate, ptimeout},
				Signer:       ctx2.GetFromAddress(),
			}

			err = utils.GenerateOrBroadcastMsgs(ctx2, txBldr, []sdk.Msg{msgconfirm})
			if err != nil {
				return err
			}

			return nil
		},
	}

	return cmd
}
