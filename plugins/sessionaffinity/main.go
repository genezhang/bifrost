// Package sessionaffinity is a Bifrost HTTP-transport plugin that rewrites the
// outbound x-session-affinity header so Claude Code subagents land in their own
// prompt-cache bucket instead of sharing the single static value supplied via
// ANTHROPIC_CUSTOM_HEADERS.
//
// # Why this exists
//
// Fireworks (and Cloudflare AI Gateway) route requests carrying the same
// x-session-affinity value to the same backend, keeping the prompt KV-cache warm.
// Claude Code can set a static affinity for a whole session via the env var
// ANTHROPIC_CUSTOM_HEADERS (e.g. x-session-affinity:<md5 of the project dir>) — but
// because it is a process-level env var and subagents run in-process, every subagent
// inherits the SAME value. Different subagents have different prompt prefixes, so a
// shared affinity thrashes the cache.
//
// Claude Code already emits a per-subagent signal as a dedicated request header,
// x-claude-code-agent-id (absent on the main agent). This plugin reads it and folds
// it into x-session-affinity, giving each subagent its own bucket while keeping the
// project namespace.
//
//	main agent -> x-session-affinity = <md5(dir)>             (unchanged)
//	subagent   -> x-session-affinity = <md5(dir)>:<agentId>   (isolated)
package sessionaffinity

import (
	"github.com/maximhq/bifrost/core/schemas"
)

// PluginName is the canonical name of this plugin.
const PluginName = "session-affinity"

// Config controls how the affinity value is composed.
type Config struct {
	// IncludeParentAgentID appends x-claude-code-parent-agent-id to the bucket key.
	// Off by default — the agent id alone is usually enough.
	IncludeParentAgentID bool

	// HashOutput md5-hashes the composed value to a fixed 32-char hex string. Enable
	// if the upstream is strict about header length or character set.
	HashOutput bool
}

// Plugin implements schemas.HTTPTransportPlugin.
type Plugin struct {
	cfg    Config
	logger schemas.Logger
}

// Init creates a new session-affinity plugin. logger may be nil.
func Init(cfg Config, logger schemas.Logger) *Plugin {
	return &Plugin{cfg: cfg, logger: logger}
}

func (p *Plugin) GetName() string { return PluginName }

func (p *Plugin) Cleanup() error { return nil }

// HTTPTransportPreHook rewrites x-session-affinity for subagent requests.
func (p *Plugin) HTTPTransportPreHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	if ctx == nil || req == nil {
		return nil, nil
	}

	agentID := req.CaseInsensitiveHeaderLookup(HeaderAgentID)
	base := req.CaseInsensitiveHeaderLookup(HeaderAffinity)
	var parentID string
	if p.cfg.IncludeParentAgentID {
		parentID = req.CaseInsensitiveHeaderLookup(HeaderParentAgentID)
	}

	value, changed := composeAffinity(base, agentID, parentID, p.cfg.IncludeParentAgentID, p.cfg.HashOutput)
	if !changed {
		// Main / top-level agent: leave the static affinity untouched.
		return nil, nil
	}

	p.setExtraHeader(ctx, HeaderAffinity, value)

	if p.logger != nil {
		p.logger.Debug("[%s] rewrote %s %q -> %q (agent=%q)", PluginName, HeaderAffinity, base, value, agentID)
	}
	return nil, nil
}

// HTTPTransportPostHook is a no-op; this plugin only touches outbound requests.
func (p *Plugin) HTTPTransportPostHook(_ *schemas.BifrostContext, _ *schemas.HTTPRequest, _ *schemas.HTTPResponse) error {
	return nil
}

// HTTPTransportStreamChunkHook passes chunks through unchanged.
func (p *Plugin) HTTPTransportStreamChunkHook(_ *schemas.BifrostContext, _ *schemas.HTTPRequest, chunk *schemas.BifrostStreamChunk) (*schemas.BifrostStreamChunk, error) {
	return chunk, nil
}

// setExtraHeader writes the value into BifrostContextKeyExtraHeaders, which the
// provider layer applies to the upstream request via Set() — overriding the
// client-supplied x-session-affinity (see core/providers/utils SetExtraHeaders).
//
// Copy-on-write: the existing map may be shared across the request, so we never
// mutate it in place (the repo's recurring map-race gotcha).
func (p *Plugin) setExtraHeader(ctx *schemas.BifrostContext, key, value string) {
	existing, _ := ctx.Value(schemas.BifrostContextKeyExtraHeaders).(map[string][]string)
	merged := make(map[string][]string, len(existing)+1)
	for k, v := range existing {
		merged[k] = v
	}
	merged[key] = []string{value}
	ctx.SetValue(schemas.BifrostContextKeyExtraHeaders, merged)
}

// Compile-time assertion that *Plugin satisfies the transport plugin interface.
var _ schemas.HTTPTransportPlugin = (*Plugin)(nil)
