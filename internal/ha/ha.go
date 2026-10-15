// Package ha publishes Home Assistant MQTT discovery messages (port of HaDiscovery.cs).
// Enabled by homeassistant.id (--hass-id); that forces separate topics + retain.
package ha

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"ism7/internal/ism7"
)

const StatusTopic = "homeassistant/status"

// Discovery publishes the discovery config messages.
type Discovery struct {
	ID  string
	IP  string
	Cfg *ism7.Config
	// Publish sends a retained message.
	Send func(topic, payload string)
}

// Publish publishes discovery info for all devices/parameters.
func (d *Discovery) Publish() {
	n := 0
	for _, m := range d.Messages() {
		b, _ := json.Marshal(m.Payload)
		d.Send(m.Topic, string(b))
		n++
	}
	slog.Info("home assistant discovery published", "entities", n)
}

// Cleanup clears (empty retained payload) every given discovery topic of this id that is no longer published.
func (d *Discovery) Cleanup(retained []string) {
	current := map[string]bool{}
	for _, m := range d.Messages() {
		current[m.Topic] = true
	}
	prefix := laundry(d.ID + "_")
	n := 0
	for _, t := range retained {
		parts := strings.Split(t, "/")
		if len(parts) != 4 || parts[0] != "homeassistant" || parts[3] != "config" ||
			!strings.HasPrefix(parts[2], prefix) || current[t] {
			continue
		}
		d.Send(t, "")
		n++
	}
	slog.Info("home assistant stale entities removed", "entities", n)
}

type Message struct {
	Topic   string
	Payload map[string]any
}

var (
	umlauts = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "Ä", "Ae", "Ö", "Oe", "Ü", "Ue", "ß", "ss", " ", "_")
	illegal = regexp.MustCompile(`[^a-zA-Z0-9_ ]+`)
)

func laundry(s string) string { return illegal.ReplaceAllString(umlauts.Replace(s), "") }

func entityType(p ism7.ParameterDef, writable bool) string {
	switch p.Kind {
	case "ListParameterDescriptor":
		switch {
		case writable && p.IsBoolean():
			return "switch"
		case writable:
			return "select"
		case p.IsBoolean():
			return "binary_sensor"
		}
		return "sensor"
	case "NumericParameterDescriptor":
		if writable {
			return "number"
		}
		return "sensor"
	case "TextParameterDescriptor", "OtherParameterDescriptor":
		if writable {
			return "text"
		}
		return "sensor"
	}
	return ""
}

func (d *Discovery) Messages() []Message {
	var out []Message
	for _, dev := range d.Cfg.Devices() {
		for _, p := range dev.Params {
			def := p.Def
			typ := entityType(def, p.Writable)
			if typ == "" || def.ControlType == "DaySwitchTimes" || strings.Contains(def.ControlType, "NO_DISPLAY") {
				continue
			}
			uid := laundry(fmt.Sprintf("%s_%s_%s_%d_%s", d.ID, dev.Name, dev.WriteBus, def.PTID, def.Name))
			suffix := ""
			if def.IsList() {
				suffix = "/text"
			}
			dedup := ""
			if p.Duplicate {
				dedup = "/" + strconv.Itoa(def.PTID)
			}
			m := map[string]any{
				"unique_id":         uid,
				"state_topic":       dev.Topic + "/" + p.MQTTName + dedup + suffix,
				"name":              def.Name,
				"default_entity_id": typ + "." + uid,
			}
			if p.Writable {
				m["command_topic"] = dev.Topic + "/set/" + p.MQTTName + dedup + suffix
			}
			for k, v := range properties(def, p.Writable) {
				m[k] = v
			}
			if _, ok := m["icon"]; !ok {
				if icon := guessIcon(def.Name); icon != "" {
					m["icon"] = icon
				}
			}
			m["device"] = map[string]any{
				"configuration_url": "http://" + d.IP + "/",
				"manufacturer":      "Wolf",
				"model":             dev.Name,
				"name":              d.ID + " " + dev.Name,
				"connections":       [][]string{{"ip_dev", d.IP + "_" + dev.Name}},
			}
			out = append(out, Message{"homeassistant/" + typ + "/" + uid + "/config", m})
		}
	}
	return out
}

func guessIcon(name string) string {
	n := strings.ToLower(name)
	icon := ""
	switch {
	case strings.Contains(n, "brenner"):
		icon = "mdi:fire"
	case strings.Contains(n, "solar"):
		icon = "mdi:solar-panel"
	case strings.Contains(n, "ventil"):
		icon = "mdi:pipe-valve"
	case strings.Contains(n, "heizung"):
		icon = "mdi:radiator"
	case strings.Contains(n, "pumpe"):
		icon = "mdi:pump"
	}
	if strings.Contains(n, "druck") {
		icon = "mdi:gauge"
	}
	return icon
}

func properties(p ism7.ParameterDef, writable bool) map[string]any {
	m := map[string]any{}
	switch p.Kind {
	case "NumericParameterDescriptor":
		if writable {
			for k, s := range map[string]string{"min": p.Min, "max": p.Max, "step": p.Step} {
				if f, err := strconv.ParseFloat(s, 64); err == nil {
					m[k] = f
				}
			}
		}
		if u := p.UnitName; u != "" {
			m["unit_of_measurement"] = u
			switch u {
			case "°C":
				m["icon"], m["state_class"] = "mdi:thermometer", "measurement"
			case "%", "l/min", "L/min", "U/min":
				m["state_class"] = "measurement"
			case "kWh":
				m["state_class"], m["device_class"] = "total_increasing", "energy"
			case "W", "kW":
				m["state_class"], m["device_class"] = "measurement", "power"
			case "Hz":
				m["state_class"], m["device_class"] = "measurement", "frequency"
			}
		}
	case "ListParameterDescriptor":
		if p.IsBoolean() {
			for _, o := range p.Options() {
				switch o[1] {
				case "Ein", "Aktiviert":
					m["payload_on"] = o[1]
				case "Aus", "Deaktiviert":
					m["payload_off"] = o[1]
				}
			}
		} else {
			opts := []string{}
			for _, o := range p.Options() {
				opts = append(opts, o[1])
			}
			m["options"], m["device_class"] = opts, "enum"
		}
	}
	return m
}
