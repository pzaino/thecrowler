package fingerprints

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/crypto/blake2b"
)

func TestDigestFingerprints(t *testing.T) {
	t.Parallel()

	input := "CROWler fingerprint input"
	md5Sum := md5.Sum([]byte(input))
	shaSum := sha256.Sum256([]byte(input))
	blakeSum := blake2b.Sum256([]byte(input))

	tests := []struct {
		name string
		fp   Fingerprint
		want string
	}{
		{"JA3", JA3{}, hex.EncodeToString(md5Sum[:])},
		{"JA3S", JA3S{}, hex.EncodeToString(md5Sum[:])},
		{"HASSH", HASSH{}, hex.EncodeToString(md5Sum[:])},
		{"HASSHServer", HASSHServer{}, hex.EncodeToString(md5Sum[:])},
		{"SHA256", SHA256{}, hex.EncodeToString(shaSum[:])},
		{"CustomTLS", CustomTLS{}, hex.EncodeToString(shaSum[:])},
		{"BLAKE2", BLAKE2{}, hex.EncodeToString(blakeSum[:])},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.fp.Compute(input); got != tt.want {
				t.Fatalf("Compute(%q) = %q, want %q", input, got, tt.want)
			}
		})
	}
}

func TestFingerprintFactory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind FingerprintType
		want Fingerprint
	}{
		{TypeJA3, &JA3{}},
		{TypeJA3S, &JA3S{}},
		{TypeHASSH, &HASSH{}},
		{TypeHASSHServer, &HASSHServer{}},
		{TypeTLSH, &TLSH{}},
		{TypeSimHash, &SimHash{}},
		{TypeMinHash, &MinHash{}},
		{TypeBLAKE2, &BLAKE2{}},
		{TypeSHA256, &SHA256{}},
		{TypeCityHash, &CityHash{}},
		{TypeMurmurHash, &MurmurHash{}},
		{TypeCustomTLS, &CustomTLS{}},
		{TypeJARM, &JARM{}},
	}

	for _, tt := range tests {
		got, err := FingerprintFactory(tt.kind)
		if err != nil {
			t.Fatalf("FingerprintFactory(%d) returned error: %v", tt.kind, err)
		}
		if reflect.TypeOf(got) != reflect.TypeOf(tt.want) {
			t.Errorf("FingerprintFactory(%d) returned %T, want %T", tt.kind, got, tt.want)
		}
	}

	if got, err := FingerprintFactory(FingerprintType(999)); err == nil || got != nil {
		t.Fatalf("unknown type returned (%T, %v), want (nil, error)", got, err)
	}
}

func TestCityHashAllLengthBranches(t *testing.T) {
	t.Parallel()

	lengths := []int{0, 1, 3, 4, 8, 9, 16, 17, 32, 33, 64, 65, 129}
	seen := make(map[uint64]int)
	for _, length := range lengths {
		data := []byte(strings.Repeat("abcdefgh", (length+7)/8)[:length])
		got := CityHash64(data)
		if previous, ok := seen[got]; ok {
			t.Errorf("lengths %d and %d unexpectedly produced the same hash %x", previous, length, got)
		}
		seen[got] = length

		wantText := fmt.Sprintf("%x", got)
		if gotText := (CityHash{}).Compute(string(data)); gotText != wantText {
			t.Errorf("CityHash.Compute length %d = %q, want %q", length, gotText, wantText)
		}
	}

	if got := CityHash64(nil); got != k2 {
		t.Errorf("CityHash64(nil) = %x, want %x", got, k2)
	}
}

func TestCityHashHelpers(t *testing.T) {
	t.Parallel()

	if got := rotateRight(0x0123456789abcdef, 8); got != 0xef0123456789abcd {
		t.Errorf("rotateRight = %x", got)
	}
	if shiftMix(42) != 42 {
		t.Errorf("shiftMix(42) = %d, want 42", shiftMix(42))
	}
	data := []byte("0123456789abcdefghijklmnopqrstuv")
	if got, want := weakHashLen32WithSeeds(data, 1, 2), weakHashLen32WithSeeds(data, 1, 2); got != want {
		t.Errorf("weak hash is not deterministic: %v != %v", got, want)
	}
	if hashLen16(1, 2) == hashLen16(2, 1) {
		t.Error("hashLen16 should be order-sensitive for this input")
	}
}

func TestMurmurHash(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"":      "0",
		"hello": "248bfa47",
	}
	for input, want := range tests {
		if got := (MurmurHash{}).Compute(input); got != want {
			t.Errorf("Compute(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMinHash(t *testing.T) {
	t.Parallel()

	mh := NewMinHash(8)
	for i, value := range mh.Signature() {
		if value != math.MaxUint64 {
			t.Fatalf("initial signature[%d] = %d, want MaxUint64", i, value)
		}
	}

	mh.Push([]byte("alpha"))
	first := append([]uint64(nil), mh.Signature()...)
	if first[0] == first[1] {
		t.Fatal("different MinHash seeds produced identical values")
	}

	mh.Push([]byte("beta"))
	for i, value := range mh.Signature() {
		if value > first[i] {
			t.Errorf("signature[%d] increased from %d to %d", i, first[i], value)
		}
	}

	if hashFunction([]byte("alpha"), 1) == hashFunction([]byte("alpha"), 2) {
		t.Error("hashFunction ignored its seed")
	}
	if got := (MinHash{}).Compute("alpha"); got == "" || got != (MinHash{}).Compute("alpha") {
		t.Errorf("MinHash.Compute returned an empty or non-deterministic result: %q", got)
	}
}

func TestSimHash(t *testing.T) {
	t.Parallel()

	if got := (SimHash{}).Compute(""); got != "0" {
		t.Errorf("empty SimHash = %q, want 0", got)
	}
	one := (SimHash{}).Compute("crowler")
	repeated := (SimHash{}).Compute("crowler crowler")
	if one != repeated {
		t.Errorf("repeating one token changed SimHash: %q != %q", one, repeated)
	}
	if one == (SimHash{}).Compute("different") {
		t.Error("different tokens produced the same SimHash")
	}
}

func TestTLSHIncrementalAndCompute(t *testing.T) {
	t.Parallel()

	incremental := NewTLSH()
	incremental.Update([]byte("abc"))
	incremental.Update([]byte("abc"))

	combined := NewTLSH()
	combined.Update([]byte("abcabc"))
	if incremental.Finalize() != combined.Finalize() {
		t.Error("incremental and combined TLSH updates differ")
	}
	if incremental.total != 6 || incremental.checksum[0] != 0 {
		t.Errorf("TLSH state = total %d, checksum %d; want 6, 0", incremental.total, incremental.checksum[0])
	}
	if got := (TLSH{}).Compute("abcabc"); got != combined.Finalize() {
		t.Errorf("TLSH.Compute = %q, want %q", got, combined.Finalize())
	}
}

func TestJA4(t *testing.T) {
	t.Parallel()

	explicit := "771,2,1,1,1,example.com,1"
	want := md5.Sum([]byte(explicit))
	if got := compute(explicit); got != hex.EncodeToString(want[:]) {
		t.Errorf("compute = %q, want %x", got, want)
	}

	client := JA4{
		Version: 771, Ciphers: []uint16{1, 2}, Extensions: []uint16{3},
		SupportedGroups: []uint16{4}, SignatureAlgorithms: []uint16{5},
		SNI: "example.com", ALPN: []string{"h2"},
	}
	if got := client.Compute(""); got != client.Compute(explicit) {
		t.Errorf("JA4 field-generated hash %q differs from explicit hash %q", got, client.Compute(explicit))
	}

	server := JA4S{
		Version: 771, Ciphers: []uint16{1, 2}, Extensions: []uint16{3},
		SupportedGroups: []uint16{4}, SignatureAlgorithms: []uint16{5},
		SNI: "example.com", ALPN: []string{"h2"},
	}
	if got := server.Compute(""); got != server.Compute(explicit) {
		t.Errorf("JA4S field-generated hash %q differs from explicit hash %q", got, server.Compute(explicit))
	}
}

func TestJARM(t *testing.T) {
	t.Parallel()

	empty := "|||,|||,|||,|||,|||,|||,|||,|||,|||,|||"
	if got := (JARM{}).Compute(empty); got != strings.Repeat("0", 62) {
		t.Errorf("empty JARM = %q", got)
	}

	raw := "1301|TLS1.3|h2|ext,0004|TLS1.2|http/1.1|other"
	got := jarmHash(raw)
	wantPrefix := cipherBytes("1301") + versionByte("TLS1.3") +
		cipherBytes("0004") + versionByte("TLS1.2")
	if !strings.HasPrefix(got, wantPrefix) || len(got) != len(wantPrefix)+32 {
		t.Errorf("jarmHash(%q) = %q, want prefix %q and length %d", raw, got, wantPrefix, len(wantPrefix)+32)
	}
	if got := (JARM{}).Compute("malformed"); len(got) != 35 {
		t.Errorf("malformed JARM result length = %d, want 35", len(got))
	}
}

func TestJARMHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cipher string
		want   string
	}{
		{"", "00"},
		{"0004", "01"},
		{"1305", "45"},
		{"ffff", "46"},
	}
	for _, tt := range tests {
		if got := cipherBytes(tt.cipher); got != tt.want {
			t.Errorf("cipherBytes(%q) = %q, want %q", tt.cipher, got, tt.want)
		}
	}

	for input, want := range map[string]string{
		"": "0", "bad": "0", "TLS1.2": "c", "TLS1.9": "0",
	} {
		if got := versionByte(input); got != want {
			t.Errorf("versionByte(%q) = %q, want %q", input, got, want)
		}
	}

	sum := sha256.Sum256([]byte("abc"))
	if got := sha256Sum("abc"); got != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256Sum = %q, want %x", got, sum)
	}
}
