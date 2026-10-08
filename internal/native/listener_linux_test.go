package native

import (
	"encoding/binary"
	"testing"
)

func TestSocketDiagnosisRequiresExplicitCapability(t *testing.T) {
	// Linux UAPI inet_diag_msg plus one padded SKV6ONLY attribute.
	data := make([]byte, 80)
	data[0] = 10
	data[1] = 10
	binary.BigEndian.PutUint16(data[4:], 1234)
	binary.NativeEndian.PutUint32(data[68:], 42)
	binary.NativeEndian.PutUint16(data[72:], 5)
	binary.NativeEndian.PutUint16(data[74:], 11)
	matches, only, err := diagnosticIPv6Only(data, 42, 1234)
	if err != nil || !matches || only {
		t.Fatal("explicit dual-stack capability rejected", matches, only, err)
	}
	data[76] = 1
	if matches, only, err := diagnosticIPv6Only(data, 42, 1234); err != nil || !matches || !only {
		t.Fatal("IPv6-only capability lost", matches, only, err)
	}
	for _, bad := range [][]byte{data[:71], data[:72], append(append([]byte(nil), data[:76]...), 2, 0, 0, 0)} {
		if _, _, err := diagnosticIPv6Only(bad, 42, 1234); err == nil {
			t.Fatal("missing/malformed capability accepted")
		}
	}
	if matches, _, err := diagnosticIPv6Only(data, 43, 1234); err != nil || matches {
		t.Fatal("unrelated inode accepted", matches, err)
	}
	if matches, _, err := diagnosticIPv6Only(data, 42, 1235); err != nil || matches {
		t.Fatal("different port accepted", matches, err)
	}
}
