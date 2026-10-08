package contentrepo

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The fixtures are produced by OpenSSL (testdata/gen.sh) over the worked
// example message from the SAP spec, so they test interoperability rather
// than round-tripping our own encoder.
func TestSecKeyFixtures(t *testing.T) {
	msg := fixture(t, "message.txt")
	dsaCert, err := parseCertificateLenient(fixture(t, "dsa_cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	rsaCert, err := parseCertificateLenient(fixture(t, "rsa_cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseCertificateLenient(fixture(t, "dsa_cert.der")); err != nil {
		t.Fatalf("DER certificate: %v", err)
	}
	cases := []struct {
		file string
		cert any
	}{
		{"dsa_sha1.p7", dsaCert.PublicKey},
		{"dsa_sha1_noattr.p7", dsaCert.PublicKey},
		{"rsa_sha256.p7", rsaCert.PublicKey},
		{"rsa_md5_noattr.p7", rsaCert.PublicKey},
		{"rsa_ripemd160.p7", rsaCert.PublicKey},
		{"rsa_sha256_embedded.p7", rsaCert.PublicKey},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			sig, err := parseSignature(fixture(t, c.file))
			if err != nil {
				t.Fatal(err)
			}
			if err := sig.verify(msg, c.cert); err != nil {
				t.Fatalf("valid signature rejected: %v", err)
			}
			tampered := append([]byte{}, msg...)
			tampered[3] ^= 1
			if err := sig.verify(tampered, c.cert); err == nil {
				t.Fatal("tampered message accepted")
			}
			other := rsaCert.PublicKey
			if c.cert == rsaCert.PublicKey {
				other = dsaCert.PublicKey
			}
			if err := sig.verify(msg, other); err == nil {
				t.Fatal("signature accepted with the wrong key")
			}
		})
	}
}

func TestSignedMessageFollowsURLOrder(t *testing.T) {
	// URL from the spec's "URL Encoding" example (pVersion and secKey are not signed).
	q := "get&pVersion=0046&contRep=K1&docId=361A524A3ECB5459E0000800099245EC&accessMode=r&authId=pawdf054_BCE_26&expiration=19981104091537&secKey=g3AhQg%3D%3D"
	msgs := signedMessages("get", q)
	if string(msgs[0]) != string(fixture(t, "message.txt")) {
		t.Fatalf("message %q", msgs[0])
	}
	// Order follows the URL; compId is not signed for get but is for delete.
	q2 := "delete&docId=D1&compId=data&contRep=K1&expiration=20300101000000&authId=CN%3DS4H&accessMode=d"
	if got := string(signedMessages("delete", q2)[0]); got != "D1dataK120300101000000CN=S4Hd" {
		t.Fatalf("delete message %q", got)
	}
	if got := string(signedMessages("get", q2)[0]); got != "D1K120300101000000CN=S4Hd" {
		t.Fatalf("get message %q", got)
	}
	if raw := signedMessages("delete", q2); len(raw) != 2 || string(raw[1]) != "D1dataK120300101000000CN%3DS4Hd" {
		t.Fatalf("raw variant %q", raw)
	}
}

func TestNameEncodingRoundTrip(t *testing.T) {
	for _, in := range []string{"data", "data1", "file:name?.pdf", "~x", " lead", "trail.", "a%b#c", "x_vti_y", `q"*<>|\/`} {
		enc, err := encodeName(in)
		if err != nil {
			t.Fatal(err)
		}
		if decodeName(enc) != in {
			t.Errorf("round trip %q -> %q -> %q", in, enc, decodeName(enc))
		}
		if enc[0] == '~' || enc == sidecarName {
			t.Errorf("%q encodes into the reserved namespace", in)
		}
	}
}

func TestSearchForwardAcrossChunks(t *testing.T) {
	data := make([]byte, 3<<20)
	for i := range data {
		data[i] = 'x'
	}
	copy(data[(1<<20)-2:], "NEEDLE") // spans the 1 MiB read boundary
	copy(data[2<<20:], "needle")
	hits, err := searchForward(bytesReader(data), 100, []byte("needle"), false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0] != 100+(1<<20)-2 || hits[1] != 100+(2<<20) {
		t.Fatalf("hits %v", hits)
	}
	if hits, _ := searchForward(bytesReader(data), 0, []byte("needle"), true, 10); len(hits) != 1 {
		t.Fatalf("case-sensitive hits %v", hits)
	}
	if got := searchBackward([]byte("abcabcabc"), 0, []byte("abc"), true, 2); len(got) != 2 || got[0] != 6 || got[1] != 3 {
		t.Fatalf("backward %v", got)
	}
}

type sliceReader struct {
	b []byte
	n int
}

// bytesReader returns data in 1 MiB reads, like a network body.
func bytesReader(b []byte) *sliceReader { return &sliceReader{b: b} }

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.n >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.n:min(len(r.b), r.n+(1<<20))])
	r.n += n
	return n, nil
}

// Signature checks run on every signed request; these show their CPU cost.
func BenchmarkVerifyDSA(b *testing.B)     { benchVerify(b, "dsa_sha1.p7", "dsa_cert.pem") }
func BenchmarkVerifyRSA2048(b *testing.B) { benchVerify(b, "rsa_sha256.p7", "rsa_cert.pem") }

func benchVerify(b *testing.B, sigFile, certFile string) {
	read := func(n string) []byte {
		d, err := os.ReadFile(filepath.Join("testdata", n))
		if err != nil {
			b.Fatal(err)
		}
		return d
	}
	msg, der := read("message.txt"), read(sigFile)
	cert, err := parseCertificateLenient(read(certFile))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		sig, err := parseSignature(der)
		if err != nil || sig.verify(msg, cert.PublicKey) != nil {
			b.Fatal("verify failed")
		}
	}
}
