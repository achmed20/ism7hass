package ism7

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ConfigFile is parameter.json (same format as the .NET ism7config produces).
type ConfigFile struct {
	TcpPort uint16         `json:"TcpPort"`
	Devices []ConfigDevice `json:"Devices"`
}

type ConfigDevice struct {
	Name             string            `json:"Name,omitempty"` // informational
	ReadBusAddress   string            `json:"ReadBusAddress"`
	WriteBusAddress  *string           `json:"WriteBusAddress"`
	DeviceTemplateID int               `json:"DeviceTemplateId"`
	Parameter        []ConfigParameter `json:"Parameter"`
}

// ConfigParameter is a parameter id; Name is informational only. Also accepts a bare number when reading.
type ConfigParameter struct {
	ID   int    `json:"id"`
	Name string `json:"name,omitempty"`
	// Enabled: nil (missing) counts as enabled.
	Enabled *bool `json:"enabled,omitempty"`
}

func (p ConfigParameter) On() bool { return p.Enabled == nil || *p.Enabled }

func (p *ConfigParameter) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &p.ID); err == nil {
		return nil
	}
	type plain ConfigParameter
	return json.Unmarshal(b, (*plain)(p))
}

func LoadConfigFile(path string) (*ConfigFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cf := ConfigFile{TcpPort: 9092}
	if err := json.Unmarshal(b, &cf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cf, nil
}

// Message is one MQTT message to publish.
type Message struct {
	Topic   string
	Payload string
}

// Bundle is a group of info reads that is requested together.
type Bundle struct {
	ID    string
	Bus   string
	Reads []InfoRead
}

const maxBundleReads = 20

// Config holds the running devices. All methods are safe for concurrent use.
type Config struct {
	mu      sync.Mutex
	devices []*device
	byBus   map[byte][]*device
	ids     idGen
}

type idGen struct{ bundle, seq int }

func (g *idGen) nextBundle() string { g.bundle++; return strconv.Itoa(g.bundle) }
func (g *idGen) nextSeq() string    { g.seq++; return "A;" + strconv.Itoa(g.seq+1) }

type device struct {
	name, topic, readBus, writeBus string
	params                         []*param
}

type param struct {
	def       ParameterDef
	conv      *Converter
	duplicate bool
}

// writable: the descriptor allows it and the converter can encode it.
func (p *param) writable() bool { return p.def.Writable() && p.conv.CanWrite() }

// write validates value against the descriptor (min/max, list options) and encodes it.
func (p *param) write(v string) ([]InfoWrite, error) {
	v = strings.TrimSpace(v)
	switch {
	case p.def.IsList():
		if opts := p.def.Options(); len(opts) > 0 {
			ok := false
			for _, o := range opts {
				ok = ok || o[0] == v
			}
			if !ok {
				return nil, fmt.Errorf("%q is not a valid option for %q", v, p.def.Name)
			}
		}
	case p.def.Kind == "NumericParameterDescriptor":
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			break // converter reports the parse error
		}
		// bounds that are condition ids (C_123) do not parse and are not enforced
		if lo, err := strconv.ParseFloat(p.def.Min, 64); err == nil && f < lo {
			return nil, fmt.Errorf("%v is below the minimum %v of %q", f, lo, p.def.Name)
		}
		if hi, err := strconv.ParseFloat(p.def.Max, 64); err == nil && f > hi {
			return nil, fmt.Errorf("%v is above the maximum %v of %q", f, hi, p.def.Name)
		}
	}
	return p.conv.Write(v)
}

func (p *param) mqttName() string { return mqttEscape(p.def.Name) }

var escaper = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "Ä", "Ae", "Ö", "Oe", "Ü", "Ue", "ß", "ss",
	"/", "_", " ", "_", "+", "_", "#", "_")

func mqttEscape(s string) string { return escaper.Replace(s) }

// NewConfig builds the running devices for the given parameter.json.
func NewConfig(cf *ConfigFile, t *Templates, ip string) (*Config, error) {
	c := &Config{byBus: map[byte][]*device{}}
	for _, cd := range cf.Devices {
		var dt *DeviceDef
		for i := range t.Devices {
			if t.Devices[i].DTID == cd.DeviceTemplateID {
				dt = &t.Devices[i]
			}
		}
		if dt == nil {
			return nil, fmt.Errorf("unknown device template %d", cd.DeviceTemplateID)
		}
		rb, err := ParseHex(cd.ReadBusAddress)
		if err != nil {
			return nil, err
		}
		wb := fmt.Sprintf("0x%02X", rb-5)
		if cd.WriteBusAddress != nil {
			wb = *cd.WriteBusAddress
		}
		d := &device{name: dt.Name, readBus: cd.ReadBusAddress, writeBus: wb,
			topic: fmt.Sprintf("Wolf/%s/%s_%s", ip, dt.Name, cd.ReadBusAddress)}
		ids := map[int]bool{}
		for _, id := range cd.Parameter {
			if id.On() {
				ids[id.ID] = true
			}
		}
		for _, pd := range t.Parameters {
			if !ids[pd.PTID] {
				continue
			}
			for _, cv := range t.Converters {
				if cv.CTID == pd.PTID {
					d.params = append(d.params, &param{def: pd, conv: NewConverter(cv)})
					break
				}
			}
		}
		names := map[string]int{}
		for _, p := range d.params {
			names[p.def.Name]++
		}
		for _, p := range d.params {
			p.duplicate = names[p.def.Name] > 1
		}
		c.devices = append(c.devices, d)
		c.byBus[rb] = append(c.byBus[rb], d)
	}
	return c, nil
}

// BusAddresses returns the distinct read bus addresses.
func (c *Config) BusAddresses() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range c.devices {
		if !seen[d.readBus] {
			seen[d.readBus] = true
			out = append(out, d.readBus)
		}
	}
	return out
}

// Bundles splits the reads of all devices on a bus into bundles of at most 20 reads,
// keeping all reads of one parameter together.
func (c *Config) Bundles(bus string) ([]Bundle, error) {
	rb, err := ParseHex(bus)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var params []*param
	for _, d := range c.byBus[rb] {
		for _, p := range d.params {
			if len(p.conv.tel) > 0 {
				params = append(params, p)
			}
		}
	}
	sort.SliceStable(params, func(i, j int) bool { return len(params[i].conv.tel) > len(params[j].conv.tel) })
	var bundles []Bundle
	for _, p := range params {
		reads := p.conv.InfoReads()
		placed := false
		for i := range bundles {
			if len(bundles[i].Reads) <= maxBundleReads-len(reads) {
				bundles[i].Reads = append(bundles[i].Reads, reads...)
				placed = true
				break
			}
		}
		if !placed {
			bundles = append(bundles, Bundle{Bus: bus, Reads: reads})
		}
	}
	for i := range bundles {
		bundles[i].ID = c.ids.nextBundle()
	}
	return bundles, nil
}

// NewBundleID returns a fresh bundle id.
func (c *Config) NewBundleID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ids.nextBundle()
}

// Prepare returns a copy of the bundle's reads with bus address and sequence numbers set.
// interval < 0 omits the push interval (pull).
func (c *Config) Prepare(b Bundle, interval int) []InfoRead {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]InfoRead, len(b.Reads))
	for i, r := range b.Reads {
		r.Bus, r.Seq = b.Bus, c.ids.nextSeq()
		if interval >= 0 {
			r.Interval = &interval
		}
		out[i] = r
	}
	return out
}

// Process feeds telegrams into the converters and reports whether any device has a value.
func (c *Config) Process(ts []Telegram, write bool) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range ts {
		if t.State != "OK" {
			continue
		}
		bus, err := ParseHex(t.Bus)
		if err != nil {
			return false, err
		}
		low, err := ParseHex(t.DBLow)
		if err != nil {
			return false, err
		}
		high, err := ParseHex(t.DBHigh)
		if err != nil {
			return false, err
		}
		for _, d := range c.devices {
			if write {
				wb, err := ParseHex(d.writeBus)
				if err != nil || wb != bus {
					continue
				}
			} else if rb, _ := ParseHex(d.readBus); rb != bus {
				continue
			}
			for _, p := range d.params {
				if s := t.ServiceNr(); s != nil {
					if write && p.conv.def.ServiceWrite != 0 && p.conv.def.ServiceWrite != *s {
						continue
					}
					if !write && (p.conv.def.ServiceRead == nil || *p.conv.def.ServiceRead != *s) {
						continue
					}
				}
				p.conv.Add(t.Number, low, high)
			}
		}
	}
	for _, d := range c.devices {
		for _, p := range d.params {
			if p.conv.HasValue() {
				return true, nil
			}
		}
	}
	return false, nil
}

// Messages consumes all pending values. With separate=false one JSON object per device is returned,
// otherwise one message per value.
func (c *Config) Messages(separate bool) []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Message
	for _, d := range c.devices {
		obj := map[string]any{}
		for _, p := range d.params {
			if !p.conv.HasValue() {
				continue
			}
			v, err := p.conv.Value()
			if err != nil {
				logErr(err)
				continue
			}
			if separate {
				out = append(out, p.singleMessages(d.topic, v)...)
				continue
			}
			jv := p.jsonValue(v)
			if p.duplicate {
				m, _ := obj[p.def.Name].(map[string]any)
				if m == nil {
					m = map[string]any{}
				}
				m[strconv.Itoa(p.def.PTID)] = jv
				jv = m
			}
			obj[p.def.Name] = jv
		}
		if !separate && len(obj) > 0 {
			b, _ := json.Marshal(obj)
			out = append(out, Message{d.topic, string(b)})
		}
	}
	return out
}

func logErr(err error) { slog.Error("convert value", "error", err) }

func (p *param) optionText(v any) (string, bool) {
	key := fmt.Sprint(v)
	for _, o := range p.def.Options() {
		if o[0] == key && o[1] != "" {
			return o[1], true
		}
	}
	return "", false
}

func (p *param) jsonValue(v any) any {
	if p.def.IsList() {
		if text, ok := p.optionText(v); ok {
			return map[string]any{"value": v, "text": text}
		}
	}
	return v
}

func (p *param) singleMessages(topic string, v any) []Message {
	base := topic + "/" + p.mqttName()
	if p.duplicate {
		base += "/" + strconv.Itoa(p.def.PTID)
	}
	b, _ := json.Marshal(v)
	if p.def.IsList() {
		if text, ok := p.optionText(v); ok {
			// text is published raw (no JSON quotes) so Home Assistant can match payload_on/off and options
			return []Message{{base + "/value", string(b)}, {base + "/text", text}}
		}
	}
	return []Message{{base, string(b)}}
}

// WriteFromParts handles a "<device topic>/set/<parts...>" message with a plain value.
func (c *Config) WriteFromParts(topic string, parts []string, value string) ([]InfoWrite, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.deviceByTopic(topic)
	if d == nil {
		return nil, fmt.Errorf("unknown device topic %q", topic)
	}
	var out []InfoWrite
	for _, p := range d.params {
		if !p.writable() || len(parts) == 0 || p.mqttName() != parts[0] {
			continue
		}
		rest := parts[1:]
		if p.duplicate {
			if len(rest) == 0 || rest[0] != strconv.Itoa(p.def.PTID) {
				continue
			}
			rest = rest[1:]
		}
		v := value
		if p.def.IsList() {
			if len(rest) == 0 {
				continue
			}
			switch rest[0] {
			case "text":
				k, ok := p.keyForText(value)
				if !ok {
					return nil, fmt.Errorf("unknown option %q for %q", value, p.def.Name)
				}
				v = k
			case "value":
			default:
				continue
			}
		}
		w, err := p.write(v)
		if err != nil {
			return nil, err
		}
		out = append(out, w...)
	}
	return d.addr(out), nil
}

// WriteFromJSON handles a "<device topic>/set" message with a JSON object {"name": value}.
func (c *Config) WriteFromJSON(topic string, data []byte) ([]InfoWrite, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("invalid json payload: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.deviceByTopic(topic)
	if d == nil {
		return nil, fmt.Errorf("unknown device topic %q", topic)
	}
	var out []InfoWrite
	for name, raw := range obj {
		for _, p := range d.params {
			if !p.writable() || p.def.Name != name {
				continue
			}
			v, ok := p.jsonWriteValue(raw)
			if !ok {
				continue
			}
			w, err := p.write(v)
			if err != nil {
				return nil, err
			}
			out = append(out, w...)
		}
	}
	return d.addr(out), nil
}

func scalar(raw json.RawMessage) (string, bool) {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return "", false
	}
	switch x := v.(type) {
	case string:
		return x, true
	case float64, bool:
		return fmt.Sprint(x), true
	}
	return "", false
}

func (p *param) jsonWriteValue(raw json.RawMessage) (string, bool) {
	if p.duplicate {
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			return "", false
		}
		if raw, _ = m[strconv.Itoa(p.def.PTID)]; raw == nil {
			return "", false
		}
	}
	if p.def.IsList() && len(p.def.Options()) > 0 {
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) == nil {
			if v, ok := m["value"]; ok {
				return scalar(v)
			}
			if t, ok := m["text"]; ok {
				s, _ := scalar(t)
				return p.keyForText(s)
			}
			return "", false
		}
	}
	return scalar(raw)
}

func (p *param) keyForText(text string) (string, bool) {
	for _, o := range p.def.Options() {
		if o[1] == text {
			return o[0], true
		}
	}
	return "", false
}

func (c *Config) deviceByTopic(topic string) *device {
	for _, d := range c.devices {
		if d.topic == topic {
			return d
		}
	}
	return nil
}

func (d *device) addr(w []InfoWrite) []InfoWrite {
	for i := range w {
		w[i].Bus, w[i].Seq = d.writeBus, ""
	}
	return w
}

// Topics returns the device topics (used for subscriptions/diagnostics).
func (c *Config) Topics() []string {
	var out []string
	for _, d := range c.devices {
		out = append(out, d.topic)
	}
	return out
}

// DeviceInfo is a read-only view of a running device (used by Home Assistant discovery).
type DeviceInfo struct {
	Name, Topic, WriteBus string
	Params                []ParamInfo
}

type ParamInfo struct {
	Def       ParameterDef
	MQTTName  string
	Duplicate bool
	Writable  bool // descriptor allows writing and the converter can encode it
}

func (c *Config) Devices() []DeviceInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []DeviceInfo
	for _, d := range c.devices {
		di := DeviceInfo{Name: d.name, Topic: d.topic, WriteBus: d.writeBus}
		for _, p := range d.params {
			di.Params = append(di.Params, ParamInfo{Def: p.def, MQTTName: p.mqttName(), Duplicate: p.duplicate, Writable: p.writable()})
		}
		out = append(out, di)
	}
	return out
}

// DescribeWrites renders acknowledged write telegrams as "name=value (bus, telegram, raw bytes)".
// Parameters are matched by write bus and telegram; unmatched telegrams show raw bytes only.
func (c *Config) DescribeWrites(ts []Telegram) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, t := range ts {
		if t.State != "OK" {
			continue
		}
		low, _ := ParseHex(t.DBLow)
		high, _ := ParseHex(t.DBHigh)
		line := fmt.Sprintf("bus=%s telegram=%d dl=%s dh=%s", t.Bus, t.Number, t.DBLow, t.DBHigh)
		bus, _ := ParseHex(t.Bus)
	find:
		for _, d := range c.devices {
			if wb, _ := ParseHex(d.writeBus); wb != bus {
				continue
			}
			for _, p := range d.params {
				if !p.writable() || !p.conv.CanProcess(t.Number) || len(p.conv.tel) != 1 {
					continue
				}
				cv := NewConverter(p.conv.def) // fresh copy: do not disturb pending read values
				cv.Add(t.Number, low, high)
				if v, err := cv.Value(); err == nil {
					line = fmt.Sprintf("%s/%s=%v %s", d.name, p.def.Name, v, line)
					break find
				}
			}
		}
		out = append(out, line)
	}
	return out
}
