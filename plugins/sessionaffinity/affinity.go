package sessionaffinity

import (
	"crypto/md5"
	"encoding/hex"
)

// Canonical inbound header names (Claude Code sends them lower-cased).
const (
	// HeaderAffinity is the prompt-cache affinity header consumed by Fireworks /
	// Cloudflare. Claude Code can supply a static base value via ANTHROPIC_CUSTOM_HEADERS.
	HeaderAffinity = "x-session-affinity"

	// HeaderAgentID is set by Claude Code ONLY on subagent (sidechain) requests.
	// It is absent on the main/top-level agent. Value is the agent id (e.g. "name@team").
	HeaderAgentID = "x-claude-code-agent-id"

	// HeaderParentAgentID is set by Claude Code to the parent agent's id. Optional;
	// useful if you want to disambiguate agents that share an agent id.
	HeaderParentAgentID = "x-claude-code-parent-agent-id"
)

// composeAffinity derives the outbound x-session-affinity value for a request.
//
//   - base:    the inbound x-session-affinity (e.g. md5(project dir)) supplied statically
//     via ANTHROPIC_CUSTOM_HEADERS; "" if the client sent none.
//   - agentID: the inbound x-claude-code-agent-id header; "" for the main agent.
//   - parentID: the inbound x-claude-code-parent-agent-id header; "" if absent. Included
//     only when includeParent is true (off by default — agent id alone is usually enough).
//   - hash:    when true, the composed value is md5-hashed to a fixed 32-char hex string
//     (useful if the upstream is strict about header length/charset).
//
// It returns (value, changed). changed is false when there is nothing to rewrite — i.e.
// no agent id, meaning this is the main agent and the static affinity is left untouched.
//
// Resulting buckets:
//
//	main agent  -> (unchanged; base passes through)
//	subagent    -> base + ":" + agentID            (namespaced under the project)
//	subagent    -> base + ":" + agentID + ":" + parentID   (when includeParent)
func composeAffinity(base, agentID, parentID string, includeParent, hash bool) (string, bool) {
	if agentID == "" {
		return "", false
	}

	value := agentID
	if base != "" {
		value = base + ":" + agentID
	}
	if includeParent && parentID != "" {
		value = value + ":" + parentID
	}

	if hash {
		sum := md5.Sum([]byte(value))
		value = hex.EncodeToString(sum[:])
	}
	return value, true
}
