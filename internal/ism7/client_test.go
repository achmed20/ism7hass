package ism7

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func u16(v uint16) *uint16 { return &v }

func testTemplates() *Templates {
	return &Templates{
		Devices: []DeviceDef{{DTID: 1, Name: "CHA"}},
		Converters: []ConverterDef{
			{Kind: "NumericConverter16Template", CTID: 1, Type: "SS10", TelegramNr: u16(5)},
			{Kind: "NumericConverter16Template", CTID: 2, Type: "US", TelegramNr: u16(6)},
			{Kind: "NumericConverter32Template", CTID: 3, Type: "UINT32", TelegramNrHigh: 7, TelegramNrLow: 8},
		},
		Parameters: []ParameterDef{
			{Kind: "NumericParameterDescriptor", PTID: 1, Name: "Temp Aussen", ReadOnly: "True"},
			{Kind: "ListParameterDescriptor", PTID: 2, Name: "Raumeinfluss", ReadOnly: "False", KeyValueStr: "0;Aus;1;Ein"},
			{Kind: "NumericParameterDescriptor", PTID: 3, Name: "Zähler", ReadOnly: "True"},
		},
	}
}

func testConfig(t *testing.T) *Config {
	cf := &ConfigFile{Devices: []ConfigDevice{{ReadBusAddress: "0x29", DeviceTemplateID: 1, Parameter: []ConfigParameter{{ID: 1}, {ID: 2}, {ID: 3}}}}}
	cfg, err := NewConfig(cf, testTemplates(), "1.2.3.4")
	require.NoError(t, err)
	return cfg
}

func telegram(bus string, n uint16, v uint16) Telegram {
	return Telegram{Bus: bus, State: "OK", Number: n, DBLow: hex(byte(v)), DBHigh: hex(byte(v >> 8))}
}

// fakeISM7 answers login, pull, push and write like the device does.
func fakeISM7(t *testing.T, values map[uint16]uint16) (uint16, chan BundleReq) {
	cert, err := tls.X509KeyPair(clientPEM, clientPEM)
	require.NoError(t, err)
	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAnyClientCert, MaxVersion: tls.VersionTLS12,
		CipherSuites: []uint16{tls.TLS_RSA_WITH_RC4_128_SHA, tls.TLS_RSA_WITH_AES_256_CBC_SHA},
	})
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })
	reqs := make(chan BundleReq, 10)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			typ, p, err := ReadFrame(conn)
			if err != nil {
				return
			}
			switch typ {
			case TypeLoginReq:
				b, _ := marshal(struct {
					XMLName xml.Name `xml:"direct-logon-response"`
					State   string   `xml:"state,attr"`
				}{State: "ok"})
				WriteFrame(conn, TypeLoginResp, b)
			case TypeSystemconfigReq:
				WriteFrame(conn, TypeSystemconfigResp, []byte(`<read-systemconfig-response sid="1"><busconfig type="ebus"><busDevices>`+
					`<busDevice ba="0x8" sv="0x1" sr="0x70" did="0x29"/></busDevices></busconfig></read-systemconfig-response>`))
			case TypeBundleReq:
				var r BundleReq
				if xml.Unmarshal(p, &r) != nil {
					return
				}
				reqs <- r
				resp := `<tbres bn="` + r.BundleID + `" st="OK">`
				for _, ird := range r.Reads {
					resp += `<irs ba="0x29" st="OK" in="` + strconv.Itoa(ird.Number) + `" dl="` + hex(byte(values[uint16(ird.Number)])) + `" dh="` + hex(byte(values[uint16(ird.Number)]>>8)) + `"/>`
				}
				for _, iwr := range r.Writes {
					resp += `<iac ba="` + iwr.Bus + `" st="OK" in="` + strconv.Itoa(int(iwr.Number)) + `" dl="` + iwr.DBLow + `" dh="` + iwr.DBHigh + `"/>`
				}
				WriteFrame(conn, TypeBundleResp, []byte(resp+`</tbres>`))
			}
		}
	}()
	return uint16(l.Addr().(*net.TCPAddr).Port), reqs
}

func TestClient_ReadPushAndWrite(t *testing.T) {
	port, reqs := fakeISM7(t, map[uint16]uint16{5: 215, 6: 1, 7: 1, 8: 2})
	cfg := testConfig(t)
	cl := NewClient(Options{Host: "127.0.0.1", Port: port, Password: "x", Interval: 30, StartupTimeout: 5 * time.Second}, cfg)

	msgs := make(chan []Message, 10)
	cl.OnData = func() { msgs <- cfg.Messages(true) }
	inited := make(chan struct{})
	cl.OnInitialized = func() { close(inited) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cl.Run(ctx)

	got := map[string]string{}
	for len(got) < 4 {
		select {
		case ms := <-msgs:
			for _, m := range ms {
				got[m.Topic] = m.Payload
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout, got %v", got)
		}
	}
	assert.Equal(t, "21.5", got["Wolf/1.2.3.4/CHA_0x29/Temp_Aussen"])
	assert.Equal(t, "1", got["Wolf/1.2.3.4/CHA_0x29/Raumeinfluss/value"])
	assert.Equal(t, "Ein", got["Wolf/1.2.3.4/CHA_0x29/Raumeinfluss/text"])
	assert.Equal(t, "65538", got["Wolf/1.2.3.4/CHA_0x29/Zaehler"])
	<-inited

	assert.Equal(t, "pull", (<-reqs).Type)

	// write by text
	w, err := cfg.WriteFromParts("Wolf/1.2.3.4/CHA_0x29", []string{"Raumeinfluss", "text"}, "Aus")
	require.NoError(t, err)
	require.Len(t, w, 1)
	assert.Equal(t, "0x24", w[0].Bus) // read address - 5
	require.NoError(t, cl.Write(w))
	for r := range reqs {
		if r.Type == "write" {
			assert.True(t, r.AbortOnError)
			break
		}
	}
	for { // push responses of the fake carry the old value 1, the write ack carries 0
		select {
		case ms := <-msgs:
			for _, m := range ms {
				if m.Topic == "Wolf/1.2.3.4/CHA_0x29/Raumeinfluss/value" && m.Payload == "0" {
					return
				}
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no write ack")
		}
	}
}

func TestConverter_Roundtrip(t *testing.T) {
	for _, tc := range []struct {
		typ   string
		in    string
		low   byte
		high  byte
		value any
	}{
		{"SS10", "-1.5", 0xF1, 0xFF, -1.5},
		{"US", "300", 0x2C, 0x01, uint16(300)},
		{"SS100", "4.35", 0xB3, 0x01, 4.35},
	} {
		c := NewConverter(ConverterDef{Kind: "NumericConverter16Template", Type: tc.typ, TelegramNr: u16(1)})
		w, err := c.Write(tc.in)
		require.NoError(t, err)
		assert.Equal(t, []InfoWrite{{Number: 1, DBLow: hex(tc.low), DBHigh: hex(tc.high)}}, w, tc.typ)
		c.Add(1, tc.low, tc.high)
		v, err := c.Value()
		require.NoError(t, err)
		assert.Equal(t, tc.value, v, tc.typ)
	}
}

func TestConfig_Bundles(t *testing.T) {
	var convs []ConverterDef
	var params []ParameterDef
	var ids []ConfigParameter
	for i := 1; i <= 45; i++ {
		convs = append(convs, ConverterDef{Kind: "NumericConverter16Template", CTID: i, Type: "US", TelegramNr: u16(uint16(i))})
		params = append(params, ParameterDef{PTID: i, Name: "p" + strconv.Itoa(i)})
		ids = append(ids, ConfigParameter{ID: i})
	}
	cfg, err := NewConfig(&ConfigFile{Devices: []ConfigDevice{{ReadBusAddress: "0x29", DeviceTemplateID: 1, Parameter: ids}}},
		&Templates{Devices: []DeviceDef{{DTID: 1, Name: "D"}}, Converters: convs, Parameters: params}, "ip")
	require.NoError(t, err)
	b, err := cfg.Bundles("0x29")
	require.NoError(t, err)
	require.Len(t, b, 3)
	assert.Len(t, b[0].Reads, 20)
}

func TestLoadTemplates(t *testing.T) {
	tpl, err := LoadTemplates("")
	require.NoError(t, err)
	assert.Greater(t, len(tpl.Devices), 40)
	assert.Greater(t, len(tpl.Converters), 2000)
	assert.Greater(t, len(tpl.Parameters), 5000)
}

func TestConfigFile_BothFormats(t *testing.T) {
	var d ConfigDevice
	require.NoError(t, json.Unmarshal([]byte(`{"Parameter":[5,{"id":6,"name":"x","enabled":false}]}`), &d))
	assert.True(t, d.Parameter[0].On())
	assert.False(t, d.Parameter[1].On())
	cfg, err := NewConfig(&ConfigFile{Devices: []ConfigDevice{{ReadBusAddress: "0x29", DeviceTemplateID: 1, Parameter: []ConfigParameter{{ID: 1}, {ID: 2, Enabled: new(bool)}}}}}, testTemplates(), "ip")
	require.NoError(t, err)
	b, _ := cfg.Bundles("0x29")
	assert.Len(t, b[0].Reads, 1)
}

func TestBuildConfigFile_SupersetOfReal(t *testing.T) {
	tpl, err := LoadTemplates("")
	require.NoError(t, err)
	b, err := os.ReadFile("testdata/wolfcha_org.json")
	require.NoError(t, err)
	var real struct {
		Devices []struct {
			ReadBusAddress   string
			WriteBusAddress  string
			DeviceTemplateId int
			Parameter        []int
		}
	}
	require.NoError(t, json.Unmarshal(b, &real))

	cf, skipped := tpl.BuildConfigFile([]BusDevice{
		{Ba: "0x8", DeviceID: "0x29", SoftwareVersion: "0x1", SoftwareRev: "0x70"},
		{Ba: "0x35", DeviceID: "0x20", SoftwareVersion: "0x2", SoftwareRev: "0x4"},
	}, 9092, true)
	require.Empty(t, skipped)
	require.Len(t, cf.Devices, len(real.Devices))
	for _, rd := range real.Devices {
		var got *ConfigDevice
		for i, d := range cf.Devices {
			if d.DeviceTemplateID == rd.DeviceTemplateId && d.ReadBusAddress == rd.ReadBusAddress {
				got = &cf.Devices[i]
			}
		}
		require.NotNil(t, got, "device %d", rd.DeviceTemplateId)
		wb, _ := ParseHex(*got.WriteBusAddress)
		rwb, _ := ParseHex(rd.WriteBusAddress)
		assert.Equal(t, rwb, wb)
		have := map[int]bool{}
		for _, p := range got.Parameter {
			have[p.ID] = true
		}
		for _, id := range rd.Parameter {
			assert.True(t, have[id], "missing %d in %d", id, rd.DeviceTemplateId)
		}
	}
}

func TestBuildConfigFile_MergesDuplicates(t *testing.T) {
	tpl := &Templates{
		Devices: []DeviceDef{{DTID: 1, Name: "D", WRSDeviceIDs: "0x1", ParameterRefs: []struct {
			PTID int `xml:"PTID,attr"`
		}{{1}, {2}, {3}, {4}}}},
		Converters: []ConverterDef{
			{Kind: "NumericConverter16Template", CTID: 1, Type: "US", TelegramNr: u16(5)},
			{Kind: "NumericConverter16Template", CTID: 2, Type: "US", TelegramNr: u16(5)},
			{Kind: "NumericConverter16Template", CTID: 3, Type: "US", TelegramNr: u16(6)},
			{Kind: "NumericConverter16Template", CTID: 4, Type: "US", TelegramNr: u16(7)},
		},
		Parameters: []ParameterDef{{PTID: 1, Name: "A"}, {PTID: 2, Name: "A"}, {PTID: 3, Name: "Mo"}, {PTID: 4, Name: "Mo"}},
	}
	cf, _ := tpl.BuildConfigFile([]BusDevice{{Ba: "0x9", DeviceID: "0x1"}}, 9092, true)
	var on []int
	for _, p := range cf.Devices[0].Parameter {
		if p.On() {
			on = append(on, p.ID)
		}
	}
	assert.Equal(t, []int{1, 3, 4}, on) // 2 merged into 1; Mo/Mo have different telegrams
}

func TestWrite_Validation(t *testing.T) {
	tpl := &Templates{
		Devices: []DeviceDef{{DTID: 1, Name: "D"}},
		Converters: []ConverterDef{
			{Kind: "NumericConverter16Template", CTID: 1, Type: "SS10", TelegramNr: u16(5)},
			{Kind: "NumericConverter16Template", CTID: 2, Type: "US", TelegramNr: u16(6)},
			{Kind: "UnicodeTextConverter", CTID: 3, TelegramNumbers: []uint16{7}},
		},
		Parameters: []ParameterDef{
			{Kind: "NumericParameterDescriptor", PTID: 1, Name: "Soll", ReadOnly: "False", Min: "-4", Max: "4"},
			{Kind: "ListParameterDescriptor", PTID: 2, Name: "Mode", ReadOnly: "False", KeyValueStr: "0;Aus;1;Ein"},
			{Kind: "TextParameterDescriptor", PTID: 3, Name: "Txt", ReadOnly: "False"},
		},
	}
	cfg, err := NewConfig(&ConfigFile{Devices: []ConfigDevice{{ReadBusAddress: "0x29", DeviceTemplateID: 1,
		Parameter: []ConfigParameter{{ID: 1}, {ID: 2}, {ID: 3}}}}}, tpl, "ip")
	require.NoError(t, err)
	top := "Wolf/ip/D_0x29"

	for _, v := range []string{"-4", "4", "0.5"} {
		w, err := cfg.WriteFromParts(top, []string{"Soll"}, v)
		require.NoError(t, err, v)
		assert.Len(t, w, 1)
	}
	for _, v := range []string{"4.5", "-4.5", "9"} {
		_, err := cfg.WriteFromParts(top, []string{"Soll"}, v)
		assert.Error(t, err, v)
	}
	_, err = cfg.WriteFromParts(top, []string{"Mode", "value"}, "2")
	assert.Error(t, err)
	_, err = cfg.WriteFromParts(top, []string{"Mode", "value"}, "1")
	assert.NoError(t, err)

	// one bad value rejects the whole JSON message
	_, err = cfg.WriteFromJSON(top, []byte(`{"Soll": 1, "Mode": 7}`))
	assert.Error(t, err)

	// converter that cannot encode: no write
	w, err := cfg.WriteFromParts(top, []string{"Txt"}, "x")
	assert.NoError(t, err)
	assert.Empty(t, w)

	// signed overflow after scaling
	c := NewConverter(ConverterDef{Kind: "NumericConverter16Template", Type: "SS10", TelegramNr: u16(1)})
	_, err = c.Write("4000")
	assert.Error(t, err)
}

func TestConfig_DescribeWrites(t *testing.T) {
	cfg := testConfig(t)
	got := cfg.DescribeWrites([]Telegram{{Bus: "0x24", State: "OK", Number: 6, DBLow: "0x01", DBHigh: "0x00"}})
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "CHA/Raumeinfluss=1")
	assert.Contains(t, got[0], "telegram=6")
}

func TestGenerateConfigFile(t *testing.T) {
	port, _ := fakeISM7(t, nil)
	tpl, err := LoadTemplates("")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "parameter.json")
	cf, err := GenerateConfigFile(context.Background(), tpl, "127.0.0.1", "x", path, true, port)
	require.NoError(t, err)
	require.Len(t, cf.Devices, 1)
	assert.Equal(t, "CHA", cf.Devices[0].Name)
	loaded, err := LoadConfigFile(path)
	require.NoError(t, err)
	assert.Equal(t, cf.Devices[0].DeviceTemplateID, loaded.Devices[0].DeviceTemplateID)
	files, _ := os.ReadDir(filepath.Dir(path))
	assert.Len(t, files, 1) // no temp file left behind
}
