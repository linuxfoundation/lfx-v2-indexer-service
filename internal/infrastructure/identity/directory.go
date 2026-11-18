// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package identity implements auth-service lookups for retained audit identity.
package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

type requester interface {
	RequestWithContext(context.Context, string, []byte) (*natsgo.Msg, error)
}

// Directory reads primary email by principal without logging directory data.
// The owning contract is lfx-v2-auth-service/docs/subjects/user_emails.md.
type Directory struct {
	request requester
	timeout time.Duration
}

// NewDirectory uses the service's existing NATS connection and bounds each
// lookup independently of the delivery deadline. No connection is opened here.
func NewDirectory(nc *natsgo.Conn, timeout time.Duration) (*Directory, error) {
	if nc == nil || timeout <= 0 {
		return nil, fmt.Errorf("identity directory requires NATS and a positive timeout")
	}
	return &Directory{request: nc, timeout: timeout}, nil
}

// LookupIdentity returns absence only for the owner's explicit user-not-found
// reply. All other failures are transient; response/driver text is never echoed.
func (d *Directory) LookupIdentity(ctx context.Context, principal string) (contracts.IdentityLookup, error) {
	if !types.ValidAuditActorRef("user:" + principal) {
		return contracts.IdentityLookup{}, fmt.Errorf("invalid identity principal")
	}
	request := struct {
		User struct {
			AuthToken string `json:"auth_token"`
		} `json:"user"`
	}{}
	request.User.AuthToken = principal
	payload, err := json.Marshal(request)
	if err != nil {
		return contracts.IdentityLookup{}, &contracts.IdentityLookupError{Cause: err}
	}
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	message, err := d.request.RequestWithContext(ctx, constants.AuthUserEmailsRead, payload)
	if err != nil {
		return contracts.IdentityLookup{}, &contracts.IdentityLookupError{Cause: err}
	}
	if message == nil || len(message.Data) > 1<<20 {
		return contracts.IdentityLookup{}, invalidReply()
	}
	var reply struct {
		Success *bool  `json:"success"`
		Error   string `json:"error"`
		Data    *struct {
			PrimaryEmail *string `json:"primary_email"`
		} `json:"data"`
	}
	if json.Unmarshal(message.Data, &reply) != nil || reply.Success == nil {
		return contracts.IdentityLookup{}, invalidReply()
	}
	if !*reply.Success {
		if reply.Error == "user not found" && reply.Data == nil {
			return contracts.IdentityLookup{Found: false}, nil
		}
		return contracts.IdentityLookup{}, invalidReply()
	}
	if reply.Data == nil || reply.Error != "" {
		return contracts.IdentityLookup{}, invalidReply()
	}
	email := reply.Data.PrimaryEmail
	if email != nil && *email == "" {
		email = nil
	}
	return contracts.IdentityLookup{Found: true, Email: email}, nil
}

func invalidReply() error {
	return &contracts.IdentityLookupError{Cause: fmt.Errorf("invalid identity directory reply")}
}
