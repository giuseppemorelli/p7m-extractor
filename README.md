# p7m-extractor

Estrae il documento originale da file firmati digitalmente in formato
PKCS#7/CMS (`.p7m`, CAdES), come le fatture elettroniche (`.xml.p7m`) o i
documenti firmati con smart card e firma remota (`.pdf.p7m`).

Disponibile in tre forme, tutte basate sullo stesso codice Go senza
dipendenze esterne:

- **programma da riga di comando** per Linux, Windows e macOS;
- **interfaccia grafica locale**: lo stesso eseguibile, avviato senza argomenti;
- **sito web statico** (WebAssembly), pubblicabile su GitHub Pages, Cloudflare
  Pages o qualsiasi hosting statico: l'estrazione avviene nel browser e i file
  non vengono caricati su alcun server.

## Formati supportati

- binario DER e BER, inclusa la lunghezza indefinita con contenuto a blocchi
  (tipica dei software di firma che lavorano "in streaming")
- Base64, con o senza intestazioni PEM (`-----BEGIN PKCS7-----`)
- firme annidate (`.p7m.p7m`), aperte automaticamente

Le firme *detached* (`.p7s`) non contengono il documento: il programma lo
segnala con un errore.

> **Attenzione:** il programma estrae il contenuto ma **non verifica** la
> validità della firma, del certificato o della catena di certificazione.
> Le informazioni mostrate con `-i` sono quelle dichiarate nella busta.

## Interfaccia grafica

Avviando il programma **senza argomenti** (ad esempio con un doppio clic
sull'eseguibile) si apre l'interfaccia grafica nel browser predefinito:

- trascina i file `.p7m`, o intere cartelle, nella pagina oppure usa "Scegli i file";
- per ogni documento vedi il firmatario, il codice fiscale, l'ente
  certificatore e la data di firma;
- "Apri" mostra l'anteprima (PDF, immagini, XML come testo), "Scarica" salva
  il singolo file, "Scarica tutti (ZIP)" li salva tutti insieme.

L'interfaccia è servita da un piccolo server interno raggiungibile solo dal
computer locale (`127.0.0.1`) e protetto da un token casuale: i file non
lasciano mai il computer. Per chiudere il programma usa "Esci" nella pagina,
premi Ctrl+C o chiudi la finestra del terminale.

```sh
p7m-extractor              # apre l'interfaccia
p7m-extractor -gui -no-browser   # mostra solo l'indirizzo da aprire
```

Su macOS, al primo avvio di un eseguibile scaricato da internet, Gatekeeper
può bloccarlo: si sblocca con `xattr -d com.apple.quarantine p7m-extractor-darwin-*`
oppure da Impostazioni di Sistema → Privacy e sicurezza.

## Versione web

```sh
make web        # → dist/web/ (index.html, p7m.wasm, wasm_exec.js, _headers)
```

`dist/web/` è un sito completamente statico: l'estrattore Go compilato in
WebAssembly gira nel browser di chi visita il sito, quindi i documenti non
lasciano mai il suo computer. Tutti i percorsi sono relativi, quindi il sito
funziona anche in una sottocartella (come `https://utente.github.io/progetto/`).

`wasm_exec.js` viene copiato dall'installazione di Go usata per compilare,
perché deve corrispondere esattamente alla sua versione: non va modificato a
mano né preso da un'altra versione di Go.

Per provarlo in locale serve un server HTTP qualsiasi (aprire `index.html`
direttamente dal disco non funziona), ad esempio
`python3 -m http.server -d dist/web 8080` e poi <http://localhost:8080>.

### Pubblicazione su GitHub Pages

La pubblicazione è automatica: il workflow `.github/workflows/pages.yml`
esegue i test, compila il sito e lo pubblica a ogni push sul branch `master`.

Configurazione, da fare una volta sola:

1. Carica il repository su GitHub (`git push origin master`).
2. Su GitHub apri il repository e vai in **Settings → Pages**.
3. In **Build and deployment → Source** scegli **GitHub Actions**.
4. Avvia la prima pubblicazione: fai un push, oppure vai nella scheda
   **Actions**, scegli "Pubblica su GitHub Pages" e premi **Run workflow**.
5. Al termine (1-2 minuti) il sito è online su
   `https://<utente>.github.io/<repository>/`, in questo caso
   <https://giuseppemorelli.github.io/p7m-extractor/>. L'indirizzo compare
   anche in Settings → Pages e nel riepilogo del workflow.

Da quel momento ogni push su `master` aggiorna il sito. Se i test falliscono,
il sito non viene aggiornato e resta online la versione precedente.

Note:

- con un account GitHub gratuito, GitHub Pages funziona solo con i
  repository **pubblici**;
- per usare un **dominio personalizzato** inseriscilo in Settings → Pages →
  Custom domain, configura il DNS come indicato da GitHub e attiva
  "Enforce HTTPS" (con la pubblicazione tramite Actions non serve il file
  `CNAME`);
- GitHub Pages non permette header HTTP personalizzati: la Content Security
  Policy è quindi inserita direttamente nella pagina con un tag `<meta>`. Non
  è possibile impedire che il sito venga incluso in un iframe di un altro
  sito, cosa poco rilevante per questo strumento, che non ha account né azioni
  da proteggere.

### Pubblicazione su Cloudflare Pages

Su Cloudflare il file `_headers` aggiunge gli header di sicurezza che GitHub
Pages non supporta (protezione dagli iframe, `nosniff`, ...). Per pubblicare:

- carica la cartella `dist/web` dalla dashboard di Cloudflare
  (Workers & Pages → Crea → Pages → *Direct Upload*);
- oppure usa Wrangler: `npx wrangler pages deploy dist/web --project-name p7m-extractor`;
- oppure collega il repository Git con comando di build `make web` e cartella
  di output `dist/web` (la versione di Go si sceglie con la variabile
  d'ambiente `GO_VERSION`).

## Uso da riga di comando

```sh
p7m-extractor fattura.xml.p7m                 # crea fattura.xml accanto al file
p7m-extractor -d estratti/ *.p7m              # più file in una cartella
p7m-extractor -d estratti/ cartella_fatture/  # tutti i .p7m di una cartella
p7m-extractor -i documento.pdf.p7m            # mostra anche i firmatari
p7m-extractor -o - fattura.xml.p7m | less     # documento su stdout
```

| Opzione        | Descrizione                                                   |
|----------------|---------------------------------------------------------------|
| `-o file`      | file di output (un solo input; `-` = stdout)                  |
| `-d cartella`  | cartella di output (default: quella del file di input)        |
| `-f`           | sovrascrive i file esistenti                                  |
| `-i`           | mostra firmatario, ente certificatore, validità e data firma  |
| `-q`           | silenzioso, mostra solo gli errori                            |
| `-nested=false`| apre solo la busta più esterna                                |
| `-version`     | mostra la versione                                            |
| `-gui`         | avvia l'interfaccia grafica                                   |
| `-no-browser`  | con `-gui`, non apre il browser automaticamente               |

Le opzioni vanno scritte **prima** dei file. I pattern come `*.p7m` vengono
espansi dal programma stesso, quindi funzionano anche nel prompt dei comandi
di Windows.

Il nome del file estratto si ottiene togliendo `.p7m` (`fattura.xml.p7m` →
`fattura.xml`). Se il nome non contiene l'estensione originale, il programma
la ricava dal contenuto (PDF, XML, ZIP, immagini…).

Codici di uscita: `0` tutto ok, `1` almeno un file non estratto, `2` errore
negli argomenti.

## Compilazione

Serve Go 1.21 o successivo.

```sh
make build      # sistema corrente → bin/p7m-extractor
make release    # tutte le piattaforme → dist/
make web        # sito statico WebAssembly → dist/web/
make test
```

Senza `make`, ad esempio da PowerShell:

```powershell
$env:CGO_ENABLED="0"; $env:GOOS="windows"; $env:GOARCH="amd64"
go build -trimpath -ldflags "-s -w" -o p7m-extractor.exe .
```

Valori di `GOOS`/`GOARCH` utili: `linux`, `windows`, `darwin` con `amd64` o
`arm64`.

## Struttura del progetto

| Percorso          | Contenuto                                                        |
|-------------------|------------------------------------------------------------------|
| `p7m/`            | libreria di estrazione (parser BER, CMS, nomi file, ZIP)         |
| `main.go`         | riga di comando                                                  |
| `gui.go`          | interfaccia grafica locale (server su 127.0.0.1)                 |
| `cmd/wasm/`       | versione WebAssembly per il sito statico                         |
| `web/index.html`  | pagina unica, usata sia dall'interfaccia locale sia dal sito web |
| `web/_headers`    | header di sicurezza per Cloudflare Pages                         |
| `.github/workflows/pages.yml` | pubblicazione automatica su GitHub Pages             |

## Test

I file in `testdata/` vengono generati con OpenSSL e un certificato
autofirmato di prova (`sh testdata/genera.sh`). È incluso anche un fuzz test:

```sh
go test ./p7m -run '^$' -fuzz FuzzExtract -fuzztime 60s
```

La libreria si può testare anche compilata in WebAssembly (serve Node.js):

```sh
PATH="$PATH:$(go env GOROOT)/misc/wasm" GOOS=js GOARCH=wasm go test ./p7m
```

(con Go 1.24 o successivo il percorso è `$(go env GOROOT)/lib/wasm`).
