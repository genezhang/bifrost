// Command plugin builds the session-affinity plugin as a Go native shared object
// (.so) loadable by a prebuilt bifrost-http binary via the config.json `plugins[].path`
// mechanism.
//
// The .so loader (framework/plugins/soloader.go) looks up PACKAGE-LEVEL functions by
// name (GetName, Cleanup, optional Init, HTTPTransport*Hook) — not methods on a struct.
// This file is that thin shim: it delegates to the reusable, unit-tested logic in the
// parent `sessionaffinity` package.
//
// Build (must match the target image's Go toolchain + core version — see README):
//
//	CGO_ENABLED=1 go build -buildmode=plugin -o sessionaffinity.so ./plugin
package main

import (
	"encoding/json"

	"github.com/maximhq/bifrost/core/schemas"
	sa "github.com/maximhq/bifrost/plugins/sessionaffinity"
)

// instance holds the configured plugin. Defaults are used until Init is called.
var instance = sa.Init(sa.Config{}, nil)

// Init receives the plugin's "config" object from config.json (decoded as `any`) and
// reconfigures the instance. Optional — absence keeps the zero-value defaults.
func Init(config any) error {
	var cfg sa.Config
	if config != nil {
		b, err := json.Marshal(config)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return err
		}
	}
	instance = sa.Init(cfg, nil) // no logger is passed to .so plugins
	return nil
}

// GetName is required by the loader.
func GetName() string { return instance.GetName() }

// Cleanup is required by the loader.
func Cleanup() error { return instance.Cleanup() }

// HTTPTransportPreHook is where the affinity rewrite happens.
func HTTPTransportPreHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	return instance.HTTPTransportPreHook(ctx, req)
}

// HTTPTransportPostHook is a no-op passthrough.
func HTTPTransportPostHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, resp *schemas.HTTPResponse) error {
	return instance.HTTPTransportPostHook(ctx, req, resp)
}

// HTTPTransportStreamChunkHook passes chunks through unchanged.
func HTTPTransportStreamChunkHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, chunk *schemas.BifrostStreamChunk) (*schemas.BifrostStreamChunk, error) {
	return instance.HTTPTransportStreamChunkHook(ctx, req, chunk)
}

func main() {} // required for buildmode=plugin; never executed
