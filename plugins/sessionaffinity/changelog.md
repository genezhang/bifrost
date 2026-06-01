# Changelog

## 0.1.2

- Add `build.sh`: one command builds a version-matched `.so` for a given prebuilt image
  (auto-detects Go toolchain, `core`, and Alpine versions; all overridable via env).

## 0.1.1

- Add `plugin/main.go` `package main` shim and `Dockerfile.plugin` so the plugin can be
  built as a Go native `.so` and loaded by a prebuilt bifrost image via `config.json`.
- Add JSON tags to `Config` (`include_parent_agent_id`, `hash_output`) for `config.json`.
- Document deploying to a prebuilt Docker image, including the Go-plugin version-lock
  constraint and why `:latest` should be pinned.

## 0.1.0

- Initial release. HTTP-transport plugin that rewrites the outbound
  `x-session-affinity` header for Claude Code subagent requests by folding in the
  `x-claude-code-agent-id` header, so each subagent gets its own prompt-cache
  affinity bucket instead of sharing the static value from `ANTHROPIC_CUSTOM_HEADERS`.
