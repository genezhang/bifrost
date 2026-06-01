# session-affinity plugin

A Bifrost **HTTP-transport plugin** that gives Claude Code **subagents their own
prompt-cache affinity bucket** when routing to providers that honor an
`x-session-affinity` header (e.g. **Fireworks**, **Cloudflare AI Gateway**).

---

## The problem

Fireworks/Cloudflare route requests that carry the same `x-session-affinity` value
to the same backend, keeping the prompt KV-cache warm across turns.

Claude Code can set a static affinity for a whole session via the environment
variable:

```bash
export ANTHROPIC_CUSTOM_HEADERS="x-session-affinity:$(echo -n "$PWD" | md5sum | cut -d' ' -f1)"
```

This works great for a **single agent** — every request shares the project's hash and
stays on one warm backend.

It breaks down for **subagents**. `ANTHROPIC_CUSTOM_HEADERS` is a *process-level* env
var, and Claude Code subagents run **in-process** (they are async "sidechains", not
child processes). So the main agent and every subagent emit the **identical**
`x-session-affinity`. Because each subagent has a **different prompt prefix** (different
system prompt + tools), routing them all to the same backend **thrashes** the cache.

You cannot fix this at the source: the env var is static and there is no per-subagent
hook into it.

## The signal we use instead

Claude Code already tags every **subagent** request with a dedicated header (verified
in the CLI bundle):

| Header | Present on | Value |
|---|---|---|
| `x-claude-code-agent-id` | subagents only | the agent id (e.g. `name@team`), percent-escaped — **not** hashed |
| `x-claude-code-parent-agent-id` | subagents with a parent | the parent agent id |
| `x-session-affinity` | all requests | your static `ANTHROPIC_CUSTOM_HEADERS` value |

The main/top-level agent sends **no** `x-claude-code-agent-id`.

> Note: `session_id` is *not* a usable discriminator here — it lives inside the request
> body (`metadata.user_id`, which is plaintext JSON like
> `{"device_id":…,"account_uuid":…,"session_id":…}`) and is **shared** by the main agent
> and all its subagents. The agent-id header is the only per-subagent value on the wire.

## What the plugin does

In `HTTPTransportPreHook` it reads the inbound headers and recomposes the affinity:

```
main agent  ->  x-session-affinity = <base>             (left untouched)
subagent    ->  x-session-affinity = <base>:<agentId>   (own bucket, still project-namespaced)
```

where `<base>` is whatever the client already sent (your `md5(dir)`), and `<agentId>`
is `x-claude-code-agent-id`. The new value is written through
`BifrostContextKeyExtraHeaders`, which the provider layer applies to the upstream
request with `Set()` — so it **overrides** the static client value
(`core/providers/utils` → `SetExtraHeaders`).

Net effect:

- Same project + same subagent type → same bucket → warm cache across its turns.
- Different subagent type → different bucket → no cross-contamination.
- Main agent → unchanged behavior.

## Configuration

```go
sessionaffinity.Init(sessionaffinity.Config{
    IncludeParentAgentID: false, // also fold in x-claude-code-parent-agent-id
    HashOutput:           false, // md5 the composed value to a fixed 32-char hex string
}, logger)
```

- **`IncludeParentAgentID`** — append the parent agent id, useful to disambiguate agents
  that happen to share an agent id.
- **`HashOutput`** — enable if Fireworks/Cloudflare is strict about header length or
  character set; produces a clean 32-char hex bucket key.

## Enabling it in bifrost-http

This plugin is **not** registered as a built-in. To load it, add it where the HTTP
transport assembles its plugins (it is picked up automatically by the transport because
it satisfies `schemas.HTTPTransportPlugin` — see
`transports/bifrost-http/lib/config.go` → `GetLoadedHTTPTransportPlugins()` /
`rebuildDerivedPluginCaches`, which type-asserts `p.(schemas.HTTPTransportPlugin)`).

Two common paths:

1. **Built-in style** — add `sessionaffinity.PluginName` to `builtinPluginNames` in
   `transports/bifrost-http/lib/config.go` and construct it in the plugin loader where
   the other built-ins (`governance`, `logging`, …) are instantiated.
2. **Standalone/SDK** — if you embed Bifrost as a Go library, register the plugin
   instance directly with the transport's `RegisterPlugin`.

> The exact loader wiring depends on how you run Bifrost; the plugin itself is
> self-contained and provider-agnostic — it only needs to sit in the HTTP-transport
> pre-hook chain.

## Deploying to a prebuilt Docker image

A prebuilt image can't have the plugin compiled in, so it's loaded at runtime as a Go
**native `.so` plugin** via `config.json`:

```json
{
  "plugins": [
    { "enabled": true, "name": "session-affinity",
      "path": "/app/plugins/sessionaffinity.so",
      "config": { "include_parent_agent_id": false, "hash_output": false } }
  ]
}
```

`path` may also be an `http(s)://` URL (the loader downloads it). The loader looks up
package-level functions — provided by the `./plugin` shim (`plugin/main.go`).

### ⚠️ The version-lock constraint (this is the whole game)

`plugin.Open` **rejects** a `.so` unless it was built with the **exact same** Go toolchain,
`github.com/maximhq/bifrost/core` version, **libc** (the official image is **Alpine/musl**,
not glibc), and OS/arch as the bifrost binary in your image. Otherwise:
`plugin was built with a different version of package ...`.

**Do not use the `:latest` tag.** It is a moving target — when it advances and you re-pull,
your `.so` (pinned to one core version) silently stops loading. **Pin a specific tag**, and
when you deliberately bump it, **rebuild the `.so` to match**. That re-match is mechanical
(below), but it is mandatory on every version bump.

### 1. Read the exact versions out of your target image

The runtime image has no Go toolchain, so extract the binary and inspect its build info:

```bash
id=$(docker create <image>:<tag>)        # e.g. maximhq/bifrost:1.5.x
docker cp "$id":/app/main ./bifrost-main # binary lives at /app/main
docker rm "$id"
go version -m ./bifrost-main | grep -E 'mod\s+github.com/maximhq/bifrost|^.*go1\.'
# -> the `go1.XX.Y` toolchain  and  `dep github.com/maximhq/bifrost/core vA.B.C`
```

Feed those into the build args below.

### 2. Build the `.so` against those versions

From this directory, using `Dockerfile.plugin`:

```bash
docker build -f Dockerfile.plugin \
  --build-arg GO_VERSION=1.26.3 \
  --build-arg ALPINE_VERSION=3.23 \
  --build-arg CORE_VERSION=v1.5.15 \
  --target export --output type=local,dest=./out .
# -> ./out/sessionaffinity.so   (Alpine/musl, matching the image)
```

### 3. Mount it and point config at it

```bash
docker run ... \
  -v "$PWD/out/sessionaffinity.so":/app/plugins/sessionaffinity.so:ro \
  -v "$PWD/config.json":/app/config.json:ro \
  <image>:<tag>
```

### 4. Verify it loaded

The loader runs `VerifyBasePlugin` (checks `GetName`/`Cleanup`) at startup. Watch the logs:
a clean start means it loaded; a `plugin was built with a different version` error means a
version mismatch — re-check the three build args against step 1.

### Alternative: rebuild the image with the plugin built-in

Because the `.so` must be re-matched on every tag bump, the lower-maintenance option for a
frequently-updated deployment is to **build a custom image from source** at the tag you want,
with the plugin added to `builtinPluginNames` and constructed in the loader. You give up
"prebuilt," but you never fight version skew — each upgrade rebuilds the plugin in lockstep.

|  | `.so` on a pinned prebuilt tag | custom image (built-in) |
|---|---|---|
| Uses the official image | ✅ | ❌ (build from source) |
| Per-upgrade work | rebuild `.so`, re-match 3 versions | rebuild image (plugin always in lockstep) |
| Skew risk | ⚠️ must re-match each bump | ✅ none |

## Building & testing

This is a workspace module. From the repo root:

```bash
cd plugins/sessionaffinity
go mod tidy          # resolve indirect deps (needs network or a warm module cache)
go test ./...        # composeAffinity unit tests (pure, no network)
```

The affinity-composition logic (`composeAffinity`) is intentionally separated from the
hook so it is unit-testable without the `core` module — see `affinity_test.go`.

## Verifying without Fireworks

You do **not** need a Fireworks subscription to validate the rewrite — only to observe
the cache-hit behavior. Point Claude Code at Bifrost with any backend:

```bash
export ANTHROPIC_BASE_URL="http://localhost:<port>/anthropic"
export ANTHROPIC_CUSTOM_HEADERS="x-session-affinity:$(echo -n "$PWD" | md5sum | cut -d' ' -f1)"
```

Run a task that spawns a subagent, then confirm via the plugin's debug log (or raw
request capture) that:

- main-agent requests keep `x-session-affinity = <md5(dir)>`
- subagent requests get `x-session-affinity = <md5(dir)>:<agentId>`

## Maximizing cache hits: the two axes

Cache hit rate is governed by **two independent things**. Affinity is only one of them;
in practice the other one is the bigger lever.

| Axis | Controlled by | What it does |
|---|---|---|
| **Which backend** | `x-session-affinity` (this plugin) | routes you to a server that *might* have a warm cache |
| **Whether the prefix matches** | prefix stability (below) | makes the cached prefix byte-identical turn-to-turn |

They are multiplicative: affinity gets you to the right machine; prefix stability decides
whether that machine actually has your prefix cached. Affinity alone plateaus.

### Why a static `extra_headers` value isn't enough (and why this plugin exists)

The Bifrost UI (Provider → Networking → Extra Headers) and the `extra_headers` config let
you set `x-session-affinity` to a **literal string only**. The value is typed as a plain
string map end to end — `z.record(z.string(), z.string())` in the UI
(`ui/lib/schemas/providerForm.ts`), `Record<string, string>` in the config types, and a
static `map[string]string` on the backend — and it is written onto every upstream request
verbatim. There is **no CEL, template, or expression** support on the value, so it cannot
read an inbound header such as `x-claude-code-agent-id`.

That makes a static `extra_headers` entry a **single fixed bucket** for every request
through the provider — main agent and all subagents alike. It helps (you land on one warm
backend) but plateaus, because different prefixes collide in that one bucket. The only
header-reading CEL in Bifrost lives in **governance routing**, and it can only choose a
route (provider/model/key) — it cannot *set* a header. Turning one inbound header into a
different outbound value requires a request hook, which is exactly what this plugin does.

Use a static `extra_headers` value only for the single-fixed-bucket case; use this plugin
for per-subagent buckets.

### Companion setting: `CLAUDE_CODE_ATTRIBUTION_HEADER=0`

By default Claude Code splices an attribution block (issues URL, package URL, version,
git SHA) into the **message content** during normalization. That block sits in the
cacheable region. Disabling it removes that content and stabilizes the prefix:

```bash
export CLAUDE_CODE_ATTRIBUTION_HEADER=0   # also accepts false / no / off
```

In a single-agent test this took the hit rate from "improved some" to **90%+**. Set it
regardless of whether this plugin is in the path — it is a client-side, source-level fix
for the prefix axis, and it does not interact with affinity routing.

### Does Bifrost's conversion stabilize the prefix? No — verify it doesn't *de*-stabilize.

A natural hope is that routing through Bifrost (Anthropic → unified → provider) might
normalize the prefix for you and remove the need for `CLAUDE_CODE_ATTRIBUTION_HEADER=0`.
It does not:

- The attribution lives in **message content**, and the conversion layer is faithful to
  content — it translates the envelope, not the text. So the attribution survives the
  round-trip either way. (Conversion only incidentally drops fields it normalizes away,
  e.g. `metadata.user_id`; content is not one of them.)
- More importantly, re-serializing through a different schema is itself a **prefix-stability
  risk**. Automatic prefix caching keys on exact bytes; if the converter's output differs
  turn-to-turn for identical logical input (tool-argument JSON ordering, content-block
  flattening, whitespace), the gateway becomes a *new* cache-buster the direct path never
  had.

Bifrost *does* model `cache_control` breakpoints (`schemas.CacheControl`, ephemeral) and
can preserve or strip them (`StripCacheControlScope`) — but that honors the client's
caching intent, it does not clean up a varying prefix.

**Verify, don't assume.** Enable raw-request capture and diff the exact bytes Bifrost
sends upstream across two turns that share a prefix:

- byte-identical → conversion is cache-safe
- any diff → you've found a Bifrost-introduced buster; fix the converter rather than chase
  affinity

## Caveats

- **Agent id is per type/role, not per invocation.** Two concurrent `Explore` subagents
  share an agent id and therefore a bucket. For prompt-cache affinity that is usually
  *desirable* (they share a system prompt). If you need per-invocation isolation, the CLI
  does not currently expose a per-invocation id on the wire — confirm with raw capture
  before relying on one.
- **Header passthrough.** The static `x-session-affinity` must already reach the upstream
  for the single-agent case to work; this plugin overrides it for subagents via the
  provider extra-headers path.
