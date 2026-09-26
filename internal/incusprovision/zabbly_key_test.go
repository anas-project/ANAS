package incusprovision

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// The compiled key must be the one Zabbly publishes for its Incus repository
// (fingerprint from https://github.com/zabbly/incus). Replacing it is a
// deliberate trust change, never a routine refresh.
func TestZabblyArchiveKeyHasThePublishedFingerprint(t *testing.T) {
	lines := strings.Split(strings.TrimRight(string(zabblyArchiveKey), "\n"), "\n")
	if lines[0] != "-----BEGIN PGP PUBLIC KEY BLOCK-----" || lines[len(lines)-1] != "-----END PGP PUBLIC KEY BLOCK-----" {
		t.Fatal("embedded key is not a single ASCII-armored public key block")
	}
	var body strings.Builder
	for _, line := range lines[1 : len(lines)-1] {
		if line == "" || strings.HasPrefix(line, "=") || strings.Contains(line, ": ") {
			continue
		}
		body.WriteString(line)
	}
	data, err := base64.StdEncoding.DecodeString(body.String())
	if err != nil || len(data) < 4 {
		t.Fatal("embedded key armor does not decode")
	}
	// The first packet is the primary public key (tag 6). A v4 fingerprint
	// is SHA-1 over 0x99, a two-byte length and the packet body.
	header := data[0]
	var tag, length, offset int
	switch {
	case header&0xc0 == 0xc0:
		tag = int(header & 0x3f)
		switch first := int(data[1]); {
		case first < 192:
			length, offset = first, 2
		case first < 224:
			length, offset = ((first-192)<<8)+int(data[2])+192, 3
		default:
			t.Fatal("unexpected primary key packet length encoding")
		}
	case header&0xc0 == 0x80:
		tag = int(header>>2) & 0x0f
		switch header & 0x03 {
		case 0:
			length, offset = int(data[1]), 2
		case 1:
			length, offset = int(data[1])<<8|int(data[2]), 3
		default:
			t.Fatal("unexpected primary key packet length encoding")
		}
	default:
		t.Fatal("embedded key does not start with an OpenPGP packet")
	}
	if tag != 6 || offset+length > len(data) || data[offset] != 4 {
		t.Fatal("embedded key does not start with a v4 primary public key")
	}
	packet := data[offset : offset+length]
	sum := sha1.Sum(append([]byte{0x99, byte(length >> 8), byte(length)}, packet...))
	if got := strings.ToUpper(hex.EncodeToString(sum[:])); got != zabblyKeyFingerprint {
		t.Fatalf("embedded key fingerprint = %s, want %s", got, zabblyKeyFingerprint)
	}
}

func TestZabblySourceCarriesTheKeyInline(t *testing.T) {
	source := string(zabblySource("trixie"))
	if !strings.HasPrefix(source, "Types: deb\nURIs: https://pkgs.zabbly.com/incus/lts-7.0\nSuites: trixie\nComponents: main\n") {
		t.Fatalf("unexpected source stanza:\n%s", source)
	}
	if !strings.Contains(source, "\nSigned-By:\n -----BEGIN PGP PUBLIC KEY BLOCK-----\n .\n") ||
		!strings.HasSuffix(source, " -----END PGP PUBLIC KEY BLOCK-----\n") {
		t.Fatal("key is not embedded as a deb822 multi-line field")
	}
	for _, line := range strings.Split(strings.TrimSuffix(source, "\n"), "\n") {
		if line == "" {
			t.Fatal("an empty line would end the deb822 stanza inside the key")
		}
	}
}
