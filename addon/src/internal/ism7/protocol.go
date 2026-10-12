package ism7

import (
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Payload types (2 byte frame header field).
const (
	TypeSystemconfigReq  uint16 = 0x2
	TypeSystemconfigResp uint16 = 0x3
	TypeBundleReq        uint16 = 0x4
	TypeBundleResp       uint16 = 0x5
	TypeLoginReq         uint16 = 0x8
	TypeLoginResp        uint16 = 0x9
	TypeKeepAlive        uint16 = 0xf
)

type LoginReq struct {
	XMLName  xml.Name `xml:"direct-logon-request"`
	Password string   `xml:"password"`
}

type LoginResp struct {
	State    string `xml:"state,attr"`
	Sid      string `xml:"sid,attr"`
	ErrorMsg string `xml:"errormsg"`
}

type SystemconfigReq struct {
	XMLName xml.Name `xml:"read-systemconfig-request"`
	Sid     string   `xml:"sid,attr"`
}

type BusDevice struct {
	Ba              string `xml:"ba,attr"`
	SoftwareVersion string `xml:"sv,attr"`
	SoftwareRev     string `xml:"sr,attr"`
	DeviceID        string `xml:"did,attr"`
}

type SystemconfigResp struct {
	Devices []BusDevice `xml:"busconfig>busDevices>busDevice"`
}

type InfoRead struct {
	XMLName  xml.Name `xml:"ird"`
	Number   int      `xml:"in,attr"`
	Seq      string   `xml:"se,attr"`
	Bus      string   `xml:"ba,attr,omitempty"`
	Interval *int     `xml:"is,attr"`
	Service  *int     `xml:"snr,attr"`
}

type InfoWrite struct {
	XMLName xml.Name `xml:"iwr"`
	Seq     string   `xml:"se,attr"`
	Bus     string   `xml:"ba,attr"`
	Number  uint16   `xml:"in,attr"`
	DBLow   string   `xml:"dl,attr"`
	DBHigh  string   `xml:"dh,attr"`
	Service *int     `xml:"snr,attr"`
}

type BundleReq struct {
	XMLName      xml.Name    `xml:"tbreq"`
	BundleID     string      `xml:"bn,attr"`
	Gateway      string      `xml:"gw,attr"`
	AbortOnError bool        `xml:"ae,attr"`
	Type         string      `xml:"ty,attr"` // pull, push, write
	Reads        []InfoRead  `xml:"ird"`
	Writes       []InfoWrite `xml:"iwr"`
}

// Telegram is an irs (read result) or iac (write ack) element.
type Telegram struct {
	Seq     string `xml:"se,attr"`
	Bus     string `xml:"ba,attr"`
	State   string `xml:"st,attr"`
	Number  uint16 `xml:"in,attr"`
	DBLow   string `xml:"dl,attr"`
	DBHigh  string `xml:"dh,attr"`
	Service string `xml:"snr,attr"`
}

func (t Telegram) ServiceNr() *int {
	if v, err := strconv.Atoi(t.Service); err == nil {
		return &v
	}
	return nil
}

type BundleResp struct {
	BundleID string     `xml:"bn,attr"`
	State    string     `xml:"st,attr"`
	ErrorMsg string     `xml:"emsg,attr"`
	Reads    []Telegram `xml:"irs"`
	Writes   []Telegram `xml:"iac"`
}

// ParseHex parses "0x1F" style bytes as used by the protocol.
func ParseHex(s string) (byte, error) {
	if len(s) > 4 || !strings.HasPrefix(s, "0x") {
		return 0, fmt.Errorf("%q is not a hex value", s)
	}
	v, err := strconv.ParseUint(s[2:], 16, 8)
	return byte(v), err
}

func hex(b byte) string { return fmt.Sprintf("0x%02X", b) }

// WriteFrame writes a message: 4 byte big endian length, 2 byte type, payload.
func WriteFrame(w io.Writer, typ uint16, payload []byte) error {
	buf := make([]byte, 6+len(payload))
	binary.BigEndian.PutUint32(buf, uint32(len(payload)))
	binary.BigEndian.PutUint16(buf[4:], typ)
	copy(buf[6:], payload)
	_, err := w.Write(buf)
	return err
}

func ReadFrame(r io.Reader) (typ uint16, payload []byte, err error) {
	var h [6]byte
	if _, err = io.ReadFull(r, h[:]); err != nil {
		return
	}
	n := binary.BigEndian.Uint32(h[:4])
	if n > 16<<20 {
		return 0, nil, fmt.Errorf("frame too large: %d bytes", n)
	}
	payload = make([]byte, n)
	_, err = io.ReadFull(r, payload)
	return binary.BigEndian.Uint16(h[4:]), payload, err
}

func marshal(v any) ([]byte, error) {
	b, err := xml.Marshal(v)
	return append([]byte(xml.Header), b...), err
}
