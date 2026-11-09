# Frontforge Report Server

Frontforge Report Server is a report server you run yourself. You design reports in the browser, and your applications render them as PDF, Word, Excel or HTML from their own data, through an HTTP API.

This repository holds what's public: the releases, and the example gallery your install reads. The code is developed elsewhere; this repository is written by its release and publishing workflows, not by hand.

## The Community edition

Frontforge Report Server is free to download and run as the Community edition: up to 5 templates, 1 sub-report per template and 3 designer seats. Every install needs a licence, and the Community licence is free and never expires: get one at <https://myaccount.frontforge.com/community> with your email address. A paid licence lifts those limits and adds the features of the paid editions. The licence is checked offline, on your server.

Community is used under the Frontforge Report Server Community Licence. Each release carries it as `LICENSE`, and the setup wizard shows it and asks you to accept it. In short: you may use Community, commercial use included, and render documents for your own customers from your own applications. You may not get around its limits, give anyone outside your organisation the studio or template management as a hosted service, or redistribute it. The licence itself is what applies.

## Run it

You need Docker with Compose v2.

1. Get your free Community licence at <https://myaccount.frontforge.com/community>. It's emailed to you.
2. From the [latest release](https://github.com/frontforge-corporation/report-server-releases/releases/latest), download `report-server-compose-<version>.zip` and unzip it.
3. In its folder:

   ```bash
   cp .env.example .env
   docker compose up -d
   docker compose logs reportd | grep setupCode
   ```

4. Open `http://localhost:43123/setup` and enter the code from the log. The setup wizard asks for your licence and creates the first administrator.

The bundle's own README covers the add-ons, backups and upgrading, and [the documentation](https://docs.frontforge.com/operations/docker) covers running it in full. Each release lists `SHA256SUMS` for its files and carries an SBOM of the image.

The image is `ghcr.io/frontforge-corporation/report-server`, for amd64 and arm64, tagged with each version, its `x.y`, and `latest`.

## The example gallery

`gallery/` holds the examples the studio offers under "Start from an example": bills, letters, documentation and internal business documents of three made-up organisations. Every install reads this folder, at most once an hour. An example that needs a newer report language than an install has is left out of its list, so new examples never break older installs.

The examples are dedicated to the public domain under CC0 1.0 Universal (`gallery/LICENSE.md`): copy, change and use them for anything, commercial use included, with no credit needed. CC0 covers the examples only, not Frontforge Report Server.

On a machine without internet access, copy the `gallery/` folder to it and set `REPORTD_EXAMPLES_GALLERY` to that folder. Each example's `.zip` can also be imported into the studio directly.

## What your install sends

Once a day, reportd asks this repository for its latest release, to tell administrators when a newer version is out, and once an hour it reads the gallery. Those requests are all; there is no other telemetry. `REPORTD_UPDATES_CHECK=false` turns the version check off, and `REPORTD_EXAMPLES_GALLERY=off` the gallery.

## Help

The documentation is at <https://docs.frontforge.com>: installing and running it, designing reports, the report language and the API.

Questions go to Frontforge at <support@frontforge.com>. Community comes with no promised response time, but every message is read. Report a security issue to <security@frontforge.com> instead, as [the security page](https://docs.frontforge.com/operations/security) says. Issues and discussions are off in this repository.

Frontforge Report Server is made by Frontforge Pty Ltd.
