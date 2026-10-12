package ism7

import (
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Converter turns raw telegram words into a value and back. One instance per running parameter.
type Converter struct {
	def  ConverterDef
	tel  []uint16          // telegram numbers read for this converter, in definition order
	vals map[uint16]uint16 // received, not yet consumed
	bad  bool              // unicode text with unsupported data
}

func NewConverter(def ConverterDef) *Converter {
	c := &Converter{def: def, vals: map[uint16]uint16{}}
	switch def.Kind {
	case "NumericConverter32Template":
		c.tel = []uint16{def.TelegramNrHigh, def.TelegramNrLow}
	case "MixerStateConverterTemplate":
		c.tel = []uint16{def.TelegramNrOpen, def.TelegramNrClose}
	case "DateTimeConverterTemplate", "SolarStatisticConverterTemplate", "TimeprogConverterTemplate", "UnicodeTextConverter":
		c.tel = def.TelegramNumbers
	case "NullConverterTemplate", "Timeprog03F1ConverterTemplate":
	default:
		if def.TelegramNr != nil {
			c.tel = []uint16{*def.TelegramNr}
		}
	}
	return c
}

func (c *Converter) CTID() int { return c.def.CTID }

// InfoReads returns the read telegrams to request for this converter.
func (c *Converter) InfoReads() []InfoRead {
	out := make([]InfoRead, 0, len(c.tel))
	for _, t := range c.tel {
		out = append(out, InfoRead{Number: int(t), Service: c.def.ServiceRead})
	}
	return out
}

func (c *Converter) CanProcess(tg uint16) bool {
	for _, t := range c.tel {
		if t == tg {
			return true
		}
	}
	return false
}

func (c *Converter) Add(tg uint16, low, high byte) {
	if !c.CanProcess(tg) {
		return
	}
	w := uint16(high)<<8 | uint16(low)
	switch c.def.Kind {
	case "READONLY_Bit0to3ConverterTemplate", "READONLY_Bit4to7ConverterTemplate":
		if high != 0 {
			slog.Error("bit converter: unexpected high byte", "ctid", c.def.CTID)
			return
		}
	case "BM2DateConverterTemplate":
		if (w>>5)&0xf > 11 {
			slog.Error("invalid date, make sure the time on your BM2 is correct", "telegram", tg)
			return
		}
	case "DateTimeConverterTemplate":
		if !c.validDateTime(tg, w) {
			slog.Error("invalid date/time value, make sure it is set correctly on your device", "telegram", tg, "value", w)
			return
		}
	case "UnicodeTextConverter":
		if high != 128 && high != 0 {
			c.bad = true
		}
		if c.bad {
			slog.Error("unicode text: unsupported data", "telegram", tg, "high", high, "low", low)
			return
		}
		if high == 128 && low == 0 {
			return
		}
	case "TimeprogConverterTemplate":
		return
	}
	c.vals[tg] = w
}

func (c *Converter) validDateTime(tg uint16, v uint16) bool {
	idx := -1
	for i, t := range c.tel {
		if t == tg {
			idx = i
		}
	}
	switch c.def.Type {
	case "TIME_hh_mm_ss":
		return (idx == 0 && v <= 23) || ((idx == 1 || idx == 2) && v <= 59)
	case "Date_DD_MM_YY":
		return (idx == 0 && v >= 1 && v <= 31) || (idx == 1 && v >= 1 && v <= 12) || (idx == 2 && v <= 99)
	}
	return true
}

func (c *Converter) HasValue() bool {
	switch c.def.Kind {
	case "NullConverterTemplate", "Timeprog03F1ConverterTemplate", "TimeprogConverterTemplate":
		return false
	case "MixerStateConverterTemplate", "SolarStatisticConverterTemplate":
		return len(c.vals) > 0
	case "UnicodeTextConverter":
		return !c.bad && len(c.vals) > 0
	}
	return len(c.tel) > 0 && len(c.vals) == len(c.tel)
}

// Value returns the decoded value (number or string) and clears the stored telegrams.
func (c *Converter) Value() (any, error) {
	if !c.HasValue() {
		return nil, fmt.Errorf("CTID %d has no value", c.def.CTID)
	}
	defer clear(c.vals)
	first := c.vals[c.tel[0]]
	switch c.def.Kind {
	case "NumericConverter16Template":
		switch c.def.Type {
		case "US":
			return first, nil
		case "SS":
			return int16(first), nil
		case "SS10":
			return float64(int16(first)) / 10, nil
		case "US10":
			return float64(first) / 10, nil
		case "SS100":
			return float64(int16(first)) / 100, nil
		case "SSPR":
			return float64(int16(first)) / 255, nil
		case "US4":
			return float64(first) / 4, nil
		case "IntDiv60":
			return float64(first) / 60, nil
		}
	case "NumericConverter32Template":
		if c.def.Type == "UINT32" {
			return uint32(c.vals[c.def.TelegramNrHigh])<<16 | uint32(c.vals[c.def.TelegramNrLow]), nil
		}
	case "READONLY_Bit0to3ConverterTemplate":
		return first & 0xf, nil
	case "READONLY_Bit4to7ConverterTemplate":
		return (first & 0xff) >> 4, nil
	case "BinaryReadOnlyConverterTemplate":
		if int(first>>c.def.Bitnumber)&1 == c.def.OnValue {
			return 1, nil
		}
		return 0, nil
	case "MixerStateConverterTemplate":
		if c.vals[c.def.TelegramNrOpen] != 0 {
			return 2, nil
		}
		if c.vals[c.def.TelegramNrClose] != 0 {
			return 1, nil
		}
		return 0, nil
	case "BM2TimeConverterTemplate":
		return fmt.Sprintf("%02d:%02d:00", first>>8, first&0xff), nil
	case "BM2DateConverterTemplate":
		return time.Date(2000+int(first>>9), time.Month((first>>5)&0xf+1), int(first&0x1f)+1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), nil
	case "DateTimeConverterTemplate":
		v := func(i int) int { return int(c.vals[c.tel[i]]) }
		switch c.def.Type {
		case "TIME_hh_mm_ss":
			return fmt.Sprintf("%02d:%02d:%02d", v(0), v(1), v(2)), nil
		case "Date_DD_MM_YY":
			return time.Date(2000+v(2), time.Month(v(1)), v(0), 0, 0, 0, 0, time.UTC).Format("2006-01-02"), nil
		}
	case "SolarStatisticConverterTemplate":
		if c.def.Type == "SolarertragW_kW" || c.def.Type == "SolarertragW_kW_MW" {
			keys := make([]int, 0, len(c.vals))
			for k := range c.vals {
				keys = append(keys, int(k))
			}
			sort.Ints(keys)
			total, mul := uint64(0), uint64(1)
			for _, k := range keys {
				total += uint64(c.vals[uint16(k)]) * mul
				mul *= 1000
				if mul > 1_000_000 {
					break
				}
			}
			return total, nil
		}
	case "UnicodeTextConverter":
		keys := make([]int, 0, len(c.vals))
		for k := range c.vals {
			keys = append(keys, int(k))
		}
		sort.Ints(keys)
		var sb strings.Builder
		for _, k := range keys {
			sb.WriteRune(rune(c.vals[uint16(k)]))
		}
		return sb.String(), nil
	}
	return nil, fmt.Errorf("type %q of %s (CTID %d) is not implemented", c.def.Type, c.def.Kind, c.def.CTID)
}

func word(n uint16, v uint16) InfoWrite {
	return InfoWrite{Number: n, DBLow: hex(byte(v)), DBHigh: hex(byte(v >> 8))}
}

// CanWrite reports whether Write is implemented for this converter kind.
func (c *Converter) CanWrite() bool {
	switch c.def.Kind {
	case "NumericConverter16Template", "NumericConverter32Template", "BM2TimeConverterTemplate", "BM2DateConverterTemplate":
		return true
	}
	return false
}

// Write converts a textual value to the write telegrams. Bus address and seq are set by the caller.
func (c *Converter) Write(value string) ([]InfoWrite, error) {
	value = strings.TrimSpace(value)
	var out []InfoWrite
	switch c.def.Kind {
	case "NumericConverter16Template":
		if c.def.TelegramNr == nil {
			return nil, nil
		}
		var scale float64
		switch c.def.Type {
		case "US", "SS":
			scale = 1
		case "SS10", "US10":
			scale = 10
		case "SS100":
			scale = 100
		case "SSPR":
			scale = 255
		case "US4":
			scale = 4
		case "IntDiv60":
			scale = 60
		default:
			return nil, fmt.Errorf("type %q of CTID %d is not implemented", c.def.Type, c.def.CTID)
		}
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", value)
		}
		r := math.Round(f * scale)
		lo := 0.0
		if strings.HasPrefix(c.def.Type, "S") {
			lo = -32768
		}
		hi := 65535.0
		if lo < 0 {
			hi = 32767
		}
		if r < lo || r > hi {
			return nil, fmt.Errorf("value %q out of range for type %s", value, c.def.Type)
		}
		out = append(out, word(*c.def.TelegramNr, uint16(int32(r))))
	case "NumericConverter32Template":
		u, err := strconv.ParseUint(value, 10, 32)
		if err != nil || c.def.Type != "UINT32" {
			return nil, fmt.Errorf("invalid uint32 %q", value)
		}
		out = append(out, word(c.def.TelegramNrLow, uint16(u)), word(c.def.TelegramNrHigh, uint16(u>>16)))
	case "BM2TimeConverterTemplate":
		var h, m int
		if _, err := fmt.Sscanf(value, "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 || c.def.TelegramNr == nil {
			return nil, fmt.Errorf("invalid time %q, want hh:mm", value)
		}
		out = append(out, word(*c.def.TelegramNr, uint16(h)<<8|uint16(m)))
	case "BM2DateConverterTemplate":
		d, err := time.Parse("2006-01-02", value)
		if err != nil || d.Year() < 2000 || c.def.TelegramNr == nil {
			return nil, fmt.Errorf("invalid date %q, want yyyy-mm-dd", value)
		}
		out = append(out, word(*c.def.TelegramNr, uint16(d.Day()-1)|uint16(d.Month()-1)<<5|uint16(d.Year()-2000)<<9))
	default:
		return nil, fmt.Errorf("writing %s (CTID %d) is not implemented", c.def.Kind, c.def.CTID)
	}
	return out, nil
}
