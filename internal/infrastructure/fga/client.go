// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fga

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	openfga "github.com/openfga/go-sdk"
	"github.com/openfga/go-sdk/client"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// httpTimeout bounds every OpenFGA request. The SDK sets no timeout on an
// HTTP client it is given, so without this a hung OpenFGA call never returns.
const httpTimeout = 8 * time.Second

// ErrStoreNotResolved means the OpenFGA store has not been found yet. It is
// transient: the lookup keeps retrying in the background.
var ErrStoreNotResolved = errors.New("openfga store is not resolved yet")

var errStoreNotFound = errors.New("no openfga store has this name")

// fgaClient is the part of the OpenFGA API the tuple store uses.
type fgaClient interface {
	Read(ctx context.Context, req client.ClientReadRequest, options client.ClientReadOptions) (*client.ClientReadResponse, error)
	Write(ctx context.Context, req client.ClientWriteRequest, options client.ClientWriteOptions) (*client.ClientWriteResponse, error)
}

// sdkClient adds the store id to every request, because the id is only known
// once the background lookup has found the store.
type sdkClient struct {
	sdk     *client.OpenFgaClient
	storeID atomic.Pointer[string]
}

func newSDKClient(apiURL string) (*sdkClient, error) {
	sdk, err := client.NewSdkClient(&client.ClientConfiguration{ApiUrl: apiURL, HTTPClient: newHTTPClient()})
	if err != nil {
		return nil, err
	}
	return &sdkClient{sdk: sdk}, nil
}

func newHTTPClient() *http.Client {
	return &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport), Timeout: httpTimeout}
}

func (c *sdkClient) currentStoreID() (*string, error) {
	id := c.storeID.Load()
	if id == nil {
		return nil, ErrStoreNotResolved
	}
	return id, nil
}

func (c *sdkClient) Read(
	ctx context.Context,
	req client.ClientReadRequest,
	options client.ClientReadOptions,
) (*client.ClientReadResponse, error) {
	id, err := c.currentStoreID()
	if err != nil {
		return nil, err
	}
	options.StoreId = id
	return c.sdk.Read(ctx).Body(req).Options(options).Execute()
}

func (c *sdkClient) Write(
	ctx context.Context,
	req client.ClientWriteRequest,
	options client.ClientWriteOptions,
) (*client.ClientWriteResponse, error) {
	id, err := c.currentStoreID()
	if err != nil {
		return nil, err
	}
	options.StoreId = id
	return c.sdk.Write(ctx).Body(req).Options(options).Execute()
}

// findStore returns the id of the one store named name. Several stores with
// the same name are an error rather than a guess, since writing tuples to the
// wrong store silently breaks access.
func (c *sdkClient) findStore(ctx context.Context, name string) (string, error) {
	var ids []string
	options := client.ClientListStoresOptions{Name: openfga.PtrString(name)}
	for {
		resp, err := c.sdk.ListStores(ctx).Options(options).Execute()
		if err != nil {
			return "", err
		}
		for _, store := range resp.Stores {
			if store.Name == name {
				ids = append(ids, store.Id)
			}
		}
		if resp.ContinuationToken == "" {
			break
		}
		options.ContinuationToken = openfga.PtrString(resp.ContinuationToken)
	}
	switch len(ids) {
	case 0:
		return "", errStoreNotFound
	case 1:
		return ids[0], nil
	default:
		return "", fmt.Errorf("%d openfga stores have this name", len(ids))
	}
}
