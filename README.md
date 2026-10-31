# Reportforge

Reportforge is a report server you run yourself. You design reports in the browser, and your applications render them as PDF, Word, Excel or HTML from their own data, through an HTTP API.

This repository holds what's public: the releases, and the example gallery your install reads. The code is developed elsewhere; this repository is written by its release and publishing workflows, not by hand.

## The Community edition

Reportforge is free to download and run as the Community edition: up to 5 templates, 1 sub-report per template and 3 designer seats. A licence lifts those limits and adds the features of the paid editions.

## Run it

You need Docker with Compose v2.

1. From the [latest release](https://github.com/frontforge-corporation/report-server-releases/releases/latest), download `reportforge-compose-<version>.zip` and unzip it.
2. In its folder:

   ```bash
   cp .env.example .env
   docker compose up -d
   docker compose logs reportd | grep setupCode
   ```

3. Open `http://localhost:43123/setup` and enter the code from the log. The setup wizard creates the first administrator.

The bundle's own README covers the add-ons, backups and upgrading, and [the documentation](https://docs.frontforge.com/operations/docker) covers running it in full. Each release lists `SHA256SUMS` for its files and carries an SBOM of the image.

The image is `ghcr.io/frontforge-corporation/report-server`, for amd64 and arm64, tagged with each version, its `x.y`, and `latest`.

## The example gallery

`gallery/` holds the examples the studio offers under "Start from an example": the documents of Larkmere Water, a fictional water utility. Every install reads this folder, at most once an hour. An example that needs a newer report language than an install has is left out of its list, so new examples never break older installs.

On a machine without internet access, copy the `gallery/` folder to it and set `REPORTD_EXAMPLES_GALLERY` to that folder. Each example's `.zip` can also be imported into the studio directly.

## What your install sends

Once a day, reportd asks this repository for its latest release, to tell administrators when a newer version is out, and once an hour it reads the gallery. Those requests are all; there is no other telemetry. `REPORTD_UPDATES_CHECK=false` turns the version check off, and `REPORTD_EXAMPLES_GALLERY=off` the gallery.

## Help

The documentation is at <https://docs.frontforge.com>: installing and running it, designing reports, the report language and the API.

Questions go to Frontforge by email or through the contact form on the Reportforge site, both coming with the first release. Issues and discussions are off in this repository.

Reportforge is made by Frontforge Pty Ltd.
