#!/usr/bin/with-contenv bashio
# parameter.json lives in /config/ism7 (mapped "config") so it can be edited; it is generated on first start.
mkdir -p /config/ism7

# broker: manual options win, otherwise the Supervisor's MQTT service (Mosquitto app)
if bashio::config.has_value 'mqtt_host'; then
  host="$(bashio::config 'mqtt_host')"
  port="$(bashio::config 'mqtt_port')"
  user="$(bashio::config 'mqtt_user')"
  pass="$(bashio::config 'mqtt_password')"
elif bashio::services.available mqtt; then
  host="$(bashio::services mqtt host)"
  port="$(bashio::services mqtt port)"
  user="$(bashio::services mqtt username)"
  pass="$(bashio::services mqtt password)"
else
  bashio::exit.nok "No MQTT broker: set mqtt_host in the app options or install the Mosquitto app"
fi

args=(run
  -i "$(bashio::config 'ism7_ip')"
  -p "$(bashio::config 'ism7_password')"
  --mqtt-host "$host"
  --mqtt-port "${port:-1883}"
  -t /config/ism7/parameter.json
  --hass-id "$(bashio::config 'hass_id')"
  --interval "$(bashio::config 'interval')"
  --log-format text)
[ -n "$user" ] && args+=(--mqtt-user "$user")
[ -n "$pass" ] && args+=(--mqtt-pass "$pass")
# never use --debug here: it dumps the ISM7 password
bashio::config.true 'debug_logging' && args+=(--level debug)

exec ism7 "${args[@]}"
