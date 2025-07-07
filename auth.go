package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	comatproto "github.com/bluesky-social/indigo/api/atproto"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/xrpc"
)

var sessPath string = "auth-session.json"

type AuthSession struct {
	DID          syntax.DID `json:"did"`
	Password     string     `json:"password"`
	RefreshToken string     `json:"session_token"`
	PDS          string     `json:"pds"`
}

func loadAuthSession(ctx context.Context, username *syntax.AtIdentifier, password string) (*xrpc.Client, error) {
	var sess *AuthSession
	fBytes, err := os.ReadFile(sessPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("Auth session file does not exist, creating session")
			sess, err = refreshAuthSession(ctx, *username, password, "", "")
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	err = json.Unmarshal(fBytes, &sess)
	if err != nil {
		return nil, err
	}

	if did, err := username.AsDID(); sess.DID != did {
		if err != nil {
			return nil, fmt.Errorf("failed to parse username as DID: %w", err)
		}
		fmt.Println("Stored session identity does not match identity provided, recreating session")
		sess, err = refreshAuthSession(ctx, *username, password, "", "")
		if err != nil {
			return nil, err
		}
	}

	client := xrpc.Client{
		Client:    &http.Client{},
		Host:      "",
		UserAgent: userAgent(),
		Auth: &xrpc.AuthInfo{
			Did:        sess.DID.String(),
			AccessJwt:  sess.RefreshToken,
			RefreshJwt: sess.RefreshToken,
		},
	}

	resp, err := comatproto.ServerRefreshSession(ctx, &client) // only fails if refresh jwt is expired, very rare
	if err != nil {
		as, err := refreshAuthSession(ctx, *username, password, "", "")
		if err != nil {
			return nil, err
		}
		client.Host = as.PDS
		client.Auth.AccessJwt = as.RefreshToken
		client.Auth.RefreshJwt = as.RefreshToken
		resp, err = comatproto.ServerRefreshSession(ctx, &client)
		if err != nil {
			return nil, err
		}
	}
	client.Auth.AccessJwt = resp.AccessJwt
	client.Auth.RefreshJwt = resp.RefreshJwt

	return &client, nil
}

func refreshAuthSession(ctx context.Context, username syntax.AtIdentifier, password, pdsURL, authFactorToken string) (*AuthSession, error) {
	var did syntax.DID
	// get pds url if not already
	if pdsURL == "" {
		// pds not provided
		dir := identity.DefaultDirectory()
		ident, err := dir.Lookup(ctx, username)
		if err != nil {
			return nil, err
		}

		pdsURL = ident.PDSEndpoint()
		if pdsURL == "" {
			return nil, fmt.Errorf("empty PDS URL")
		}
		did = ident.DID
	}

	// make sure that did has a did
	if did == "" && username.IsDID() {
		d, err := username.AsDID()
		if err != nil {
			return nil, fmt.Errorf("failed to parse username as DID: %w", err)
		}
		did = d
	}

	// unauthenticated client
	client := xrpc.Client{
		Host:      pdsURL,
		UserAgent: userAgent(),
	}
	var token *string
	if authFactorToken != "" {
		token = &authFactorToken
	}
	// actually create the session
	sess, err := comatproto.ServerCreateSession(ctx, &client, &comatproto.ServerCreateSession_Input{
		Identifier:      username.String(),
		Password:        password,
		AuthFactorToken: token,
	})
	if err != nil {
		return nil, err
	}

	if did == "" {
		did, err = syntax.ParseDID(sess.Did)
		if err != nil {
			return nil, err
		}
	} else if sess.Did != did.String() {
		return nil, fmt.Errorf("session DID didn't match expected: %s != %s", sess.Did, did)
	}

	authSession := AuthSession{
		DID:          did,
		Password:     password,
		PDS:          pdsURL,
		RefreshToken: sess.RefreshJwt,
	}

	writeAuthSession(&authSession)

	return &authSession, nil
}

func writeAuthSession(sess *AuthSession) error {
	f, err := os.OpenFile(sessPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	authBytes, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}

	f.Write(authBytes)
	return nil
}

func userAgent() *string {
	str := fmt.Sprintf("Bluesky MCP Server v%s", Version)
	return &str
}
