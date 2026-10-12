package mqtt

import (
	"testing"

	"ism7/internal/ism7"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBridge_ParseSet(t *testing.T) {
	u := uint16(5)
	cfg, err := ism7.NewConfig(
		&ism7.ConfigFile{Devices: []ism7.ConfigDevice{{ReadBusAddress: "0x29", DeviceTemplateID: 1, Parameter: []ism7.ConfigParameter{{ID: 1}}}}},
		&ism7.Templates{
			Devices:    []ism7.DeviceDef{{DTID: 1, Name: "CHA"}},
			Converters: []ism7.ConverterDef{{Kind: "NumericConverter16Template", CTID: 1, Type: "US", TelegramNr: &u}},
			Parameters: []ism7.ParameterDef{{PTID: 1, Name: "Soll Wert", ReadOnly: "False"}},
		}, "ip")
	require.NoError(t, err)
	b := New(Options{IP: "ip"}, cfg)

	w, err := b.parse("Wolf/ip/CHA_0x29/set", `{"Soll Wert": 10}`)
	require.NoError(t, err)
	require.Len(t, w, 1)
	assert.Equal(t, "0x0A", w[0].DBLow)
	assert.Equal(t, "0x24", w[0].Bus)

	w, err = b.parse("Wolf/ip/CHA_0x29/set/Soll_Wert", "10")
	require.NoError(t, err)
	assert.Len(t, w, 1)

	w, _ = b.parse("Wolf/ip/CHA_0x29/set/Unbekannt", "10")
	assert.Empty(t, w)
	_, err = b.parse("Wolf/ip/OTHER/set", `{}`)
	assert.Error(t, err)
}
