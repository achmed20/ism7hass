package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"ism7/internal/ha"
	"ism7/internal/ism7"
	"ism7/internal/mqtt"

	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the ISM7 to MQTT bridge",
	Long:  `Connects to the ISM7 and the MQTT broker, publishes all values and applies <topic>/set messages.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		c := config
		if c.ISM7.IP == "" || c.ISM7.Password == "" || c.MQTT.Host == "" {
			return errors.New("ism7.ip, ism7.password and mqtt.host are required (flags --ip, --password, --mqtt-host)")
		}
		haEnabled := c.HomeAssistant.ID != ""
		if haEnabled { // like the .NET version: discovery needs one retained topic per value
			c.MQTT.Retain, c.MQTT.Separate = true, true
		}

		tpl, err := ism7.LoadTemplates(c.ISM7.ParameterXMLOverride)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		var cf *ism7.ConfigFile
		if _, err := os.Stat(c.ISM7.Parameter); errors.Is(err, os.ErrNotExist) {
			slog.Warn("parameter file missing, generating it automatically", "file", c.ISM7.Parameter)
			if cf, err = ism7.GenerateConfigFile(ctx, tpl, c.ISM7.IP, c.ISM7.Password, c.ISM7.Parameter, true); err != nil {
				return fmt.Errorf("generate %s: %w", c.ISM7.Parameter, err)
			}
		} else if err != nil {
			return err
		} else if cf, err = ism7.LoadConfigFile(c.ISM7.Parameter); err != nil {
			return err
		}
		cfg, err := ism7.NewConfig(cf, tpl, c.ISM7.IP)
		if err != nil {
			return err
		}

		var current atomic.Pointer[ism7.Client] // set only while initialized, so writes never hit a half-started session
		mo := mqtt.Options{
			Host: c.MQTT.Host, Port: c.MQTT.Port, Username: c.MQTT.Username, Password: c.MQTT.Password,
			ClientID: "Wolf_" + strings.ReplaceAll(c.ISM7.IP, ".", ""),
			QoS:      byte(c.MQTT.QoS), Retain: c.MQTT.Retain, Separate: c.MQTT.Separate, IP: c.ISM7.IP,
		}
		var disc *ha.Discovery
		if haEnabled {
			disc = &ha.Discovery{ID: c.HomeAssistant.ID, IP: c.ISM7.IP, Cfg: cfg}
			mo.HAStatusTopic = ha.StatusTopic
		}
		bridge := mqtt.New(mo, cfg)
		bridge.Send = func(w []ism7.InfoWrite) error {
			cl := current.Load()
			if cl == nil {
				return errors.New("not connected to ISM7")
			}
			return cl.Write(w)
		}
		if disc != nil {
			disc.Send = func(t, p string) { bridge.PublishRaw(t, p, true) }
			bridge.OnHAStatus = disc.Publish
		}
		if err := bridge.Connect(); err != nil {
			return fmt.Errorf("mqtt connect: %w", err)
		}
		defer bridge.Close()

		cleaned := false
		var lastData atomic.Int64
		lastData.Store(time.Now().UnixNano())
		if c.ISM7.RebootTimeout > 0 {
			// a healthy gateway must be able to deliver at least a few push cycles within the timeout
			timeout := max(time.Duration(c.ISM7.RebootTimeout)*time.Minute, 3*time.Duration(c.ISM7.Interval)*time.Second)
			go watchdog(ctx, &lastData, timeout, c.ISM7.IP, c.ISM7.Password)
		}

		port := cf.TcpPort
		for ctx.Err() == nil {
			cl := ism7.NewClient(ism7.Options{
				Host: c.ISM7.IP, Port: port, Password: c.ISM7.Password, Interval: c.ISM7.Interval,
				StartupTimeout:      time.Duration(c.ISM7.StartupTimeout) * time.Second,
				SerializeSubscribes: c.ISM7.SerializePushSubscribes, Debug: c.ISM7.Debug,
			}, cfg)
			cl.OnData = func() {
				lastData.Store(time.Now().UnixNano())
				bridge.Publish()
			}
			cl.OnInitialized = func() {
				current.Store(cl)
				if disc != nil {
					if !cleaned {
						cleaned = true
						disc.Cleanup(bridge.Retained("homeassistant/+/+/config", 2*time.Second))
					}
					disc.Publish()
				}
				slog.Info("ism7 initialized")
			}
			err := cl.Run(ctx)
			current.Store(nil)
			if ctx.Err() != nil {
				break
			}
			slog.Error("ism7 connection ended, retrying in 10s", "error", err)
			select {
			case <-ctx.Done():
			case <-time.After(10 * time.Second):
			}
		}
		return nil
	},
}

func stale(last, now time.Time, timeout time.Duration) bool { return now.Sub(last) >= timeout }

// watchdog reboots the gateway when no values arrived for timeout; the reboot restarts the clock (grace period).
func watchdog(ctx context.Context, lastData *atomic.Int64, timeout time.Duration, ip, password string) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !stale(time.Unix(0, lastData.Load()), time.Now(), timeout) {
			continue
		}
		slog.Warn("no values received, rebooting ism7", "timeout", timeout)
		if err := ism7.Reboot(ctx, "http://"+ip, password); err != nil {
			slog.Error("ism7 reboot failed", "error", err)
		}
		lastData.Store(time.Now().UnixNano())
	}
}

func init() {
	rootCmd.AddCommand(runCmd)
	f := runCmd.Flags()
	f.StringP("ip", "i", "", "ISM7 hostname or IP address")
	f.StringP("password", "p", "", "ISM7 password")
	f.StringP("parameter", "t", "", "path to parameter.json (default parameter.json)")
	f.String("parameter-xml-override", "", "ADVANCED: replace the built-in parameter.xml")
	f.Int("interval", 0, "push interval in seconds (default 60)")
	f.Int("startup-timeout", 0, "seconds to wait for each response during startup (default 90)")
	f.Int("reboot-timeout", 0, "reboot the ISM7 after this many minutes without values, 0 disables (default 5)")
	f.Bool("serialize-push-subscribes", false, "send push subscribes one at a time (some FW 5.20 devices)")
	f.BoolP("debug", "d", false, "dump raw protocol XML (includes your password!)")
	f.String("mqtt-host", "", "MQTT broker host")
	f.Int("mqtt-port", 0, "MQTT broker port (default 1883)")
	f.String("mqtt-user", "", "MQTT username")
	f.String("mqtt-pass", "", "MQTT password")
	f.Int("mqtt-qos", 0, "MQTT QoS 0-2")
	f.BoolP("separate", "s", false, "one topic per value instead of one JSON payload per device")
	f.Bool("retain", false, "set the MQTT retain flag")
	f.String("hass-id", "", "enable Home Assistant discovery with this id (implies --separate and --retain)")
}
