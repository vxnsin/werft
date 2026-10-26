# werft.yml

The file sits in the root of the project. Only `schema` and `name` are required, everything else has defaults. Unknown fields are an error so typos do not slip through quietly. `werft validate` checks the file at any time.

For completion in VS Code (with the YAML extension by Red Hat) put this line at the top:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/vxnsin/werft/main/schema/werft.schema.json
```

## Basics

| Field | Default | |
|---|---|---|
| `schema` | | always `1` |
| `name` | | name on the server: `a-z`, `0-9`, `-`. The directory and the systemd service are named after it. |
| `description` | | shown in `werft status` and in the unit |

## source

| Field | Default | |
|---|---|---|
| `channel` | `tags` | `tags`: only releases like `v1.4.2`. `branch`: every new commit on `branch`. |
| `branch` | `main` | |
| `verify_signature` | `false` | only tags with a valid signature (`git verify-tag`) |

With `channel: tags` only tags like `v1.2.3` or `1.2.3` count. Pre-releases such as `v2.0.0-rc.1` are skipped unless you ask for them with `--ref`.

## requires

Checked before the install and before every update. Ports only on install, because during an update the running app holds them itself.

```yaml
requires:
  os: [debian>=12, ubuntu>=22.04]   # one of them
  root: true
  commands: [git, python3>=3.11]     # version from "program --version"
  apt: [ffmpeg, libopus0]            # werft installs missing packages after asking
  ports: [5060/udp, 8080]            # tcp if not given
```

## questions

```yaml
questions:
  - id: sip_server
    prompt: Address of the SIP server
    help: You find it in the web interface of the phone system under Telephony.
    validate: '^[\w.-]+$'
  - id: brain
    prompt: What should the phone think with?
    type: choice
    choices: [hermes, claude, ollama]
  - id: ollama_url
    prompt: Address of Ollama
    type: url
    default: http://localhost:11434
    when: brain == ollama
  - id: api_key
    type: password
    generate: random(32)
```

| Field | |
|---|---|
| `id` | key, `{{ .id }}` in templates, upper case in the `.env` file. `werft_` at the start is reserved. |
| `prompt` | the question |
| `help` | explanation shown before the question |
| `type` | `string` (default), `int`, `bool`, `choice`, `password`, `path`, `port`, `url` |
| `default` | suggestion, taken with Enter |
| `choices` | for `choice`; the answer may also be the number |
| `validate` | regular expression |
| `secret` | the answer goes into `secrets.json` (0600). Always for `password`. |
| `optional` | an empty answer is allowed. Empty values are left out of the `.env` file, so the app falls back to its own default. |
| `warden` | only for `port`: when a [warden](https://github.com/vxnsin/warden) runs on the server, werft takes the port it hands out under the app name and does not ask. The app gets `WARDEN_SERVICE` and `WARDEN_PORT` in its `.env` file so it knows the port is already held. Without a warden the question is asked as usual. |
| `when` | only ask if `id`, `!id`, `id == value` or `id != value` |
| `env` | a different name in the `.env` file |
| `generate` | do not ask, generate instead: `random(N)` |

Answers are stored. If an update brings a new question, only that one is asked. Without a terminal (timer, script) the default is used; if there is none, werft stops and names the missing answer.

Unattended installs use an answers file:

```sh
werft install https://github.com/you/project --answers answers.yml
```

```yaml
sip_server: 192.168.1.10
brain: ollama
```

Or the environment: `WERFT_ANSWER_SIP_SERVER=192.168.1.10`.

## config

```yaml
config:
  env_file: /etc/phone/.env
  env:
    NODE_ENV: production
    DATA_DIR: "{{ .werft_shared }}/data"
  templates:
    - src: .werft/config.toml.tmpl
      dest: /etc/phone/config.toml
      mode: "0640"
```

`env` holds fixed values that are not questions. They may use the same `{{ }}` placeholders as templates. They go into the `.env` file and are also set for the build steps, which matters for tools that read variables at build time, such as Vite or Next.js.

The `.env` file holds every answer as `KEY=value`. Both systemd (`EnvironmentFile`) and docker compose (`--env-file`) understand the format. Templates are Go templates: `{{ .sip_server }}`, `{{ if eq .brain "ollama" }}…{{ end }}`.

Besides the answers, all templates as well as `exec`, `health` and `backup.paths` know:

| | |
|---|---|
| `{{ .werft_app }}` | name of the app |
| `{{ .werft_version }}` | version being installed |
| `{{ .werft_current }}` | `/opt/werft/<name>/current` |
| `{{ .werft_shared }}` | `/opt/werft/<name>/shared` |

If the service runs as its own user (`runtime.systemd.user`), that user's group may read the `.env` file and the templates.

## shared

```yaml
shared: [data, uploads]
```

Every release is unpacked fresh. Whatever has to survive updates belongs in `shared`: werft creates the directories under `/opt/werft/<name>/shared/` and links them into every release. The first time, the content from the project is the starting point.

## install and update

```yaml
install:
  - python3 -m venv .venv
  - name: Packages
    run: .venv/bin/pip install -r requirements.txt
  - run: npm ci && npm run build
    dir: web

update:
  steps:
    - .venv/bin/python manage.py collectstatic --noinput
  backup:
    paths: [/etc/phone, "{{ .werft_shared }}/data"]
    keep: 5
  migrations: .werft/migrations
  restart: always
  keep_releases: 3
```

`install` runs on install and on every update, always in the new release directory while the old version keeps running. `update.steps` runs in addition, only on updates.

Every step sees all answers in its environment (like in the `.env` file) plus `WERFT_APP`, `WERFT_VERSION`, `WERFT_PREVIOUS_VERSION`, `WERFT_RELEASE_DIR` and `WERFT_SHARED_DIR`.

Migrations are scripts in a directory, sorted by name (`001_call_log.sh`, `002_…`). Each runs exactly once, with the first update that brings it. They do not run on install, there they count as done; the install has to set up the current state by itself. A rollback does not reverse migrations.

| Field | Default | |
|---|---|---|
| `backup.keep` | `5` | number of backups kept |
| `restart` | `always` | `never`: do not restart after an update |
| `keep_releases` | `3` | number of release directories kept (at least 2) |

## runtime

```yaml
runtime:
  type: systemd
  systemd:
    exec: .venv/bin/python -m phone
    user: phone
    restart: on-failure
    restart_sec: 5
```

| `type` | |
|---|---|
| `systemd` | werft writes `/etc/systemd/system/<name>.service` and starts the service. Relative paths in `exec` start at the release directory. `user` is created as a system user if it does not exist and owns `shared/`. |
| `compose` | `docker compose -p <name> -f <release>/<file> up -d`. `pull: true` pulls new images first, `build: true` builds. The `.env` file is passed as `--env-file`. |
| `none` | werft starts nothing. For tools, static sites or things that run some other way. |

With compose, relative bind mounts like `./data:/data` point into the release directory. Anything that has to stay belongs in a named volume or in `shared`.

## health

```yaml
health:
  http: http://localhost:{{ .port }}/health
  timeout: 30s
  rollback_on_fail: true
```

After every start werft asks until the answer is below 400 or `timeout` runs out. Instead of `http` you can use `cmd`, then exit code 0 counts. If the check fails after an update, werft brings back the previous version, configuration included.

## hooks

```yaml
hooks:
  pre_install: .werft/hooks/before.sh
  post_install: .werft/hooks/after.sh
  pre_update: .werft/hooks/before-update.sh
  post_update: .werft/hooks/after-update.sh
  pre_uninstall: .werft/hooks/cleanup.sh
```

Scripts run with `sh` in the release directory and with the same environment as the steps. `pre_update` runs before anything happens to the running version, so it is the place for something like a database dump.

## Automatic updates

They are not part of this file. Whether an app updates on its own, and when, is decided on the server with `werft auto`. See the README.
