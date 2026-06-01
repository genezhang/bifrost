# Changelog

## 0.1.0

- Initial release. HTTP-transport plugin that rewrites the outbound
  `x-session-affinity` header for Claude Code subagent requests by folding in the
  `x-claude-code-agent-id` header, so each subagent gets its own prompt-cache
  affinity bucket instead of sharing the static value from `ANTHROPIC_CUSTOM_HEADERS`.
