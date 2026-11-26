package transcript

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
)

// Claude Desktop reads the display role from fields 2 → 1 → 8 in a thinking block's
// signature. Only the explicit narration role is public progress. The rest of the
// signature is opaque, and neither it nor an inferred role is sent to the client.
func claudeNarration(signature string) bool {
	if signature == "" {
		return false
	}
	data, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	for _, field := range [...]uint64{2, 1, 8} {
		data = signatureBytes(data, field)
		if data == nil {
			return false
		}
	}
	return bytes.Equal(data, []byte("narration"))
}

// Read one length-delimited protobuf field, validating the enclosing message and
// skipping unrelated fields without decoding their contents.
func signatureBytes(data []byte, field uint64) []byte {
	var value []byte
	for len(data) > 0 {
		key, n := binary.Uvarint(data)
		if n <= 0 || key>>3 == 0 || key>>3 > (1<<29)-1 {
			return nil
		}
		data = data[n:]
		var size uint64
		switch key & 7 {
		case 0:
			_, n = binary.Uvarint(data)
			if n <= 0 {
				return nil
			}
			size = uint64(n)
		case 1:
			size = 8
		case 2:
			size, n = binary.Uvarint(data)
			if n <= 0 {
				return nil
			}
			data = data[n:]
		case 5:
			size = 4
		default:
			return nil
		}
		if size > uint64(len(data)) {
			return nil
		}
		if key>>3 == field && key&7 == 2 {
			value = data[:size]
		}
		data = data[size:]
	}
	return value
}
