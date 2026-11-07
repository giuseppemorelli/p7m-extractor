#!/bin/sh
# Rigenera i file di test con OpenSSL e un certificato autofirmato usa e getta.
# Uso: sh testdata/genera.sh  (dalla radice del progetto)
set -eu
cd "$(dirname "$0")"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
  -keyout "$tmp/key.pem" -out "$tmp/cert.pem" \
  -subj "/C=IT/O=Esempio S.r.l./CN=Mario Rossi/serialNumber=TINIT-RSSMRA80A01H501U" 2>/dev/null

# Documento di esempio abbastanza grande da essere spezzato in blocchi
# quando firmato in modalità streaming.
{
  echo '<?xml version="1.0" encoding="UTF-8"?>'
  echo '<FatturaElettronica versione="FPR12">'
  i=1
  while [ $i -le 200 ]; do
    echo "  <Riga numero=\"$i\">Descrizione articolo àèìòù n. $i</Riga>"
    i=$((i + 1))
  done
  echo '</FatturaElettronica>'
} > fattura.xml

sign() { openssl cms -sign -binary -nodetach -signer "$tmp/cert.pem" -inkey "$tmp/key.pem" "$@"; }

sign -in fattura.xml -outform DER -out fattura.xml.p7m
sign -in fattura.xml -outform DER -stream -out fattura_ber.xml.p7m
sign -in fattura.xml -outform PEM -out fattura_pem.xml.p7m
openssl base64 -in fattura.xml.p7m -out fattura_b64.xml.p7m
sign -in fattura.xml.p7m -outform DER -out fattura_doppia.xml.p7m.p7m
openssl cms -sign -binary -signer "$tmp/cert.pem" -inkey "$tmp/key.pem" \
  -in fattura.xml -outform DER -out detached.p7s
