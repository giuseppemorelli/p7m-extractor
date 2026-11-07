package p7m

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

// SignerDesc è la descrizione di un firmatario, pensata per essere mostrata
// all'utente (riga di comando, interfaccia locale, versione web).
type SignerDesc struct {
	HasCert      bool       `json:"hasCert"`
	Name         string     `json:"name,omitempty"`
	SerialNumber string     `json:"serialNumber,omitempty"` // es. TINIT-<codice fiscale>
	Organization string     `json:"organization,omitempty"`
	Issuer       string     `json:"issuer,omitempty"`
	NotBefore    *time.Time `json:"notBefore,omitempty"`
	NotAfter     *time.Time `json:"notAfter,omitempty"`
	SigningTime  *time.Time `json:"signingTime,omitempty"`
}

func DescribeSigner(s Signer) SignerDesc {
	var d SignerDesc
	if c := s.Cert; c != nil {
		d.HasCert = true
		d.Name = c.Subject.CommonName
		if d.Name == "" {
			d.Name = c.Subject.String()
		}
		d.SerialNumber = c.Subject.SerialNumber
		d.Organization = strings.Join(c.Subject.Organization, ", ")
		d.Issuer = c.Issuer.CommonName
		if d.Issuer == "" {
			d.Issuer = c.Issuer.String()
		}
		nb, na := c.NotBefore, c.NotAfter
		d.NotBefore, d.NotAfter = &nb, &na
	}
	if !s.SigningTime.IsZero() {
		t := s.SigningTime.UTC()
		d.SigningTime = &t
	}
	return d
}

// DescribeLayers descrive i firmatari di ogni busta, dalla più esterna.
func DescribeLayers(layers []*Envelope) [][]SignerDesc {
	out := make([][]SignerDesc, len(layers))
	for i, env := range layers {
		out[i] = []SignerDesc{}
		for _, s := range env.Signers {
			out[i] = append(out[i], DescribeSigner(s))
		}
	}
	return out
}

// OutputName ricava il nome del file estratto: "fattura.xml.p7m" diventa
// "fattura.xml". Se il nome non contiene l'estensione originale, la
// ricava dal contenuto. Accetta percorsi con "/" e con "\".
func OutputName(in string, content []byte) string {
	base := in
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	stripped := base
	for {
		ext := path.Ext(stripped)
		if !strings.EqualFold(ext, ".p7m") || len(ext) == len(stripped) {
			break
		}
		stripped = strings.TrimSuffix(stripped, ext)
	}

	if stripped != base && path.Ext(stripped) != "" {
		return stripped
	}
	if ext := SniffExt(content); ext != "" {
		return stripped + ext
	}
	if stripped != base {
		return stripped
	}
	return base + ".estratto"
}

// SniffExt prova a riconoscere il tipo di file dai primi byte.
func SniffExt(b []byte) string {
	trimmed := bytes.TrimLeft(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), " \t\r\n")
	switch {
	case bytes.HasPrefix(b, []byte("%PDF")):
		return ".pdf"
	case bytes.HasPrefix(trimmed, []byte("<?xml")):
		return ".xml"
	case bytes.HasPrefix(b, []byte("PK\x03\x04")):
		return ".zip"
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return ".png"
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		return ".jpg"
	case bytes.HasPrefix(b, []byte("GIF8")):
		return ".gif"
	case bytes.HasPrefix(b, []byte("II*\x00")), bytes.HasPrefix(b, []byte("MM\x00*")):
		return ".tif"
	case bytes.HasPrefix(b, []byte("{\\rtf")):
		return ".rtf"
	case bytes.HasPrefix(b, []byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1")):
		return ".doc"
	}
	return ""
}

// PreviewType indica se un file può essere mostrato in sicurezza nel
// browser e con quale Content-Type. XML e simili vengono mostrati come testo
// semplice, così un documento non può mai eseguire script.
func PreviewType(name string) (string, bool) {
	switch strings.ToLower(path.Ext(name)) {
	case ".pdf":
		return "application/pdf", true
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".xml", ".txt", ".csv", ".json":
		return "text/plain; charset=utf-8", true
	}
	return "", false
}

// SanitizeName riduce un nome ricevuto dall'esterno a un nome di file semplice.
func SanitizeName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" {
		name = "documento.p7m"
	}
	return name
}

// File è un documento estratto, da inserire in un archivio ZIP.
type File struct {
	Name    string
	Content []byte
}

// WriteZip scrive un archivio ZIP con i file indicati, rendendo univoci i
// nomi duplicati ("a.xml", "a (2).xml", ...).
func WriteZip(w io.Writer, files []File) error {
	zw := zip.NewWriter(w)
	used := map[string]int{}
	now := time.Now()
	for _, f := range files {
		zf, err := zw.CreateHeader(&zip.FileHeader{
			Name:     uniqueName(f.Name, used),
			Method:   zip.Deflate,
			Modified: now,
		})
		if err != nil {
			return err
		}
		if _, err := zf.Write(f.Content); err != nil {
			return err
		}
	}
	return zw.Close()
}

func uniqueName(name string, used map[string]int) string {
	key := strings.ToLower(name)
	used[key]++
	if used[key] == 1 {
		return name
	}
	ext := path.Ext(name)
	for n := used[key]; ; n++ {
		candidate := fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n, ext)
		if used[strings.ToLower(candidate)] == 0 {
			used[strings.ToLower(candidate)] = 1
			return candidate
		}
	}
}
