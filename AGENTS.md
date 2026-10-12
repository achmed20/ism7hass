# AGENTS.md - Coding Agent Guidelines

## Project Overview

Go port of the .NET `ism7mqtt` (~/code/ism7mqtt): talks to a Wolf ISM7 heat pump gateway over TLS (port 9092/9091,
6 byte frame header + XML) and bridges all values to MQTT, including writes via `<topic>/set`.
Cobra CLI + Viper config, `log/slog` logging, stdlib otherwise.

## Commands

```sh
go build ./...
go vet ./... && gofmt -l .
go test -race ./...
go test -v -run TestClient ./internal/ism7
go run . config -i <ip> -p <pw>      # generate parameter.json
go run . run -i <ip> -p <pw> --mqtt-host <broker>
```

## Layout

```text
cmd/                 cobra commands: run, config, version; root.go + struct_config.go (viper)
internal/ism7/       protocol.go (frames/XML), conn.go (TLS), client.go (login/pull/push/write/keepalive),
                     config.go (devices, bundles, MQTT messages, write requests), convert.go (value converters),
                     templates.go (parse embedded resources/*.xml, parameter.json generation)
internal/mqtt/       paho bridge: publish values, parse /set messages
internal/ha/         Home Assistant discovery stub (enabled by homeassistant.id / --hass-id)
version/             build info (hardcoded in version/version.go)
```

## Style

- Imports: stdlib, then module (`ism7/...`), then external, blank-line separated. `gofmt`.
- Errors: wrap with `%w`, never swallow; no `panic` for expected errors.
- Logging: `log/slog` only. Tests: testify, `Test<Unit>_<Scenario>`, same package.
- Config: add key to `Config`/`SetDefaults` in `cmd/struct_config.go`, flag in the command, mapping in `flagKeys` (root.go).
- Keep behavior compatible with the .NET version (topics, JSON shape, env var names) unless noted in README.

## Do NOT

- Add dependencies without justification (current: cobra, viper, paho.mqtt.golang, testify).
- Commit real passwords/parameter.json of an installation.
- Use global state besides `config` and the slog default.
