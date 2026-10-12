# ism7

Go port of [ism7mqtt](https://github.com/zivillian/ism7mqtt): reads a Wolf ISM7 gateway directly (no Smartset cloud)
and publishes all values to MQTT. Values can be written back via `<topic>/set`.

```sh
ism7 run -i <ism7 ip> -p <password> --mqtt-host <broker>
ism7 config -i <ism7 ip> -p <password>          # (re)generate parameter.json by hand
```

`run` generates `parameter.json` on first start if it does not exist (all parameters enabled, logged as a warning).
An existing file is never overwritten. Startup pulls all enabled parameters once (about 100 s for four devices), then
the ISM7 pushes updates every `--interval` seconds (default 60).

Settings come from flags, env vars (`ISM7_IP`, `ISM7_PASSWORD`, `ISM7_MQTTHOST`, ... same names as the .NET version,
or `MQTT_HOST` style), or `config.yaml`. See `ism7 run --help`.

## parameter.json

```json
{"Name": "DHK_BM-2", "ReadBusAddress": "0x35", "WriteBusAddress": "0x30", "DeviceTemplateId": 340000,
 "Parameter": [{"id": 340028, "name": "Sollwertkorrektur", "enabled": true}]}
```

- `enabled` (default true; missing = true) means the parameter is read, published and writable. Disabled ones are skipped
  entirely. `config --enabled=false` writes everything disabled so you can switch on just what you want.
- `name` is informational; bare ids are also accepted when reading. The file is not readable by the .NET version.
- `config` lists every parameter of the matching device template (the .NET original evaluates template conditions with
  live values, which is not possible here), so it is a superset. Parameters with the same name and the same telegrams
  are merged: one stays enabled (preferring ids from a real installation), the others are written as `"enabled": false`.
- A BM-2 also gets its heating circuit (`DHK_BM-2`) and hot water (`WWSystem_BM-2`) devices on the same bus. This rule
  comes from one installation; other sub-devices (e.g. mixer circuits) are not generated and can be added by hand.

## Topics

- `Wolf/<ip>/<device>_<busaddr>`: JSON object with the values of that update (default)
- `--separate`: `Wolf/<ip>/<device>_<busaddr>/<parameter>` per value. List parameters add `/value` (number) and `/text`
  (raw text, no JSON quotes). Parameters that still share a name get their id as suffix, e.g. `/Mo/320000`.

## Writing

- JSON: publish `{"<parameter>": value}` to `<device topic>/set` (list parameters also accept `{"value": n}` or
  `{"text": "Ein"}`; duplicate names use `{"<parameter>": {"<id>": value}}`).
- Plain value: publish to `<device topic>/set/<parameter>` (list parameters: `/set/<parameter>/value` or `/text`).
- Writes are validated: numbers must be within the descriptor's min/max, list values must be one of the options, and
  the value must fit the converter type. A bad value is rejected (logged as `invalid set message`) and nothing is sent;
  in a JSON message one bad value rejects the whole message. Only 16/32-bit numbers and BM2 date/time can be written.
- Each acknowledged write is logged: `set command acknowledged ... ack="DHK_BM-2/Sollwertkorrektur=2.5 ..."`.

## Home Assistant

`--hass-id <id>` (or `homeassistant.id`) publishes retained MQTT discovery messages to `homeassistant/<type>/.../config`
after startup and again when `homeassistant/status` receives a message. It implies `--separate` and `--retain`.

- Writable list parameters become `switch` (Ein/Aus, Aktiviert/Deaktiviert) or `select`, numbers become `number`
  (with min/max/step), everything else `sensor`/`binary_sensor`/`text`. Parameters whose converter cannot be written
  show as read-only sensors.
- Names and options stay German. Parameters set to `enabled: false` get no entity.
- Old retained configs stay on the broker; delete them manually when you remove or rename parameters.

## Differences to the .NET version

- No localization (`--lang` is not implemented).
- TLS uses Go's `crypto/tls`; the ISM7 certificate has a negative serial number, so `main.go` enables
  `//go:debug x509negativeserial=1`. Certificates are not verified. Go has no `TLS_RSA_WITH_AES_256_CBC_SHA256`; if your
  device only accepts that suite the handshake fails.
- The ISM7 only accepts one connection: Smartset (or a second bridge) cannot connect while this runs.

## Home Assistant app (local)

`addon/` is a local app. Run `addon/build.sh` (copies the Go source into `addon/src/`), copy `addon/` to
`/addons/ism7` on the Home Assistant host, then install it from the local apps in the store; it is compiled on the host.
Options: `ism7_ip`, `ism7_password`, `interval`, `hass_id` (default `wolf`), `debug_logging`. The MQTT broker is either
`mqtt_host`/`mqtt_port`/`mqtt_user`/`mqtt_password` (if `mqtt_host` is set) or the Supervisor's MQTT service
(Mosquitto app). `parameter.json` is generated at `/config/ism7/parameter.json` on first start. Bump `version` in
`addon/config.yaml` when updating so Home Assistant rebuilds.
