package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRunCLI(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "fattura.xml.p7m")
	if err := os.WriteFile(in, readTestdata(t, "fattura_ber.xml.p7m"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-i", in}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "fattura.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, readTestdata(t, "fattura.xml")) {
		t.Fatal("contenuto estratto diverso dall'originale")
	}
	if !strings.Contains(stdout.String(), "Mario Rossi") {
		t.Fatalf("output -i senza firmatario:\n%s", stdout.String())
	}

	// Una seconda estrazione non deve sovrascrivere senza -f.
	stderr.Reset()
	if code := run([]string{in}, nil, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "esiste già") {
		t.Fatalf("atteso rifiuto di sovrascrittura, exit %d: %s", code, stderr.String())
	}
	if code := run([]string{"-f", "-q", in}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("-f: exit %d", code)
	}

	// Output su stdout: nessun messaggio deve finire insieme al documento.
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-o", "-", in}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !bytes.Equal(stdout.Bytes(), readTestdata(t, "fattura.xml")) {
		t.Fatal("stdout non contiene esattamente il documento")
	}

	// Input da stdin.
	out := filepath.Join(dir, "da_stdin.xml")
	stdin := bytes.NewReader(readTestdata(t, "fattura_pem.xml.p7m"))
	if code := run([]string{"-o", out, "-"}, stdin, &stdout, &stderr); code != 0 {
		t.Fatalf("stdin: exit %d: %s", code, stderr.String())
	}
}
