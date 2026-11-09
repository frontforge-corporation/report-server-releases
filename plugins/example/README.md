# An example secret plugin

A small, read-only secret plugin for Frontforge Report Server, in Go with only the standard library. It answers `secret:<name>:<ref>` references from a JSON file. Copy it and change where it reads from: the guide, [Writing a secret plugin](https://docs.frontforge.com/operations/secret-plugins), explains the protocol it speaks.

It is under the MIT licence (`LICENSE`).

## The secrets file

```json
{
  "billing#password": {"value": "hunter2", "version": "3"},
  "token": {"value": "t0k3n", "expiresAt": "2030-01-01T00:00:00Z"},
  "forbidden": {"error": "denied"}
}
```

Each key is a `ref`. An entry has a `value`, and optionally a `version` and an `expiresAt` (RFC 3339). An entry with `error` answers that error code instead, so you can see what reportd does with each one. The file is read again on each `get`. `secrets.json` here holds the data of the conformance kit's shared scripts.

## Build and install

Build it for the container image (Linux, with no system libraries):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags=-s -o reportd-secret-example .
```

Use `GOARCH=arm64` for an arm64 host. Put the program and your secrets file in a folder that only root can write, mount it read-only, and name it in `config.yaml`:

```yaml
secrets:
  pluginDirs: [/plugins]
  plugins:
    - name: example
      program: reportd-secret-example
      config:
        file: /plugins/secrets.json
```

`secret:example:billing#password` is then `hunter2`. A real plugin wouldn't keep its secrets in a plain file next to it, of course: that is what `secret:file:` already does.

## Test it with the kit

```bash
echo '{"file": "'"$PWD"'/secrets.json"}' > cfg.json
echo '{}' > bad.json
go build -o reportd-secret-example .
go run github.com/frontforge-corporation/report-server-releases/plugins/kit@v1 -- ./reportd-secret-example --config cfg.json --bad-config bad.json --hit 'billing#password'
go run github.com/frontforge-corporation/report-server-releases/plugins/kit@v1 -- ./reportd-secret-example --config cfg.json --replay store
```
