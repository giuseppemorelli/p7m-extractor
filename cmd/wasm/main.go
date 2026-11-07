//go:build js && wasm

// Versione WebAssembly dell'estrattore, per la pubblicazione come sito
// statico (es. Cloudflare Pages). L'estrazione avviene interamente nel
// browser dell'utente: i file non vengono mai inviati a un server.
//
// Compilazione:
//
//	GOOS=js GOARCH=wasm go build -o p7m.wasm ./cmd/wasm
//
// Funzioni esposte a JavaScript:
//
//	p7mExtract(data: Uint8Array, name: string)
//	    -> { meta: string (JSON), content: Uint8Array } oppure { error: string }
//	p7mZip(names: string[], contents: Uint8Array[]) -> Uint8Array
package main

import (
	"bytes"
	"encoding/json"
	"syscall/js"

	"github.com/giuseppemorelli/p7m-extractor/p7m"
)

type result struct {
	Name    string             `json:"name"`
	Size    int                `json:"size"`
	Preview bool               `json:"preview"`
	MIME    string             `json:"mime,omitempty"`
	Layers  [][]p7m.SignerDesc `json:"layers"`
}

func main() {
	js.Global().Set("p7mExtract", js.FuncOf(extract))
	js.Global().Set("p7mZip", js.FuncOf(makeZip))
	select {} // il modulo resta attivo finché la pagina è aperta
}

func extract(_ js.Value, args []js.Value) (ret any) {
	defer func() {
		if r := recover(); r != nil {
			ret = errorValue("errore interno durante l'estrazione")
		}
	}()
	if len(args) < 2 {
		return errorValue("argomenti mancanti")
	}
	data := bytesFromJS(args[0])

	content, layers, err := p7m.Extract(data, true)
	if err != nil {
		return errorValue(err.Error())
	}

	name := p7m.OutputName(p7m.SanitizeName(args[1].String()), content)
	mime, preview := p7m.PreviewType(name)
	meta, err := json.Marshal(result{
		Name:    name,
		Size:    len(content),
		Preview: preview,
		MIME:    mime,
		Layers:  p7m.DescribeLayers(layers),
	})
	if err != nil {
		return errorValue(err.Error())
	}
	return map[string]any{
		"meta":    string(meta),
		"content": bytesToJS(content),
	}
}

func makeZip(_ js.Value, args []js.Value) (ret any) {
	defer func() {
		if r := recover(); r != nil {
			ret = errorValue("errore interno durante la creazione dello ZIP")
		}
	}()
	if len(args) < 2 {
		return errorValue("argomenti mancanti")
	}
	names, contents := args[0], args[1]
	files := make([]p7m.File, names.Length())
	for i := range files {
		files[i] = p7m.File{Name: names.Index(i).String(), Content: bytesFromJS(contents.Index(i))}
	}
	var buf bytes.Buffer
	if err := p7m.WriteZip(&buf, files); err != nil {
		return errorValue(err.Error())
	}
	return bytesToJS(buf.Bytes())
}

func bytesFromJS(v js.Value) []byte {
	b := make([]byte, v.Get("length").Int())
	js.CopyBytesToGo(b, v)
	return b
}

func bytesToJS(b []byte) js.Value {
	v := js.Global().Get("Uint8Array").New(len(b))
	js.CopyBytesToJS(v, b)
	return v
}

func errorValue(msg string) map[string]any {
	return map[string]any{"error": msg}
}
