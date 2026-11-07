// p7m-extractor estrae il documento originale da file firmati digitalmente
// in formato PKCS#7/CMS (.p7m, CAdES-BES), come fatture elettroniche e atti
// firmati con smart card o firma remota.
//
// NOTA: il programma estrae il contenuto ma NON verifica la validità della
// firma né la catena di certificazione.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/giuseppemorelli/p7m-extractor/p7m"
)

var version = "dev"

type options struct {
	outFile string
	outDir  string
	force   bool
	quiet   bool
	info    bool
	nested  bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("p7m-extractor", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var o options
	fs.StringVar(&o.outFile, "o", "", "file di output (solo con un singolo input; \"-\" = stdout)")
	fs.StringVar(&o.outDir, "d", "", "cartella di output (default: la stessa del file di input)")
	fs.BoolVar(&o.force, "f", false, "sovrascrivi i file già esistenti")
	fs.BoolVar(&o.quiet, "q", false, "modalità silenziosa: mostra solo gli errori")
	fs.BoolVar(&o.info, "i", false, "mostra le informazioni sui firmatari")
	fs.BoolVar(&o.nested, "nested", true, "apri anche le buste annidate (.p7m.p7m)")
	showVersion := fs.Bool("version", false, "mostra la versione ed esci")
	gui := fs.Bool("gui", false, "avvia l'interfaccia grafica nel browser (default se non ci sono argomenti)")
	noBrowser := fs.Bool("no-browser", false, "con -gui: non aprire il browser, mostra solo l'indirizzo")

	fs.Usage = func() {
		fmt.Fprintf(stderr, `p7m-extractor %s - estrae il documento originale da file firmati .p7m

Uso:
  p7m-extractor                     avvia l'interfaccia grafica
  p7m-extractor [opzioni] <file.p7m|cartella|pattern> ...

Esempi:
  p7m-extractor fattura.xml.p7m
  p7m-extractor -d estratti/ *.p7m
  p7m-extractor -i -o - documento.pdf.p7m > documento.pdf

Opzioni:
`, version)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "p7m-extractor", version)
		return 0
	}
	// Senza argomenti (es. doppio clic sull'eseguibile) parte l'interfaccia grafica.
	if *gui || len(args) == 0 {
		return runGUI(!*noBrowser, stdout, stderr)
	}

	inputs, err := expandInputs(fs.Args())
	if err != nil {
		fmt.Fprintln(stderr, "errore:", err)
		return 2
	}
	if len(inputs) == 0 {
		fs.Usage()
		return 2
	}
	if o.outFile != "" && len(inputs) > 1 {
		fmt.Fprintln(stderr, "errore: -o può essere usato solo con un singolo file di input (usa -d per una cartella)")
		return 2
	}
	if o.outFile != "" && o.outDir != "" {
		fmt.Fprintln(stderr, "errore: -o e -d non possono essere usati insieme")
		return 2
	}

	// Se il documento va su stdout, i messaggi vanno su stderr.
	log := stdout
	if o.outFile == "-" {
		log = stderr
	}
	if o.quiet {
		log = io.Discard
	}

	failed := 0
	for _, in := range inputs {
		if err := processFile(in, o, stdin, stdout, log); err != nil {
			fmt.Fprintf(stderr, "errore: %s: %v\n", in, err)
			failed++
		}
	}
	if failed > 0 {
		if len(inputs) > 1 {
			fmt.Fprintf(stderr, "%d file su %d non estratti\n", failed, len(inputs))
		}
		return 1
	}
	return 0
}

// expandInputs espande i pattern (*, ?) e le cartelle. L'espansione dei
// pattern è fatta qui perché il prompt dei comandi di Windows non la esegue.
func expandInputs(args []string) ([]string, error) {
	var out []string
	for _, a := range args {
		if a == "-" {
			out = append(out, a)
			continue
		}
		if strings.ContainsAny(a, "*?[") {
			matches, err := filepath.Glob(a)
			if err != nil {
				return nil, fmt.Errorf("pattern non valido %q: %w", a, err)
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("nessun file corrisponde a %q", a)
			}
			out = append(out, matches...)
			continue
		}
		if st, err := os.Stat(a); err == nil && st.IsDir() {
			entries, err := os.ReadDir(a)
			if err != nil {
				return nil, err
			}
			var found []string
			for _, e := range entries {
				if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".p7m") {
					found = append(found, filepath.Join(a, e.Name()))
				}
			}
			sort.Strings(found)
			out = append(out, found...)
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func processFile(in string, o options, stdin io.Reader, stdout, log io.Writer) error {
	var (
		data []byte
		err  error
	)
	if in == "-" {
		if o.outFile == "" {
			return errors.New("leggendo da stdin bisogna indicare il file di output con -o")
		}
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(in)
	}
	if err != nil {
		return err
	}

	content, layers, err := p7m.Extract(data, o.nested)
	if err != nil {
		return err
	}

	if o.info {
		printInfo(log, in, layers)
	}

	out := o.outFile
	if out == "" {
		dir := o.outDir
		if dir == "" {
			dir = filepath.Dir(in)
		}
		out = filepath.Join(dir, p7m.OutputName(in, content))
	}

	if out == "-" {
		_, err = stdout.Write(content)
		return err
	}
	if err := writeOutput(in, out, content, o.force); err != nil {
		return err
	}

	envelopes := ""
	if len(layers) > 1 {
		envelopes = fmt.Sprintf(", %d buste annidate", len(layers))
	}
	fmt.Fprintf(log, "%s -> %s (%d byte%s)\n", in, out, len(content), envelopes)
	return nil
}

func writeOutput(in, out string, content []byte, force bool) error {
	if in != "-" {
		absIn, err1 := filepath.Abs(in)
		absOut, err2 := filepath.Abs(out)
		if err1 == nil && err2 == nil && absIn == absOut {
			return fmt.Errorf("il file di output coincide con quello di input")
		}
	}
	if dir := filepath.Dir(out); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(out, flags, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s esiste già (usa -f per sovrascrivere)", out)
		}
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func printInfo(w io.Writer, in string, layers []*p7m.Envelope) {
	fmt.Fprintf(w, "%s\n", in)
	for i, signers := range p7m.DescribeLayers(layers) {
		fmt.Fprintf(w, "  busta %d di %d\n", i+1, len(layers))
		if len(signers) == 0 {
			fmt.Fprintln(w, "    nessun firmatario")
		}
		for _, d := range signers {
			if !d.HasCert {
				fmt.Fprintln(w, "    firmatario:  (certificato non incluso nella busta)")
			} else {
				name := d.Name
				if d.SerialNumber != "" {
					name += " [" + d.SerialNumber + "]"
				}
				fmt.Fprintf(w, "    firmatario:  %s\n", name)
				if d.Organization != "" {
					fmt.Fprintf(w, "    organizz.:   %s\n", d.Organization)
				}
				fmt.Fprintf(w, "    emesso da:   %s\n", d.Issuer)
				fmt.Fprintf(w, "    validità:    %s - %s\n",
					d.NotBefore.Format("2006-01-02"), d.NotAfter.Format("2006-01-02"))
			}
			if d.SigningTime != nil {
				fmt.Fprintf(w, "    data firma:  %s (dichiarata dal firmatario)\n",
					d.SigningTime.Format("2006-01-02 15:04:05 UTC"))
			}
		}
	}
}
