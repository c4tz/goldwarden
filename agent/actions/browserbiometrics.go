package actions

import (
	"encoding/base64"
	"fmt"

	"github.com/quexten/goldwarden/agent/config"
	"github.com/quexten/goldwarden/agent/sockets"
	"github.com/quexten/goldwarden/agent/systemauth"
	"github.com/quexten/goldwarden/agent/systemauth/biometrics"
	"github.com/quexten/goldwarden/agent/systemauth/pinentry"
	"github.com/quexten/goldwarden/agent/vault"

	"github.com/quexten/goldwarden/ipc/messages"
)

func handleGetBiometricsKey(request messages.IPCMessage, cfg *config.Config, vault *vault.Vault, ctx *sockets.CallingContext) (response messages.IPCMessage, err error) {
	if !(systemauth.VerifyPinSession(*ctx) || biometrics.CheckBiometrics(biometrics.BrowserBiometrics)) {
		response, err = messages.IPCMessageFromPayload(messages.ActionResponse{
			Success: false,
			Message: "not approved",
		})
		if err != nil {
			return messages.IPCMessage{}, err
		}
		return response, nil
	}

	if approved, err := pinentry.GetApproval("Approve Credential Access", fmt.Sprintf("%s on %s>%s>%s is trying to access your vault encryption key for browser biometric unlock.", ctx.UserName, ctx.GrandParentProcessName, ctx.ParentProcessName, ctx.ProcessName)); err != nil || !approved {
		response, err = messages.IPCMessageFromPayload(messages.ActionResponse{
			Success: false,
			Message: "not approved",
		})
		if err != nil {
			return messages.IPCMessage{}, err
		}
		return response, nil
	}

	masterKey, err := cfg.GetMasterKey()
	if err != nil {
		return messages.IPCMessage{}, err
	}
	masterKeyB64 := base64.StdEncoding.EncodeToString(masterKey)
	response, err = messages.IPCMessageFromPayload(messages.GetBiometricsKeyResponse{
		Key: masterKeyB64,
	})
	return response, err
}

func init() {
	AgentActionsRegistry.Register(messages.MessageTypeForEmptyPayload(messages.GetBiometricsKeyRequest{}), ensureIsNotLocked(ensureIsLoggedIn(handleGetBiometricsKey)))
}
