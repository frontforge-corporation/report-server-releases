# Frontforge Report Server

Frontforge Report Server is a report server you run yourself. You design reports in the browser, and your applications render them as PDF, Word, Excel or HTML from their own data, through an HTTP API.

This repository holds what's public, as source: the setup that runs each release, the example reports, sample clients for the API, and the secret plugin kit. The code of the server is developed elsewhere; this repository is written by its release and publishing workflows, not by hand.

| Folder | What |
| --- | --- |
| [`compose/`](compose/) | The release's Docker Compose setup: run it from a clone. |
| [`examples/`](examples/) | The example reports' source: RFML, sample data, schemas, tests and images. |
| [`gallery/`](gallery/) | The same examples as your install reads them, built from `examples/`. |
| [`clients/`](clients/) | Sample API clients in C#, Python, Go and TypeScript. |
| [`plugins/`](plugins/) | An example secret plugin, and the kit that checks one. |
| `LICENSE`, `THIRD_PARTY_NOTICES.md`, [`licences/`](licences/) | The Community licence, and the third-party software in the release with its licence texts. |
| `VERSION` | The release this checkout runs. |

## The Community edition

Frontforge Report Server is free to download and run as the Community edition: up to 5 templates, 1 sub-report per template and 3 designer seats. Every install needs a licence, and the Community licence is free and never expires: get one at <https://myaccount.frontforge.com/community> with your email address. A paid licence lifts those limits and adds the features of the paid editions. The licence is checked offline, on your server.

Community is used under the Frontforge Report Server Community Licence. Each release carries it as `LICENSE`, and the setup wizard shows it and asks you to accept it. In short: you may use Community, commercial use included, and render documents for your own customers from your own applications. You may not get around its limits, give anyone outside your organisation the studio or template management as a hosted service, or redistribute it. The licence itself is what applies.

## Run it

You need Docker with Compose v2, and git.

1. Get your free Community licence at <https://myaccount.frontforge.com/community>. It's emailed to you.
2. Clone this repository and start the compose setup:

   ```bash
   git clone https://github.com/frontforge-corporation/report-server-releases.git
   cd report-server-releases/compose
   cp .env.example .env
   docker compose up -d
   docker compose logs reportd | grep setupCode
   ```

   For a release other than the newest, clone its tag or line instead: `git clone --branch v0.10.0 …` or `--branch release/0.10`.
3. Open `http://localhost:43123/setup` and enter the code from the log. The setup wizard asks for your licence and creates the first administrator.

To upgrade, take a backup, then `git pull` and `docker compose pull && docker compose up -d` in `compose/`. Its README covers the add-ons, backups and upgrading, and [the documentation](https://docs.frontforge.com/operations/docker) covers running it in full.

The image is `ghcr.io/frontforge-corporation/report-server`, for amd64 and arm64, tagged with each version, its `x.y`, and `latest`. The compose files name the release's version.

## Releases, branches and tags

- **`main` is the newest release.** Clone it to run the latest version.
- **`release/<x.y>`**, such as `release/0.10`, is the newest release of that line, pre-releases included. Follow it to get that line's fixes and nothing newer.
- **`v<version>`**, such as `v0.10.0`, is one release, and never moves. (`v0.9.0` and `v0.10.0-rc.1` moved once, in November 2026, onto commits that hold their source: in a clone made before, `git fetch --tags --force` updates them.) Its [GitHub release](https://github.com/frontforge-corporation/report-server-releases/releases) has the notes, the image's SBOM, the source of the image's Debian packages (`report-server-<version>-debian-sources.tar`, from 0.10.0), the secret plugins for installs outside the image, and `SHA256SUMS`.

Pre-releases (a version with a `-`, such as `0.10.0-rc.1`) are for trying out: they are on their tag and their line's branch, never on `main`.

## The example gallery

`examples/` holds the examples the studio offers under "Start from an example": bills, letters, documentation and internal business documents of three made-up organisations, each a folder of RFML, sample data, schemas, tests and images. `gallery/` is built from it, an `index.json` and a `.zip` per example, and every install reads it from `main`, at most once an hour. An example that needs a newer report language than an install has is left out of its list, so new examples never break older installs.

The examples are dedicated to the public domain under CC0 1.0 Universal (`examples/LICENSE.md`): copy, change and use them for anything, commercial use included, with no credit needed. CC0 covers the examples only, not Frontforge Report Server.

On a machine without internet access, set `REPORTD_EXAMPLES_GALLERY` to the `gallery/` or `examples/` folder of a clone. Each example's `.zip` can also be imported into the studio directly.

## Sample clients

[`clients/`](clients/) holds small programs that show how an application uses the API: in C# (.NET), Python, Go and TypeScript (Node.js). Each authenticates with an API key, lists the templates, renders a PDF straight away, renders a batch in the background and downloads it, handles the API's errors, and listens to the server's events. They use only each language's own HTTP client, and are under the MIT licence (`clients/LICENSE`), so copy them into your application and change them as you like. [`clients/README.md`](clients/README.md) says how to run them.

## Secret plugins

`plugins/example` is a small secret plugin to copy, and `plugins/kit` the conformance kit that checks a plugin against the protocol (`go run github.com/frontforge-corporation/report-server-releases/plugins/kit@v1`). [Writing a secret plugin](https://docs.frontforge.com/operations/secret-plugins) explains them. Both are under the MIT licence.

## What your install sends

Once a day, reportd asks this repository for its latest release, to tell administrators when a newer version is out, and once an hour it reads the gallery. Those requests are all; there is no other telemetry. `REPORTD_UPDATES_CHECK=false` turns the version check off, and `REPORTD_EXAMPLES_GALLERY=off` the gallery.

## Help

The documentation is at <https://docs.frontforge.com>: installing and running it, designing reports, the report language and the API.

Questions go to Frontforge at <support@frontforge.com>. Community comes with no promised response time, but every message is read. Report a security issue to <security@frontforge.com> instead, as [the security page](https://docs.frontforge.com/operations/security) says. Issues and discussions are off in this repository.

Frontforge Report Server is made by Frontforge Pty Ltd.
