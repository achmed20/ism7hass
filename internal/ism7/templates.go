package ism7

import (
	"embed"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// preferredJSON is a real parameter.json; its ids win when duplicate parameters are merged.
//
//go:embed testdata/wolfcha_org.json
var preferredJSON []byte

//go:embed resources/*.xml
var resources embed.FS

// ConverterDef is one <...Template> element of converter.xml. Kind is the element name.
type ConverterDef struct {
	Kind            string
	CTID            int      `xml:"CTID,attr"`
	Type            string   `xml:"Type"`
	ServiceRead     *int     `xml:"ServiceReadNumber"`
	ServiceWrite    int      `xml:"ServiceWriteNumber"`
	TelegramNr      *uint16  `xml:"TelegramNr"`
	TelegramNumbers []uint16 `xml:"TelegramNumbers>unsignedShort"`
	TelegramNrHigh  uint16   `xml:"TelegramNrHigh"`
	TelegramNrLow   uint16   `xml:"TelegramNrLow"`
	TelegramNrOpen  uint16   `xml:"TelegramNrOpen"`
	TelegramNrClose uint16   `xml:"TelegramNrClose"`
	OnValue         int      `xml:"OnValue"`
	Bitnumber       int      `xml:"Bitnumber"`
}

type converterList []ConverterDef

var knownConverters = map[string]bool{
	"NumericConverter16Template": true, "NumericConverter32Template": true,
	"DateTimeConverterTemplate": true, "MixerStateConverterTemplate": true,
	"READONLY_Bit0to3ConverterTemplate": true, "READONLY_Bit4to7ConverterTemplate": true,
	"SolarStatisticConverterTemplate": true, "BinaryReadOnlyConverterTemplate": true,
	"BM2TimeConverterTemplate": true, "BM2DateConverterTemplate": true,
	"NullConverterTemplate": true, "TimeprogConverterTemplate": true,
	"Timeprog03F1ConverterTemplate": true, "UnicodeTextConverter": true,
}

func (l *converterList) UnmarshalXML(d *xml.Decoder, _ xml.StartElement) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if !knownConverters[t.Name.Local] { // e.g. Modbus*, not supported by the original either
				if err := d.Skip(); err != nil {
					return err
				}
				continue
			}
			def := ConverterDef{Kind: t.Name.Local}
			if err := d.DecodeElement(&def, &t); err != nil {
				return err
			}
			*l = append(*l, def)
		case xml.EndElement:
			return nil
		}
	}
}

// ParameterDef is one *ParameterDescriptor element of parameter.xml.
type ParameterDef struct {
	Kind        string
	PTID        int    `xml:"PTID,attr"`
	Name        string `xml:"Name"`
	UnitName    string `xml:"UnitName"`
	ReadOnly    string `xml:"ReadOnlyConditionId"`
	KeyValueStr string `xml:"KeyValueList"`
	RemoveCond  string `xml:"RemoveConditionId"`
	ControlType string `xml:"ControlType"`
	Min         string `xml:"MinValueCondition"`
	Max         string `xml:"MaxValueCondition"`
	Step        string `xml:"StepWidth"`
}

func (p ParameterDef) Writable() bool { return p.ReadOnly == "False" }

// Options parses "0;Aus;1;Ein" into ordered key/text pairs.
func (p ParameterDef) Options() [][2]string {
	if p.KeyValueStr == "" {
		return nil
	}
	parts := strings.Split(p.KeyValueStr, ";")
	var out [][2]string
	for i := 0; i < len(parts)-1; i += 2 {
		out = append(out, [2]string{parts[i], parts[i+1]})
	}
	return out
}

// IsBoolean: a list with exactly the two options Ein/Aus or Aktiviert/Deaktiviert.
func (p ParameterDef) IsBoolean() bool {
	o := p.Options()
	if len(o) != 2 {
		return false
	}
	has := func(t string) bool { return o[0][1] == t || o[1][1] == t }
	return has("Ein") && has("Aus") || has("Aktiviert") && has("Deaktiviert")
}

func (p ParameterDef) IsList() bool { return p.Kind == "ListParameterDescriptor" }

type parameterList []ParameterDef

func (l *parameterList) UnmarshalXML(d *xml.Decoder, _ xml.StartElement) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			def := ParameterDef{Kind: t.Name.Local}
			if err := d.DecodeElement(&def, &t); err != nil {
				return err
			}
			*l = append(*l, def)
		case xml.EndElement:
			return nil
		}
	}
}

// DeviceDef is one DeviceTemplate of device.xml.
type DeviceDef struct {
	DTID          int    `xml:"DTID,attr"`
	Name          string `xml:"Name,attr"`
	SoftwareNr    string `xml:"SoftwareNumber"`
	WRSDeviceIDs  string `xml:"WRSDeviceIds"`
	ParameterRefs []struct {
		PTID int `xml:"PTID,attr"`
	} `xml:"ParameterReferenceList>ParameterReference"`
}

// Templates holds the parsed device, converter and parameter definitions.
type Templates struct {
	Devices    []DeviceDef
	Converters []ConverterDef
	Parameters []ParameterDef
}

// LoadTemplates parses the embedded XML files. parameterOverride, if set, replaces the
// embedded parameter.xml (the whole file, no merge).
func LoadTemplates(parameterOverride string) (*Templates, error) {
	read := func(name string) ([]byte, error) { return resources.ReadFile("resources/" + name) }
	var t Templates

	b, err := read("device.xml")
	if err != nil {
		return nil, err
	}
	var dev struct {
		Devices []DeviceDef `xml:"DeviceTemplates>DeviceTemplate"`
	}
	if err := xml.Unmarshal(b, &dev); err != nil {
		return nil, fmt.Errorf("parse device.xml: %w", err)
	}
	t.Devices = dev.Devices

	if b, err = read("converter.xml"); err != nil {
		return nil, err
	}
	var conv struct {
		List converterList `xml:"ConverterTemplates"`
	}
	if err := xml.Unmarshal(b, &conv); err != nil {
		return nil, fmt.Errorf("parse converter.xml: %w", err)
	}
	t.Converters = conv.List

	if parameterOverride != "" {
		b, err = os.ReadFile(parameterOverride)
	} else {
		b, err = read("parameter.xml")
	}
	if err != nil {
		return nil, err
	}
	var par struct {
		List parameterList `xml:"ParameterList"`
	}
	if err := xml.Unmarshal(b, &par); err != nil {
		return nil, fmt.Errorf("parse parameter.xml: %w", err)
	}
	t.Parameters = par.List
	return &t, nil
}

// subDevices: templates that Wolf's software adds as extra devices on the same bus address.
// ponytail: derived from one real installation (BM-2 -> heating circuit + hot water); mixer circuits (360000) etc. are not covered.
var subDevices = map[int][]int{220000: {340000, 350000}}

func (t *Templates) deviceByID(id int) *DeviceDef {
	for i := range t.Devices {
		if t.Devices[i].DTID == id {
			return &t.Devices[i]
		}
	}
	return nil
}

// BuildConfigFile maps the bus devices reported by the ISM7 to device templates and lists all
// parameters of the template that have a parameter descriptor and a converter.
// ponytail: the .NET ism7config also evaluates the template's conditions to drop parameters that are not
// available on this installation; here you get all of them and can delete unwanted ones from the json.
func (t *Templates) BuildConfigFile(devs []BusDevice, port uint16, enabled bool) (*ConfigFile, []string) {
	cf := &ConfigFile{TcpPort: port, Devices: []ConfigDevice{}}
	var skipped []string
	have := map[int]string{}
	for _, p := range t.Parameters {
		have[p.PTID] = p.Name // ponytail: RemoveConditionId needs live values (closed smartset logic), so this is a superset
	}
	conv := map[int]bool{}
	convKey := map[int]string{}
	for _, c := range t.Converters {
		conv[c.CTID] = true
		convKey[c.CTID] = fmt.Sprint(c.Kind, c.Type, NewConverter(c).tel)
	}
	var realFile struct{ Devices []struct{ Parameter []int } }
	_ = json.Unmarshal(preferredJSON, &realFile)
	preferred := map[int]bool{}
	for _, d := range realFile.Devices {
		for _, id := range d.Parameter {
			preferred[id] = true
		}
	}
	sort.Slice(devs, func(i, j int) bool { return devs[i].Ba < devs[j].Ba })
	for _, d := range devs {
		dt := t.matchDevice(d)
		if dt == nil {
			skipped = append(skipped, fmt.Sprintf("%s (did %s, sw %s/%s)", d.Ba, d.DeviceID, d.SoftwareVersion, d.SoftwareRev))
			continue
		}
		rb, _ := ParseHex(d.Ba)
		wb := hex(rb - 5)
		dts := []*DeviceDef{dt}
		for _, id := range subDevices[dt.DTID] {
			if sd := t.deviceByID(id); sd != nil {
				dts = append(dts, sd)
			}
		}
		for _, dt := range dts {
			cd := ConfigDevice{Name: dt.Name, ReadBusAddress: d.Ba, WriteBusAddress: &wb, DeviceTemplateID: dt.DTID, Parameter: []ConfigParameter{}}
			for _, r := range dt.ParameterRefs {
				if name, ok := have[r.PTID]; ok && conv[r.PTID] {
					cd.Parameter = append(cd.Parameter, ConfigParameter{ID: r.PTID, Name: name, Enabled: &enabled})
				}
			}
			sort.Slice(cd.Parameter, func(i, j int) bool { return cd.Parameter[i].ID < cd.Parameter[j].ID })
			// parameters with the same name and the same telegrams are the same value: keep one enabled
			keep := map[string]int{}
			for _, p := range cd.Parameter {
				k := p.Name + "|" + convKey[p.ID]
				if cur, ok := keep[k]; !ok || (!preferred[cur] && preferred[p.ID]) {
					keep[k] = p.ID
				}
			}
			for i, p := range cd.Parameter {
				if keep[p.Name+"|"+convKey[p.ID]] != p.ID && !preferred[p.ID] { // ids from the real file always stay on
					off := false
					cd.Parameter[i].Enabled = &off
				}
			}
			cf.Devices = append(cf.Devices, cd)
		}
	}
	return cf, skipped
}

func hexVal(s string) (int64, bool) {
	v, err := strconv.ParseInt(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, 64)
	return v, err == nil
}

// matchDevice finds the template with the same WRS device id, preferring the exact software number.
func (t *Templates) matchDevice(d BusDevice) *DeviceDef {
	did, ok := hexVal(d.DeviceID)
	if !ok {
		return nil
	}
	var sw int64 = -1
	if sv, ok1 := hexVal(d.SoftwareVersion); ok1 {
		if sr, ok2 := hexVal(d.SoftwareRev); ok2 {
			sw = sv*100 + sr
		}
	}
	var fallback *DeviceDef
	for i := range t.Devices {
		dt := &t.Devices[i]
		if w, ok := hexVal(dt.WRSDeviceIDs); !ok || w != did {
			continue
		}
		if s, _ := hexVal(dt.SoftwareNr); s == sw {
			return dt
		} else if fallback == nil || s == 0 {
			fallback = dt
		}
	}
	return fallback
}
