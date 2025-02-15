package keeper

import (
	"bytes"
	context "context"
	fmt "fmt"
	"strings"

	errorsmod "cosmossdk.io/errors"

	"cosmossdk.io/x/circuit/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
)

type msgServer struct {
	Keeper
}

var _ types.MsgServer = msgServer{}

// NewMsgServerImpl returns an implementation of the circuit MsgServer interface
// for the provided Keeper.
func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

func (srv msgServer) AuthorizeCircuitBreaker(goCtx context.Context, msg *types.MsgAuthorizeCircuitBreaker) (*types.MsgAuthorizeCircuitBreakerResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	address, err := srv.addressCodec.StringToBytes(msg.Granter)
	if err != nil {
		return nil, err
	}

	// if the granter is the module authority no need to check perms
	if !bytes.Equal(address, srv.GetAuthority()) {
		// Check that the authorizer has the permission level of "super admin"
		perms, err := srv.GetPermissions(ctx, address)
		if err != nil {
			return nil, fmt.Errorf("user permission does not exist %w", err)
		}

		if perms.Level != types.Permissions_LEVEL_SUPER_ADMIN {
			return nil, errorsmod.Wrap(sdkerrors.ErrUnauthorized, "only super admins can authorize users")
		}
	}

	grantee, err := srv.addressCodec.StringToBytes(msg.Grantee)
	if err != nil {
		return nil, err
	}

	// Append the account in the msg to the store's set of authorized super admins
	if err = srv.SetPermissions(ctx, grantee, msg.Permissions); err != nil {
		return nil, err
	}

	ctx.EventManager().EmitEvents(sdk.Events{
		sdk.NewEvent(
			"authorize_circuit_breaker",
			sdk.NewAttribute("granter", msg.Granter),
			sdk.NewAttribute("grantee", msg.Grantee),
			sdk.NewAttribute("permission", msg.Permissions.String()),
		),
	})

	return &types.MsgAuthorizeCircuitBreakerResponse{
		Success: true,
	}, nil
}

func (srv msgServer) TripCircuitBreaker(goCtx context.Context, msg *types.MsgTripCircuitBreaker) (*types.MsgTripCircuitBreakerResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	address, err := srv.addressCodec.StringToBytes(msg.Authority)
	if err != nil {
		return nil, err
	}

	// Check that the account has the permissions
	perms, err := srv.GetPermissions(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("user permission does not exist %w", err)
	}

	store := ctx.KVStore(srv.storekey)

	switch {
	case perms.Level == types.Permissions_LEVEL_SUPER_ADMIN || perms.Level == types.Permissions_LEVEL_ALL_MSGS || bytes.Equal(address, srv.GetAuthority()):
		for _, msgTypeURL := range msg.MsgTypeUrls {
			// check if the message is in the list of allowed messages
			if !srv.IsAllowed(ctx, msgTypeURL) {
				return nil, fmt.Errorf("message %s is already disabled", msgTypeURL)
			}
			store.Set(types.CreateDisableMsgPrefix(msgTypeURL), []byte{0x01})
		}
	case perms.Level == types.Permissions_LEVEL_SOME_MSGS:
		for _, msgTypeURL := range msg.MsgTypeUrls {
			// check if the message is in the list of allowed messages
			if !srv.IsAllowed(ctx, msgTypeURL) {
				return nil, fmt.Errorf("message %s is already disabled", msgTypeURL)
			}
			for _, msgurl := range perms.LimitTypeUrls {
				if msgTypeURL == msgurl {
					store.Set(types.CreateDisableMsgPrefix(msgTypeURL), []byte{0x01})
				} else {
					return nil, errorsmod.Wrapf(sdkerrors.ErrUnauthorized, "account does not have permission to trip circuit breaker for message %s", msgTypeURL)
				}
			}
		}
	default:
		return nil, errorsmod.Wrap(sdkerrors.ErrUnauthorized, "account does not have permission to trip circuit breaker")
	}

	var urls string
	if len(msg.GetMsgTypeUrls()) > 1 {
		urls = strings.Join(msg.GetMsgTypeUrls(), ",")
	}

	ctx.EventManager().EmitEvents(sdk.Events{
		sdk.NewEvent(
			"trip_circuit_breaker",
			sdk.NewAttribute("authority", msg.Authority),
			sdk.NewAttribute("msg_url", urls),
		),
	})

	return &types.MsgTripCircuitBreakerResponse{
		Success: true,
	}, nil
}

// ResetCircuitBreaker resumes processing of Msg's in the state machine that
// have been been paused using TripCircuitBreaker.
func (srv msgServer) ResetCircuitBreaker(goCtx context.Context, msg *types.MsgResetCircuitBreaker) (*types.MsgResetCircuitBreakerResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	keeper := srv.Keeper

	address, err := srv.addressCodec.StringToBytes(msg.Authority)
	if err != nil {
		return nil, err
	}

	// Get the permissions for the account specified in the msg.Authority field
	perms, err := keeper.GetPermissions(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("user permission does not exist %w", err)
	}

	store := ctx.KVStore(srv.storekey)

	if perms.Level == types.Permissions_LEVEL_SUPER_ADMIN || perms.Level == types.Permissions_LEVEL_ALL_MSGS || perms.Level == types.Permissions_LEVEL_SOME_MSGS || bytes.Equal(address, srv.GetAuthority()) {
		// add all msg type urls to the disable list
		for _, msgTypeURL := range msg.MsgTypeUrls {
			if srv.IsAllowed(ctx, msgTypeURL) {
				return nil, fmt.Errorf("message %s is not disabled", msgTypeURL)
			}
			store.Delete(types.CreateDisableMsgPrefix(msgTypeURL))
		}
	} else {
		return nil, errorsmod.Wrap(sdkerrors.ErrUnauthorized, "account does not have permission to reset circuit breaker")
	}

	var urls string
	if len(msg.GetMsgTypeUrls()) > 1 {
		urls = strings.Join(msg.GetMsgTypeUrls(), ",")
	}

	ctx.EventManager().EmitEvents(sdk.Events{
		sdk.NewEvent(
			"reset_circuit_breaker",
			sdk.NewAttribute("authority", msg.Authority),
			sdk.NewAttribute("msg_url", urls),
		),
	})

	return &types.MsgResetCircuitBreakerResponse{Success: true}, nil
}
