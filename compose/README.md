# Frontforge Report Server 0.10.0-rc.1 with Docker Compose

This folder runs Frontforge Report Server 0.10.0-rc.1 on one machine. You need Docker with Compose v2 (`docker compose version`) and git. The image is `ghcr.io/frontforge-corporation/report-server:0.10.0-rc.1`, for amd64 and arm64.

## Start it

From a clone of the repository, in this folder:

```bash
cp .env.example .env
docker compose up -d
```

Edit `.env` first if you want a different port (`REPORTD_PORT`, 43123 by default), or set `REPORTD_PUBLICURL` to the address people will use. git ignores `.env`, so your settings stay when you pull the next release.

Every install needs a licence. The Community licence is free and never expires: get one at <https://myaccount.frontforge.com/community> with your email address, before or while you start.

On the first start, Frontforge Report Server is in setup mode. Read the one-time setup code from its log:

```bash
docker compose logs reportd | grep setupCode
```

Then open `http://localhost:43123/setup` and enter the code. The setup wizard asks for your licence and creates the first administrator.

## Which release

The repository's `main` branch is the newest release, `release/<x.y>` (such as `release/0.10`) the newest release of that line, and each release is a tag, `v<version>`. This folder is the same in each: only the version it runs differs.

## The add-ons

`postgres.yml` keeps users and sessions in Postgres, and `split.yml` runs renderers as their own service so renders spread over several. Both need a licence. List them after the main file:

```bash
docker compose -f docker-compose.yml -f postgres.yml up -d
docker compose -f docker-compose.yml -f split.yml up -d --scale renderd=3
```

They make their passwords and keys in `secrets/` on the first start. Keep that folder: git ignores what's in it.

## Back it up

Everything Frontforge Report Server keeps is in the `data` volume. To take a backup and copy it out:

```bash
docker compose exec reportd reportd backup --out /data/backups/manual.tar.gz
docker compose cp reportd:/data/backups/manual.tar.gz .
```

## Upgrade

Take a backup, read the new release's notes, then bring this folder to the new release and start it:

```bash
git pull                  # on main or release/<x.y>; or: git checkout v<version>
docker compose pull
docker compose up -d
```

The compose files name the release's image, so `git pull` is what moves you to it. If your `.env` sets `REPORTD_IMAGE`, it holds you on that image: remove the line.

Frontforge Report Server backs up its data before it upgrades it, and a newer data format is never downgraded: to go back, restore a backup taken before the upgrade. The whole procedure is <https://docs.frontforge.com/operations/upgrading>.

## More

- The documentation: <https://docs.frontforge.com>. Running Frontforge Report Server in Docker is <https://docs.frontforge.com/operations/docker>, and backups are <https://docs.frontforge.com/operations/backup>.
- Releases and their notes: <https://github.com/frontforge-corporation/report-server-releases/releases>
- Help: <support@frontforge.com>. Community comes with no promised response time. Security issues go to <security@frontforge.com>.
- Frontforge Report Server checks once a day for a newer release and tells administrators. It sends nothing but that request. `REPORTD_UPDATES_CHECK=false` in `.env` turns it off.
- `LICENSE` is the licence you use Frontforge Report Server under: the Frontforge Report Server Community Licence, which the setup wizard also shows and asks you to accept. A paid edition's licence has its own agreement.
