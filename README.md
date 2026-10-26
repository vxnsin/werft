<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/wordmark-dark.svg">
    <img src="assets/wordmark.svg" alt="werft" width="380">
  </picture>
</p>

werft installs your own projects from git onto a Linux server and keeps them up to date afterwards. What it needs to know lives in the project itself, in a `werft.yml`: which questions to ask during install, how to build, how the thing runs (systemd or docker compose), and how to tell that it still works after an update.

I wrote it because every one of my projects ended up with its own install script, each a little different. And an update usually meant: ssh in, `git pull`, restart the service, hope.

```console
$ sudo werft install https://github.com/vxnsin/notepad
  git        github.com/vxnsin/notepad    v1.4.2

  notepad v1.4.2    Notes in the browser, a small Flask service

  check      requirements                 ok
  Which port should notepad listen on? [8080]:
  Title at the top of the page [Notepad]: Team notes
  Require a password? [Y/n]:
  Password:

  release    releases/v1.4.2              ok
  step       python3 -m venv .venv        ok
  step       Packages                     ok
  config     /etc/notepad/.env            ok
  restart    notepad.service              ok
  health     :8080/health                 200

  installed in 14s
```

A few weeks later:

```console
$ sudo werft update notepad
  git        fetch                        v1.5.0

  v1.4.2 → v1.5.0    9 commits

  check      requirements                 ok
  release    releases/v1.5.0              ok
  step       python3 -m venv .venv        ok
  step       Packages                     ok
  backup     /etc/notepad                 ok
  config     /etc/notepad/.env            ok
  migration  003_tags.sh                  ok
  restart    notepad.service              ok
  health     :8080/health                 200

  done in 21s
```

If the new version does not answer, werft puts the old one back, configuration included.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/vxnsin/werft/main/install.sh | sudo sh
```

The script downloads the matching binary from the releases (amd64 or arm64), checks the checksum and puts it into `/usr/local/bin`. With Go installed, `go install github.com/vxnsin/werft/cmd/werft@latest` works too.

On the server werft needs git, systemd for services and automatic updates, and Docker for compose projects.

## Preparing a project

`werft init` writes a commented `werft.yml` into the project. For a small Python app something like this is enough:

```yaml
schema: 1
name: notepad

questions:
  - id: port
    prompt: Which port should notepad listen on?
    type: port
    default: 8080

config:
  env_file: /etc/notepad/.env

shared: [data]

install:
  - python3 -m venv .venv
  - .venv/bin/pip install -r requirements.txt

update:
  backup:
    paths: [/etc/notepad]

runtime:
  type: systemd
  systemd:
    exec: .venv/bin/gunicorn --bind 0.0.0.0:{{ .port }} notepad:app
    user: notepad

health:
  http: http://localhost:{{ .port }}/health
```

Tag a release (`git tag v1.0.0 && git push --tags`) and the project can be installed. `werft validate` checks the file beforehand, in CI as well.

Every field is described in [docs/werft-yml.md](docs/werft-yml.md), two complete examples are in [examples/](examples/). There is a [JSON schema](schema/werft.schema.json) for completion in VS Code, and [ai-skills/werft](ai-skills/werft/SKILL.md) teaches coding agents how to add werft to a project.

## Commands

```
werft install <git-url> [--ref v1.2.0] [--answers file.yml]
werft update <name> | --all [--check] [--ref r] [--policy patch|minor|major]
werft rollback <name> [--to version]
werft config <name> [--set id=value]
werft list
werft status <name>
werft start | stop | restart <name>
werft logs <name> [-f]
werft backups <name>
werft uninstall <name> [--keep-data]
werft auto [set <name...> --at 02:00 | off <name...> | list]
werft track <dir> [--after cmd] [--restart unit]
werft untrack <name>
werft init
werft validate [file]
```

`-y` answers confirmations with yes, `-v` shows the output of build steps live. Without `-v` it goes into a log file and only shows up when something fails.

## What happens during an update

1. `git fetch`, then look for the newest tag. With `channel: branch` the newest commit counts instead.
2. If a command in `werft.yml` changed (build step, hook, start command), werft shows the change and asks. Automatic updates stop here.
3. New questions are asked, old answers are kept.
4. The new release is unpacked into its own directory and built. The running version does not notice any of it.
5. Backup, write the configuration, run migrations.
6. `current` now points to the new release, the service restarts, the health check runs.
7. If any of that fails, everything goes back to the previous version.

## Updating on a schedule

Which apps update on their own, and when, is decided on the server, not in the project:

```console
$ sudo werft auto
   1) notepad              off
   2) wiki                 off
   3) dotfiles             off

  Which apps? Numbers like 1,3 or all: all
  When? A time like 02:00, a systemd calendar like Mon..Fri 04:00, or off [02:00]:
  How far may releases jump on their own? patch, minor or major [patch]:

  auto       notepad                      *-*-* 02:00 (patch)
  auto       wiki                         *-*-* 02:00 (patch)
  auto       dotfiles                     *-*-* 02:00
```

Without questions: `werft auto set notepad wiki --at 02:00`, `werft auto off all`, `werft auto list`. Every app gets its own systemd timer. The policy limits how far a release may jump on its own: `patch` goes from 1.4.2 to 1.4.3 but never to 1.5.0. An automatic update stops when commands in `werft.yml` changed and leaves a note in the journal instead.

## Plain git clones

Not every repository needs a `werft.yml`. A clone that only has to stay current can be tracked as well:

```sh
sudo werft track /home/luis/dotfiles
sudo werft track /opt/bot --after "npm ci" --restart bot
```

werft then fetches, fast-forwards the branch, runs the `--after` command and restarts the unit. `update`, `rollback`, `status` and `auto` work the same as for everything else. Local changes in the clone stop an update, and if the after command fails, the clone goes back to the commit before.

## Where things live

```
/opt/werft/notepad/
├── current -> releases/v1.5.0
├── releases/
│   ├── v1.4.2/
│   └── v1.5.0/
└── shared/data/            survives updates

/var/lib/werft/apps/notepad/
├── state.json              version, history, answers
├── secrets.json            passwords, readable by root only
├── repo.git/               mirror of the repository
├── backups/
└── logs/                   output of every run
```

Private repositories work as soon as root can clone them, through a deploy key in `/root/.ssh` or a credential helper.

## Status

Early version. The tests run install, update, automatic rollback, manual rollback and uninstall against a real git repository. The systemd and docker compose parts have not seen many real servers yet.

Left out on purpose: a rollback does not reverse migrations, and the only target system is Linux.

Next up:
- a message after automatic updates, through a webhook
- firewall rules from `requires.ports`
- restoring a backup with one command
- several servers from one machine

## License

MIT
