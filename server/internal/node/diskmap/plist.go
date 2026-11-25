package diskmap

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"unicode/utf16"
)

// A minimal property list reader for an app bundle's Info.plist: the string values of the top-level dictionary, from
// the XML form or the binary one (bplist00). Nothing else of a plist is needed, so nothing else is read.

// plistMost is the largest Info.plist read; a real one is a few kilobytes.
const plistMost = 1 << 20

var errPlist = errors.New("not a property list this reader knows")

// readPlistStrings reads file's top-level string values.
func readPlistStrings(file string) (map[string]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, plistMost+1))
	if err != nil {
		return nil, err
	}
	if len(data) > plistMost {
		return nil, errPlist
	}
	return plistStrings(data)
}

// plistStrings is the top-level dictionary's string values of an XML or binary property list.
func plistStrings(data []byte) (map[string]string, error) {
	if bytes.HasPrefix(data, []byte("bplist00")) {
		return binaryPlistStrings(data)
	}
	return xmlPlistStrings(data)
}

func xmlPlistStrings(data []byte) (map[string]string, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	out := map[string]string{}
	depth := 0 // of dict and array elements
	key, haveKey := "", false
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errPlist
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			switch {
			case depth == 0 && name == "dict":
				depth = 1
			case depth == 0:
				// <plist> and anything before the top dictionary
			case depth == 1 && name == "key":
				var s string
				if err := d.DecodeElement(&s, &t); err != nil {
					return nil, errPlist
				}
				key, haveKey = s, true
			case depth == 1 && name == "string" && haveKey:
				var s string
				if err := d.DecodeElement(&s, &t); err != nil {
					return nil, errPlist
				}
				out[key] = s
				haveKey = false
			default:
				if err := d.Skip(); err != nil {
					return nil, errPlist
				}
				haveKey = false
			}
		case xml.EndElement:
			if depth == 1 && t.Name.Local == "dict" {
				return out, nil
			}
		}
	}
	if depth == 0 {
		return nil, errPlist
	}
	return out, nil
}

// binaryPlistStrings reads a bplist00: its trailer, offset table and the top object, which must be a dictionary.
func binaryPlistStrings(data []byte) (map[string]string, error) {
	if len(data) < 8+32 {
		return nil, errPlist
	}
	tr := data[len(data)-32:]
	offSize, refSize := int(tr[6]), int(tr[7])
	count := binary.BigEndian.Uint64(tr[8:16])
	top := binary.BigEndian.Uint64(tr[16:24])
	table := binary.BigEndian.Uint64(tr[24:32])
	if offSize < 1 || offSize > 8 || refSize < 1 || refSize > 8 || count == 0 || count > uint64(len(data)) || top >= count ||
		table >= uint64(len(data)) || table+count*uint64(offSize) > uint64(len(data)-32) {
		return nil, errPlist
	}
	p := bplist{data: data, offSize: offSize, refSize: refSize, count: count, table: table}
	off, ok := p.offset(top)
	if !ok || data[off]>>4 != 0xD {
		return nil, errPlist
	}
	n, start, ok := p.length(off)
	if !ok || start+2*n*refSize > len(data) {
		return nil, errPlist
	}
	out := map[string]string{}
	for i := range n {
		k, ok1 := p.ref(start + i*refSize)
		v, ok2 := p.ref(start + (n+i)*refSize)
		if !ok1 || !ok2 {
			return nil, errPlist
		}
		key, ok := p.str(k)
		if !ok {
			continue
		}
		if val, ok := p.str(v); ok {
			out[key] = val
		}
	}
	return out, nil
}

type bplist struct {
	data             []byte
	offSize, refSize int
	count, table     uint64
}

func uintN(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

// offset is where object i starts.
func (p bplist) offset(i uint64) (int, bool) {
	if i >= p.count {
		return 0, false
	}
	at := p.table + i*uint64(p.offSize)
	off := uintN(p.data[at : at+uint64(p.offSize)])
	if off < 8 || off >= uint64(len(p.data)-32) {
		return 0, false
	}
	return int(off), true
}

// ref is the object number at byte at.
func (p bplist) ref(at int) (uint64, bool) {
	if at+p.refSize > len(p.data) {
		return 0, false
	}
	return uintN(p.data[at : at+p.refSize]), true
}

// length is the count an object's marker at off gives, and where its contents start.
func (p bplist) length(off int) (int, int, bool) {
	n := int(p.data[off] & 0x0F)
	if n != 0x0F {
		return n, off + 1, true
	}
	if off+2 > len(p.data) || p.data[off+1]>>4 != 0x1 {
		return 0, 0, false
	}
	size := 1 << (p.data[off+1] & 0x0F)
	if size > 8 || off+2+size > len(p.data) {
		return 0, 0, false
	}
	v := uintN(p.data[off+2 : off+2+size])
	if v > uint64(len(p.data)) {
		return 0, 0, false
	}
	return int(v), off + 2 + size, true
}

// str is object i as a string, when it is one (ASCII or UTF-16).
func (p bplist) str(i uint64) (string, bool) {
	off, ok := p.offset(i)
	if !ok {
		return "", false
	}
	kind := p.data[off] >> 4
	if kind != 0x5 && kind != 0x6 {
		return "", false
	}
	n, start, ok := p.length(off)
	if !ok {
		return "", false
	}
	if kind == 0x5 {
		if start+n > len(p.data) {
			return "", false
		}
		return string(p.data[start : start+n]), true
	}
	if start+2*n > len(p.data) {
		return "", false
	}
	u := make([]uint16, n)
	for k := range n {
		u[k] = binary.BigEndian.Uint16(p.data[start+2*k:])
	}
	return string(utf16.Decode(u)), true
}
