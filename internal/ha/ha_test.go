package ha

import (
	"testing"

	"ism7/internal/ism7"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func u(v uint16) *uint16 { return &v }

func TestDiscovery_Messages(t *testing.T) {
	conv := func(id int, n uint16) ism7.ConverterDef {
		return ism7.ConverterDef{Kind: "NumericConverter16Template", CTID: id, Type: "SS10", TelegramNr: u(n)}
	}
	cfg, err := ism7.NewConfig(
		&ism7.ConfigFile{Devices: []ism7.ConfigDevice{{ReadBusAddress: "0x35", DeviceTemplateID: 1,
			Parameter: []ism7.ConfigParameter{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}}}}},
		&ism7.Templates{
			Devices:    []ism7.DeviceDef{{DTID: 1, Name: "BM-2"}},
			Converters: []ism7.ConverterDef{conv(1, 1), conv(2, 2), conv(3, 3), conv(4, 4), {Kind: "UnicodeTextConverter", CTID: 5, TelegramNumbers: []uint16{9}}},
			Parameters: []ism7.ParameterDef{
				{Kind: "NumericParameterDescriptor", PTID: 1, Name: "Soll Wert", ReadOnly: "False", Min: "-4", Max: "4", Step: "0.5", UnitName: "°C"},
				{Kind: "ListParameterDescriptor", PTID: 2, Name: "Party", ReadOnly: "False", KeyValueStr: "0;Deaktiviert;1;Aktiviert"},
				{Kind: "ListParameterDescriptor", PTID: 3, Name: "Modus", ReadOnly: "False", KeyValueStr: "1;Auto;2;Eco"},
				{Kind: "NumericParameterDescriptor", PTID: 5, Name: "Txt", ReadOnly: "False"},
				{Kind: "NumericParameterDescriptor", PTID: 4, Name: "Außentemp", ReadOnly: "True", UnitName: "kWh"},
			},
		}, "1.2.3.4")
	require.NoError(t, err)
	d := &Discovery{ID: "wolf", IP: "1.2.3.4", Cfg: cfg}
	got := map[string]map[string]any{}
	for _, m := range d.Messages() {
		got[m.Topic] = m.Payload
	}
	require.Len(t, got, 5)

	n := got["homeassistant/number/wolf_BM2_0x30_1_Soll_Wert/config"]
	require.NotNil(t, n)
	assert.Equal(t, "Wolf/1.2.3.4/BM-2_0x35/Soll_Wert", n["state_topic"])
	assert.Equal(t, "Wolf/1.2.3.4/BM-2_0x35/set/Soll_Wert", n["command_topic"])
	assert.Equal(t, -4.0, n["min"])
	assert.Equal(t, 0.5, n["step"])

	s := got["homeassistant/switch/wolf_BM2_0x30_2_Party/config"]
	require.NotNil(t, s)
	assert.Equal(t, "Aktiviert", s["payload_on"])
	assert.Equal(t, "Deaktiviert", s["payload_off"])
	assert.Equal(t, "Wolf/1.2.3.4/BM-2_0x35/set/Party/text", s["command_topic"])

	sel := got["homeassistant/select/wolf_BM2_0x30_3_Modus/config"]
	require.NotNil(t, sel)
	assert.Equal(t, []string{"Auto", "Eco"}, sel["options"])

	se := got["homeassistant/sensor/wolf_BM2_0x30_4_Aussentemp/config"]
	require.NotNil(t, se)
	assert.Equal(t, "energy", se["device_class"])
	assert.NotContains(t, se, "command_topic")

	ro := got["homeassistant/sensor/wolf_BM2_0x30_5_Txt/config"]
	require.NotNil(t, ro)
	assert.NotContains(t, ro, "command_topic")
}
