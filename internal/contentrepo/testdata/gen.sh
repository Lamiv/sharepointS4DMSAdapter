#!/bin/sh
# Regenerates secKey test fixtures with OpenSSL (independent of the Go code).
# The signed message is the worked example from the SAP Content Server
# HTTP 4.5 interface spec ("URL Encoding" page).
#   docker run --rm -v "$PWD/internal/contentrepo/testdata:/w" -w /w alpine sh -c "apk add -q openssl && sh gen.sh"
set -e
printf '%s' 'K1361A524A3ECB5459E0000800099245ECrpawdf054_BCE_2619981104091537' > message.txt
openssl dsaparam -out dsaparam.pem 1024 2>/dev/null
openssl gendsa -out dsa_key.pem dsaparam.pem 2>/dev/null
openssl req -new -x509 -key dsa_key.pem -sha1 -days 36500 -subj "/CN=PAW" -out dsa_cert.pem 2>/dev/null
openssl genrsa -out rsa_key.pem 2048 2>/dev/null
openssl req -new -x509 -key rsa_key.pem -days 36500 -subj "/CN=S4H" -out rsa_cert.pem
sign() { openssl cms -sign -binary -in message.txt -signer "$1_cert.pem" -inkey "$1_key.pem" -outform DER -md "$2" $3 -out "$4"; }
sign dsa sha1 "" dsa_sha1.p7
sign dsa sha1 -noattr dsa_sha1_noattr.p7
sign rsa sha256 "" rsa_sha256.p7
sign rsa md5 -noattr rsa_md5_noattr.p7
sign rsa ripemd160 "" rsa_ripemd160.p7 || echo "ripemd160 unavailable"
sign rsa sha256 "-nodetach" rsa_sha256_embedded.p7
openssl x509 -in dsa_cert.pem -outform DER -out dsa_cert.der
rm -f dsaparam.pem
rm -f dsa_key.pem rsa_key.pem  # keys are not needed by the tests; keep them out of git
ls -l
