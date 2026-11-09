# The secret plugin conformance kit

The kit checks a Frontforge Report Server secret plugin, in any language, against protocol version 1. It starts the plugin as reportd does (no arguments, a working folder of its own, only the environment variables you pass) and talks to it over its standard input and output. The protocol is in the guide, [Writing a secret plugin](https://docs.frontforge.com/operations/secret-plugins).

It needs Go 1.24 or later. It is under the MIT licence (`LICENSE`).

```bash
go run github.com/frontforge-corporation/report-server-releases/plugins/kit@v1 -- ./my-plugin --config cfg.json --hit kv/app#password
```

The flags may come before or after the program; the program itself gets none, as under reportd. The kit exits 0 when every check passes, 1 when one fails, and 2 when it was run wrongly.

| Flag | What it is |
|---|---|
| `--config <file>` | A JSON file holding the config sent in `init`, as reportd would send it, with its `secret:` references already resolved. Use absolute paths in it: the plugin runs in a folder of its own. |
| `--bad-config <file>` | A config `init` must refuse with `invalid-config` |
| `--hit <ref>` | A reference the plugin holds: the part after `secret:<name>:` |
| `--miss <ref>` | One it doesn't hold (a random one by default). Give one when your refs have a fixed form, such as an id of 16 hexadecimal digits: the pipelining check then sends it as it is, rather than random misses your plugin would call bad references. |
| `--bad-ref <ref>` | One it can't read: `invalid-ref` (by default one holding a NUL character) |
| `--env <NAME>` | An environment variable to pass through, as a plugin's `env` does; repeat it for more |
| `--replay <script>` | Replay one of the scripts of `exchanges.json` instead of the checks |
| `-v` | Print each line sent and answered |
| `-version` | Print the kit's version |

## The checks

- **hello:** offered `[1]`, it chooses 1 and names itself and its version; offered only a version that doesn't exist, it doesn't choose it.
- **init** takes `--config`; **bad config:** it refuses `--bad-config`.
- **get:** `--hit` answers a value, a miss `not-found`, a bad reference `invalid-ref`.
- **pipelining:** eight gets in flight at once each get their own answer, in any order.
- **unknown method:** `unsupported`. **health:** `ok`, `degraded` or `failing`.
- **writes:** a writable plugin runs `put` → `get` → `replace` → `get` → `delete` → `get`, using the `ref` its `put` answers, and `replace` must make a new version. A read-only one answers each write with `not-writable`.
- **shutdown:** it answers, and exits within 5 seconds. **input closed:** started again, it exits when its input closes.
- **stdout:** nothing but answers, by reportd's rules. **stderr:** no value the test read or wrote.

## Replaying the shared scripts

`exchanges.json` is the protocol as scripted exchanges, the same file reportd's own tests replay against their fake plugin. `--replay store`, `--replay vault` and `--replay bad-config` send a script's requests in order, and check each answer against the script's: a plugin must hold the scripts' data (the example's `secrets.json` does). What is the plugin's own (its name, version and health, an error's message, the refs and versions a writable plugin makes) is checked for shape only. `about` in the file says how each field is read.

## Versions

The kit is tagged `plugins/kit/v1.x.y` in this repository. `@v1` is the latest kit for protocol version 1; pin an exact version in CI if you'd rather choose when to take a new one.
