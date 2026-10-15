package cmd

import (
	"github.com/spf13/viper"
)

type Config struct {
	Log struct {
		Format string
		Level  string
	}

	ISM7 struct {
		IP                      string
		Password                string
		Parameter               string
		ParameterXMLOverride    string
		Interval                int
		StartupTimeout          int
		RebootTimeout           int // minutes without values before the gateway is rebooted; 0 disables
		SerializePushSubscribes bool
		Debug                   bool
	}

	MQTT struct {
		Host     string
		Port     int
		Username string
		Password string
		QoS      int
		Separate bool
		Retain   bool
	}

	HomeAssistant struct {
		ID string // non-empty enables discovery
	}
}

// Set default values for viper
// these need to be defined (and used) in Viper style.
// cause viper will handle env, parameter and config values.
// key's are NOT case sensitive
func (c *Config) SetDefaults() {
	viper.SetDefault("log.format", "text")
	viper.SetDefault("log.level", "info")

	viper.SetDefault("ism7.parameter", "parameter.json")
	viper.SetDefault("ism7.interval", 60)
	viper.SetDefault("ism7.startuptimeout", 90)
	viper.SetDefault("ism7.reboottimeout", 5)

	viper.SetDefault("config.enabled", true)
	viper.SetDefault("mqtt.port", 1883)
	viper.SetDefault("mqtt.qos", 0)

	// env vars of the .NET version keep working
	for key, env := range map[string]string{
		"ism7.ip":                      "ISM7_IP",
		"ism7.password":                "ISM7_PASSWORD",
		"ism7.parameterxmloverride":    "ISM7_PARAMETER_XML_OVERRIDE",
		"ism7.interval":                "ISM7_INTERVAL",
		"ism7.startuptimeout":          "ISM7_STARTUP_TIMEOUT",
		"ism7.reboottimeout":           "ISM7_REBOOT_TIMEOUT",
		"ism7.serializepushsubscribes": "ISM7_SERIALIZE_PUSH_SUBSCRIBES",
		"ism7.debug":                   "ISM7_DEBUG",
		"mqtt.host":                    "ISM7_MQTTHOST",
		"mqtt.port":                    "ISM7_MQTTPORT",
		"mqtt.username":                "ISM7_MQTTUSERNAME",
		"mqtt.password":                "ISM7_MQTTPASSWORD",
		"mqtt.qos":                     "ISM7_MQTTQOS",
		"mqtt.separate":                "ISM7_SEPARATE",
		"mqtt.retain":                  "ISM7_RETAIN",
		"homeassistant.id":             "ISM7_HOMEASSISTANT_ID",
	} {
		_ = viper.BindEnv(key, env, replaceDots(key))
	}
}
