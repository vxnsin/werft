---
name: werft
description: Add werft (https://github.com/vxnsin/werft) to a project so it can be installed and updated on a Linux server from git. Use when the user asks to make a project installable or updatable with werft, to write or fix a werft.yml, to add auto updates, or to prepare a repository for "werft install".
---

# Adding werft to a project

werft installs a project from git onto a Linux server and keeps it updated. Everything it needs is in one file in the repository root: `werft.yml`. Your job is to write that file so it fits the project as it is, not to restructure the project.

Reference for every field: https://github.com/vxnsin/werft/blob/main/docs/werft-yml.md
JSON schema: https://raw.githubusercontent.com/vxnsin/werft/main/schema/werft.schema.json

## 1. Read the project first

Find out, from the code and not from guesses:

- **How it is built**: `package.json` scripts, `requirements.txt` / `pyproject.toml`, `go.mod`, `Cargo.toml`, a `Dockerfile` or `docker-compose.yml`, existing install scripts (`install.sh`, `setup.py`, `*.ps1`). Existing install scripts are the best source for the build steps.
- **How it runs**: a long-running server (systemd), several containers (compose), or nothing that runs (static site, CLI tool: `none`).
- **What it needs to be configured**: `.env.example`, `config.example.*`, `os.getenv` / `process.env` calls, settings files. Every value a person has to choose becomes a question. Secrets the app can make up itself (session keys, internal tokens) become `generate: random(32)`.
- **What must survive updates**: database files, uploads, caches the user cares about. These go into `shared`.
- **How to tell it works**: a health or status endpoint, or a command. If there is none, use the main page (`http: http://localhost:{{ .port }}/`).

## 2. Write werft.yml

Start from this shape and drop what the project does not need:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/vxnsin/werft/main/schema/werft.schema.json
schema: 1
name: <repo-name, lowercase, a-z 0-9 ->
description: <one plain sentence>

requires:
  os: [debian>=12, ubuntu>=22.04]
  commands: [<runtime with version, e.g. node>=20 or python3>=3.11>]
  ports: [<port>/tcp]

questions:
  - id: port
    prompt: Which port should <name> listen on?
    type: port
    default: <port from the code>

config:
  env_file: /etc/<name>/.env

shared: [<data dirs>]

install:
  - <build steps, in the order the README or install script uses>

update:
  backup:
    paths: ["{{ .werft_shared }}/<data dir>", /etc/<name>]

runtime:
  type: systemd
  systemd:
    exec: <start command, relative paths start at the release directory>
    user: <name>

health:
  http: http://localhost:{{ .port }}/<health path>
```

Rules that matter:

- **Question ids are lower snake case and become env vars in upper case.** `id: db_url` arrives as `DB_URL`. If the app reads a different name, set `env:` instead of renaming the variable in the app.
- **Read config from the environment.** systemd loads `config.env_file` into the service, compose gets it as `--env-file`. If the app only reads a config file, add a template under `.werft/` and list it in `config.templates`.
- **Optional settings** (integrations, tokens the app can live without) get `optional: true`. Empty answers are left out of the `.env` file, so the app keeps its own default. Group related optional questions behind a `bool` question and `when:`, so nobody has to press Enter twenty times.
- **Fixed values that are not questions** go into `config.env`, e.g. `NODE_ENV: production`, `DATA_DIR: "{{ .werft_shared }}/data"`, or `VITE_CLIENT_ID: "{{ .client_id }}"`. They land in the `.env` file and are also set for the build steps, which is what build-time variables (Vite, Next.js `NEXT_PUBLIC_*`) need.
- **Never put real secrets into werft.yml or the repo.** Passwords are `type: password` questions or `generate`.
- **Everything the app writes at runtime goes to `shared`**, otherwise it is gone after the next update. For compose, relative bind mounts must point into a `shared` directory or be named volumes.
- **Node projects**: `npm ci` then `npm run build`; `exec: node dist/server.js` or the `start` script as `npm run start`. Static sites built to `dist/` can use `runtime: none` and serve the directory with an existing web server; say so in the description.
- **Next.js**: also put `.next/cache` into `shared` (the service user cannot write into the root-owned release directory, and image optimization caches there). Run it with `exec: node_modules/.bin/next start`; the port comes from `PORT` in the `.env` file.
- **Native Node modules** (better-sqlite3, sharp) usually ship prebuilt binaries; add `build-essential` and `python3` to `requires.apt` anyway so `npm ci` can fall back to compiling.
- **If the project already has an install script** (`scripts/setup-*.sh`), keep it. werft replaces the app part (clone, build, service, env file); things outside the app like a reverse proxy, DNS or tunnels stay in that script or in the README.
- **Python projects**: `python3 -m venv .venv` and `.venv/bin/pip install -r requirements.txt`; exec through `.venv/bin/...`.
- **Docker projects**: `runtime: compose`, `pull: true` for registry images, `build: true` if the compose file builds. No install steps are needed unless something has to be generated first.
- **Database changes between versions** go into `update.migrations` as numbered shell scripts (`001_add_column.sh`). They run once, on the first update that brings them, never on a fresh install.
- **Automatic updates** only when the user wants them: `auto_update: { enabled: true, schedule: "*-*-* 04:00", policy: patch }`.
- Keep `channel: tags` (the default) unless the project has no releases and the user wants every commit deployed.

## 3. Check it

Run `werft validate` in the project root if werft is installed. Otherwise validate against the JSON schema and re-read the reference. Then check by hand:

- every `{{ .x }}` refers to a question id or one of `werft_app`, `werft_version`, `werft_current`, `werft_shared`
- every path in `install`, `hooks`, `templates.src` and `migrations` exists in the repository
- the `exec` command works from the repository root after the install steps ran

## 4. Tell the user what is left

- werft installs from tags by default, so the project needs a release tag: `git tag v1.0.0 && git push --tags`.
- Install on the server with `sudo werft install https://github.com/<owner>/<repo>`.
- Add a short "Install" section to the README with that command. Keep it short and in the voice of the existing README.

Do not add a werft badge, a features list or marketing text to the README.
