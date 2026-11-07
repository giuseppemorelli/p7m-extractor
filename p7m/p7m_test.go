package p7m

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readTestdata(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestExtractFixtures(t *testing.T) {
	want := readTestdata(t, "fattura.xml")
	cases := []struct {
		file   string
		layers int
	}{
		{"fattura.xml.p7m", 1},            // DER
		{"fattura_ber.xml.p7m", 1},        // BER, lunghezza indefinita, contenuto a blocchi
		{"fattura_pem.xml.p7m", 1},        // PEM
		{"fattura_b64.xml.p7m", 1},        // Base64 senza intestazioni
		{"fattura_doppia.xml.p7m.p7m", 2}, // doppia firma
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			got, layers, err := Extract(readTestdata(t, tc.file), true)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("contenuto estratto diverso dall'originale (%d byte invece di %d)", len(got), len(want))
			}
			if len(layers) != tc.layers {
				t.Fatalf("buste: %d, attese %d", len(layers), tc.layers)
			}
			for _, env := range layers {
				if len(env.Signers) != 1 {
					t.Fatalf("firmatari: %d, atteso 1", len(env.Signers))
				}
				s := env.Signers[0]
				if s.Cert == nil {
					t.Fatal("certificato del firmatario non trovato")
				}
				if s.Cert.Subject.CommonName != "Mario Rossi" ||
					s.Cert.Subject.SerialNumber != "TINIT-RSSMRA80A01H501U" {
					t.Fatalf("soggetto inatteso: %s", s.Cert.Subject)
				}
				if s.SigningTime.IsZero() {
					t.Fatal("signingTime assente")
				}
			}
		})
	}
}

func TestExtractNotNested(t *testing.T) {
	got, layers, err := Extract(readTestdata(t, "fattura_doppia.xml.p7m.p7m"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 1 || !bytes.Equal(got, readTestdata(t, "fattura.xml.p7m")) {
		t.Fatal("con nested=false deve essere aperta solo la busta esterna")
	}
}

func TestExtractErrors(t *testing.T) {
	if _, _, err := Extract(readTestdata(t, "detached.p7s"), true); !errors.Is(err, ErrDetached) {
		t.Fatalf("detached: errore inatteso %v", err)
	}
	if _, _, err := Extract(readTestdata(t, "fattura.xml"), true); !errors.Is(err, ErrNotP7M) {
		t.Fatalf("file non firmato: errore inatteso %v", err)
	}
	full := readTestdata(t, "fattura.xml.p7m")
	if _, _, err := Extract(full[:len(full)/2], true); err == nil {
		t.Fatal("file troncato: atteso errore")
	}
}

func TestBERDepthLimit(t *testing.T) {
	// 100 SEQUENCE a lunghezza indefinita annidate.
	data := append(bytes.Repeat([]byte{0x30, 0x80}, 100), bytes.Repeat([]byte{0, 0}, 100)...)
	if _, err := parseBER(data); !errors.Is(err, errTooDeep) {
		t.Fatalf("atteso errTooDeep, ottenuto %v", err)
	}
}

func TestDescribeLayers(t *testing.T) {
	_, layers, err := Extract(readTestdata(t, "fattura_doppia.xml.p7m.p7m"), true)
	if err != nil {
		t.Fatal(err)
	}
	d := DescribeLayers(layers)
	if len(d) != 2 || d[1][0].Name != "Mario Rossi" || d[1][0].Organization != "Esempio S.r.l." ||
		d[1][0].NotBefore == nil || d[1][0].SigningTime == nil {
		t.Fatalf("descrizione inattesa: %+v", d)
	}
}

func TestOutputName(t *testing.T) {
	xml := []byte("<?xml version=\"1.0\"?><a/>")
	pdf := []byte("%PDF-1.7")
	cases := []struct {
		in      string
		content []byte
		want    string
	}{
		{"dir/fattura.xml.p7m", xml, "fattura.xml"},
		{`C:\dir\fattura.xml.p7m`, xml, "fattura.xml"},
		{"fattura.xml.P7M", xml, "fattura.xml"},
		{"fattura.xml.p7m.p7m", xml, "fattura.xml"},
		{"documento.p7m", pdf, "documento.pdf"},
		{"documento.p7m", []byte("???"), "documento"},
		{"busta.bin", pdf, "busta.bin.pdf"},
		{"busta.bin", []byte("???"), "busta.bin.estratto"},
		{".p7m", pdf, ".p7m.pdf"},
	}
	for _, tc := range cases {
		if got := OutputName(tc.in, tc.content); got != tc.want {
			t.Errorf("OutputName(%q) = %q, atteso %q", tc.in, got, tc.want)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		`C:\Users\a\fattura.xml.p7m`: "fattura.xml.p7m",
		"../../etc/passwd":           "passwd",
		"a\x00b:c.p7m":               "a_b_c.p7m",
		"":                           "documento.p7m",
		"..":                         "documento.p7m",
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, atteso %q", in, got, want)
		}
	}
}

func TestWriteZipUniqueNames(t *testing.T) {
	var buf bytes.Buffer
	files := []File{{"a.xml", []byte("1")}, {"A.xml", []byte("2")}, {"a (2).xml", []byte("3")}, {"b", []byte("4")}}
	if err := WriteZip(&buf, files); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.xml", "A (2).xml", "a (2) (2).xml", "b"} {
		if !strings.Contains(buf.String(), name) {
			t.Errorf("nome %q assente nello zip", name)
		}
	}
}

func FuzzExtract(f *testing.F) {
	for _, name := range []string{"fattura.xml.p7m", "fattura_ber.xml.p7m", "detached.p7s"} {
		f.Add(readTestdata(f, name))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		Extract(data, true) // non deve mai andare in panic
	})
}
