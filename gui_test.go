package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const testPort = 4242

func guiRequest(t *testing.T, s *guiServer, method, target string, body io.Reader, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Host = "127.0.0.1:4242"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	return rec
}

func TestGUISecurity(t *testing.T) {
	s := newGUIServer(testPort)

	// Pagina principale accessibile, API no senza token.
	if rec := guiRequest(t, s, "GET", "/", nil, nil); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), `data-mode="local"`) {
		t.Fatalf("index: %d, modalità locale non impostata", rec.Code)
	}
	if rec := guiRequest(t, s, "POST", "/api/extract", nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("extract senza token: %d", rec.Code)
	}
	if rec := guiRequest(t, s, "POST", "/api/extract", nil, map[string]string{"X-Token": "sbagliato"}); rec.Code != http.StatusForbidden {
		t.Fatalf("extract con token errato: %d", rec.Code)
	}

	// Host diverso (DNS rebinding) rifiutato anche con token valido.
	req := httptest.NewRequest("GET", "/api/file/x?t="+s.token, nil)
	req.Host = "attaccante.example:4242"
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("host estraneo: %d", rec.Code)
	}
}

func TestGUIFlow(t *testing.T) {
	s := newGUIServer(testPort)
	auth := map[string]string{"X-Token": s.token}
	want := readTestdata(t, "fattura.xml")

	var ids []string
	for _, name := range []string{"fattura.xml.p7m", "fattura_ber.xml.p7m", "fattura_doppia.xml.p7m.p7m"} {
		rec := guiRequest(t, s, "POST", "/api/extract?name="+url.QueryEscape("C:\\tmp\\fattura.xml.p7m"),
			bytes.NewReader(readTestdata(t, name)), auth)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
		var res guiResult
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if res.Name != "fattura.xml" || res.Size != len(want) || !res.Preview {
			t.Fatalf("%s: risultato inatteso %+v", name, res)
		}
		if res.Layers[0][0].Name != "Mario Rossi" || res.Layers[0][0].SigningTime == nil {
			t.Fatalf("%s: firmatario inatteso %+v", name, res.Layers)
		}
		ids = append(ids, res.ID)
	}

	// Errore leggibile per file non firmati.
	rec := guiRequest(t, s, "POST", "/api/extract?name=x.txt", strings.NewReader("ciao"), auth)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "non è una busta") {
		t.Fatalf("file non p7m: %d %s", rec.Code, rec.Body)
	}

	// Download.
	rec = guiRequest(t, s, "GET", "/api/file/"+ids[0]+"?t="+s.token, nil, nil)
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatalf("download: %d", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("Content-Disposition: %q", cd)
	}

	// Anteprima XML: mostrata come testo semplice e isolata.
	rec = guiRequest(t, s, "GET", "/api/file/"+ids[0]+"?inline=1&t="+s.token, nil, nil)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("anteprima Content-Type: %q", ct)
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("anteprima senza CSP sandbox")
	}

	// ZIP con nomi resi univoci.
	form := url.Values{"ids": {strings.Join(ids, ",")}}
	rec = guiRequest(t, s, "POST", "/api/zip?t="+s.token, strings.NewReader(form.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if rec.Code != 200 {
		t.Fatalf("zip: %d %s", rec.Code, rec.Body)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		rc, _ := f.Open()
		got, _ := io.ReadAll(rc)
		rc.Close()
		if !bytes.Equal(got, want) {
			t.Fatalf("zip: contenuto di %s diverso", f.Name)
		}
	}
	if strings.Join(names, "|") != "fattura.xml|fattura (2).xml|fattura (3).xml" {
		t.Fatalf("nomi nello zip: %v", names)
	}

	// Svuota ed esci.
	if rec := guiRequest(t, s, "POST", "/api/clear", nil, auth); rec.Code != http.StatusNoContent {
		t.Fatalf("clear: %d", rec.Code)
	}
	if rec := guiRequest(t, s, "GET", "/api/file/"+ids[0]+"?t="+s.token, nil, nil); rec.Code != 404 {
		t.Fatalf("file dopo clear: %d", rec.Code)
	}
	guiRequest(t, s, "POST", "/api/quit", nil, auth)
	guiRequest(t, s, "POST", "/api/quit", nil, auth) // un secondo clic non deve andare in panic
	select {
	case <-s.quit:
	default:
		t.Fatal("quit non segnalato")
	}
}
