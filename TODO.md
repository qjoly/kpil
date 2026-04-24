# TODO

## Features

- [ ] **Volume mount for AI file access** — add a `--workdir` flag that mounts a
  local directory into the container (e.g. `-v $PWD:/workspace`) so the AI can
  read and modify files on the host machine.  Should be read-write by default
  with an optional `--workdir-readonly` flag to restrict to read-only access.

- [ ] **Interactive setup mode (`-i`)** — optional interactive prompt before
  launching the container that lets the user configure runtime parameters:
  volume mounts, image override, network mode, and an optional entrypoint
  command.  Non-interactive (current) behaviour is preserved when `-i` is not
  passed.
