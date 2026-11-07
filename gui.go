package main

// Interfaccia grafica locale: un piccolo server HTTP in ascolto solo su
// 127.0.0.1 che serve la pagina web incorporata nell'eseguibile. La stessa
// pagina funziona anche come sito statico con WebAssembly (vedi cmd/wasm):
// qui viene marcata con data-mode="local" e usa le API di questo server.
//
// Sicurezza:
//   - il server accetta connessioni solo da 127.0.0.1;
//   - ogni chiamata alle API richiede un token casuale generato all'avvio,
//     quindi altri siti aperti nel browser non possono usarle;
//   - l'header Host viene controllato per bloccare il DNS rebinding;
//   - i documenti estratti vengono mostrati in anteprima solo se di tipo
//     sicuro (PDF, immagini, testo), così un file HTML/SVG/XML malevolo non
//     può eseguire script nell'origine dell'applicazione.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/giuseppemorelli/p7m-extractor/p7m"
)

//go:embed web/index.html
var webIndexHTML []byte

// La pagina in modalità locale.
var indexHTML = bytes.Replace(webIndexHTML, []byte(`data-mode="web"`), []byte(`data-mode="local"`), 1)

const (
	maxUploadSize = 512 << 20 // dimensione massima di un singolo file p7m
	maxFormSize   = 1 << 20
)

type guiServer struct {
	token string
	hosts map[string]bool

	mu    sync.Mutex
	files map[string]p7m.File

	quitOnce sync.Once
	quit     chan struct{}
}

type guiResult struct {
	ID      string             `json:"id"`
	Name    string             `json:"name"`
	Size    int                `json:"size"`
	Preview bool               `json:"preview"`
	Layers  [][]p7m.SignerDesc `json:"layers"`
}

func runGUI(openBrowser bool, stdout, stderr io.Writer) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(stderr, "errore: impossibile avviare l'interfaccia:", err)
		return 1
	}
	port := ln.Addr().(*net.TCPAddr).Port

	s := newGUIServer(port)
	srv := &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go srv.Serve(ln)

	url := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", port, s.token)
	fmt.Fprintf(stdout, "p7m-extractor %s - interfaccia grafica\n\n", version)
	fmt.Fprintf(stdout, "Se il browser non si apre, copia questo indirizzo:\n  %s\n\n", url)
	fmt.Fprintln(stdout, "Per chiudere il programma usa il pulsante \"Esci\" nella pagina,")
	fmt.Fprintln(stdout, "premi Ctrl+C oppure chiudi questa finestra.")
	if openBrowser {
		if err := openURL(url); err != nil {
			fmt.Fprintln(stderr, "impossibile aprire il browser:", err)
		}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-s.quit:
	case <-sig:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	return 0
}

func newGUIServer(port int) *guiServer {
	p := strconv.Itoa(port)
	return &guiServer{
		token: randomHex(16),
		hosts: map[string]bool{"127.0.0.1:" + p: true, "localhost:" + p: true},
		files: map[string]p7m.File{},
		quit:  make(chan struct{}),
	}
}

func (s *guiServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/extract", s.handleExtract)
	mux.HandleFunc("/api/file/", s.handleFile)
	mux.HandleFunc("/api/zip", s.handleZip)
	mux.HandleFunc("/api/clear", s.handleClear)
	mux.HandleFunc("/api/quit", s.handleQuit)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hosts[r.Host] {
			http.Error(w, "host non consentito", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") && !s.validToken(r) {
			jsonError(w, http.StatusForbidden, "token non valido: riapri l'interfaccia dal programma")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *guiServer) validToken(r *http.Request) bool {
	t := r.Header.Get("X-Token")
	if t == "" {
		t = r.URL.Query().Get("t")
	}
	return subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

func (s *guiServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; "+
		"style-src 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; "+
		"form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Write(indexHTML)
}

func (s *guiServer) handleExtract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "metodo non consentito")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUploadSize))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			jsonError(w, http.StatusRequestEntityTooLarge, "file troppo grande")
			return
		}
		jsonError(w, http.StatusBadRequest, "lettura del file non riuscita")
		return
	}

	content, layers, err := p7m.Extract(data, true)
	if err != nil {
		jsonError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	name := p7m.OutputName(p7m.SanitizeName(r.URL.Query().Get("name")), content)
	id := randomHex(12)
	s.mu.Lock()
	s.files[id] = p7m.File{Name: name, Content: content}
	s.mu.Unlock()

	_, preview := p7m.PreviewType(name)
	writeJSON(w, http.StatusOK, guiResult{
		ID:      id,
		Name:    name,
		Size:    len(content),
		Preview: preview,
		Layers:  p7m.DescribeLayers(layers),
	})
}

func (s *guiServer) handleFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "metodo non consentito", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/file/")
	s.mu.Lock()
	f, ok := s.files[id]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "file non trovato (forse l'elenco è stato svuotato)", http.StatusNotFound)
		return
	}

	h := w.Header()
	ctype, preview := p7m.PreviewType(f.Name)
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" && preview {
		disposition = "inline"
		if ctype != "application/pdf" {
			// I contenuti non PDF vengono isolati in un'origine a parte.
			h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; style-src 'unsafe-inline'")
		}
	} else {
		ctype = "application/octet-stream"
	}
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Name}))
	h.Set("Content-Length", strconv.Itoa(len(f.Content)))
	if r.Method == http.MethodGet {
		w.Write(f.Content)
	}
}

func (s *guiServer) handleZip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "metodo non consentito", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "richiesta non valida", http.StatusBadRequest)
		return
	}

	var files []p7m.File
	s.mu.Lock()
	for _, id := range strings.Split(r.PostFormValue("ids"), ",") {
		if f, ok := s.files[id]; ok {
			files = append(files, f)
		}
	}
	s.mu.Unlock()
	if len(files) == 0 {
		http.Error(w, "nessun file da scaricare", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment",
		map[string]string{"filename": "documenti-estratti.zip"}))
	p7m.WriteZip(w, files)
}

func (s *guiServer) handleClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "metodo non consentito")
		return
	}
	s.mu.Lock()
	s.files = map[string]p7m.File{}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *guiServer) handleQuit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "metodo non consentito")
		return
	}
	w.WriteHeader(http.StatusNoContent)
	s.quitOnce.Do(func() { close(s.quit) })
}

func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
