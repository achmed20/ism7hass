package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"ism7/version"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var config Config

// flagKeys maps command flags to viper keys; bound per executed command (a viper key can only be bound to one flag).
var flagKeys = map[string]string{
	"ip": "ism7.ip", "password": "ism7.password", "parameter": "ism7.parameter",
	"parameter-xml-override": "ism7.parameterxmloverride", "interval": "ism7.interval",
	"startup-timeout": "ism7.startuptimeout", "serialize-push-subscribes": "ism7.serializepushsubscribes",
	"debug": "ism7.debug", "mqtt-host": "mqtt.host", "mqtt-port": "mqtt.port", "mqtt-user": "mqtt.username",
	"mqtt-pass": "mqtt.password", "mqtt-qos": "mqtt.qos", "separate": "mqtt.separate", "retain": "mqtt.retain",
	"hass-id": "homeassistant.id", "enabled": "config.enabled",
	"target": "ism7.parameter", "log-format": "log.format", "level": "log.level",
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "ism7",
	Short: "Wolf ISM7 to MQTT bridge",
	Long: `ism7 (v` + version.VERSION + `) reads a Wolf ISM7 heat pump gateway and
publishes all values to MQTT; values can be written back via <topic>/set.
	- bridge (run)
	- parameter.json generator (config)
`,

	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if key, ok := flagKeys[f.Name]; ok {
				_ = viper.BindPFlag(key, f)
			}
		})
		if err := viper.Unmarshal(&config); err != nil {
			return fmt.Errorf("unable to decode config: %w", err)
		}
		setupLogger()
		return nil
	},
}

// Execute adds all child commands to the root command sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(-1)
	}
}

func init() {
	initConfig()

	rootCmd.PersistentFlags().String("log-format", "", "log format (json, text)")
	rootCmd.PersistentFlags().String("level", "", "log level (debug, info, warn, error)")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	config.SetDefaults()
	viper.SetConfigName("config") // no need to include file extension
	viper.AddConfigPath(".")      // set the path of your config file
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	_ = viper.ReadInConfig()
}

func replaceDots(s string) string { return strings.ReplaceAll(strings.ToUpper(s), ".", "_") }

func setupLogger() {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(config.Log.Level)); err != nil {
		lvl = slog.LevelInfo
	}
	if config.ISM7.Debug {
		lvl = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler = slog.NewTextHandler(os.Stderr, opts)
	if config.Log.Format == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}
