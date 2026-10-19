#!/usr/bin/env bash
# End-to-end smoke test of a RUNNING adapter against its real SharePoint:
# REST API (upload, download, ranges, 6 MB resumable upload, streamed upload,
# versions, paging, security checks), the SAP Content Server interface and
# 10 parallel clients. Test data is created under <repo>/e2e-test and removed.
#
#   API_KEY=<key> scripts/e2e-smoke.sh                          # REST only
#   API_KEY=<key> CS_REPO=ZT scripts/e2e-smoke.sh               # + SAP interface
#
# Settings (environment): API_KEY (required), REST_URL, CS_URL, REPO (REST
# repository id, default DMS), CS_REPO (an UNSIGNED test contRep; skip the SAP
# tests when unset), SIGNED_CS_REPO (a signature-required contRep to check it
# rejects unsigned requests; optional).
# Needs: bash, curl, sha256sum (Linux/macOS/Git Bash).
set -u
: "${API_KEY:?set API_KEY to an adapter API key with read/write/delete}"
REPO=${REPO:-DMS}
R=${REST_URL:-http://localhost:8080}/api/v1/repositories/$REPO
CS=${CS_URL:-http://localhost:8090}/ContentServer/ContentServer.dll
CSR=${CS_REPO:-}
K="X-API-Key: $API_KEY"
TMP=$(mktemp -d)
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); printf "  PASS  %s\n" "$1"; }
bad()  { FAIL=$((FAIL+1)); printf "  FAIL  %s  -> %s\n" "$1" "$2"; }
check(){ # name expected actual
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected [$2] got [$3]"; fi; }
idof() { sed -E 's/.*"id":"([^"]+)".*/\1/'; }

head -c 150000 /dev/urandom > $TMP/small.bin
head -c 6291456 /dev/urandom > $TMP/big.bin       # > 4 MiB: resumable upload session
head -c 5242880 /dev/urandom > $TMP/stream.bin    # sent chunked (no Content-Length): spooled
sha() { sha256sum "$1" | cut -d' ' -f1; }

echo "== REST API (real SharePoint) =="
code=$(curl -s -o $TMP/r1 -w '%{http_code}' -H "$K" --data-binary @$TMP/small.bin "$R/documents?folder=e2e-test&fileName=small.bin&conflict=replace")
check "upload 150 KB" 201 "$code"; SID=$(idof < $TMP/r1)
curl -s -H "$K" -o $TMP/d1 "$R/documents/$SID/content"; check "download identical" "$(sha $TMP/small.bin)" "$(sha $TMP/d1)"
code=$(curl -s -o $TMP/d2 -w '%{http_code}' -H "$K" -H "Range: bytes=100-199" "$R/documents/$SID/content")
check "range 100-199 (206)" 206 "$code"; check "range bytes identical" "$(tail -c +101 $TMP/small.bin | head -c 100 | sha256sum | cut -d' ' -f1)" "$(sha $TMP/d2)"
etag=$(curl -s -D - -o /dev/null -H "$K" "$R/documents/$SID/content" | tr -d '\r' | awk -F': ' 'tolower($1)=="etag"{print $2}')
code=$(curl -s -o /dev/null -w '%{http_code}' -H "$K" -H "If-None-Match: $etag" "$R/documents/$SID/content"); check "If-None-Match -> 304" 304 "$code"

code=$(curl -s -o $TMP/r2 -w '%{http_code}' -H "$K" --data-binary @$TMP/big.bin "$R/documents?folder=e2e-test&fileName=big.bin&conflict=replace")
check "upload 6 MB (resumable session)" 201 "$code"; BID=$(idof < $TMP/r2)
curl -s -H "$K" -o $TMP/d3 "$R/documents/$BID/content"; check "6 MB download identical" "$(sha $TMP/big.bin)" "$(sha $TMP/d3)"
code=$(curl -s -o $TMP/r3 -w '%{http_code}' -H "$K" -H "Transfer-Encoding: chunked" --data-binary @$TMP/stream.bin "$R/documents?folder=e2e-test&fileName=stream.bin&conflict=replace")
check "upload 5 MB without Content-Length (spooled)" 201 "$code"; XID=$(idof < $TMP/r3)
curl -s -H "$K" -o $TMP/d4 "$R/documents/$XID/content"; check "5 MB streamed download identical" "$(sha $TMP/stream.bin)" "$(sha $TMP/d4)"

code=$(curl -s -o $TMP/r4 -w '%{http_code}' -H "$K" -X PUT --data-binary @$TMP/small.bin "$R/documents/$SID/content"); check "replace content (new version)" 200 "$code"
code=$(curl -s -o $TMP/v -w '%{http_code}' -H "$K" "$R/documents/$SID/versions"); check "version history" 200 "$code"
code=$(curl -s -o $TMP/l -w '%{http_code}' -H "$K" "$R/children?path=e2e-test&top=2"); check "list folder (page 1)" 200 "$code"
cur=$(sed -E 's/.*"nextCursor":"([^"]+)".*/\1/' $TMP/l); [ "${#cur}" -gt 20 ] && code=$(curl -s -o /dev/null -w '%{http_code}' -H "$K" "$R/children?path=e2e-test&top=2&cursor=$cur") && check "list folder (page 2 via signed cursor)" 200 "$code"
code=$(curl -s -o /dev/null -w '%{http_code}' -H "$K" "$R/children?path=e2e-test&top=2&cursor=$cur"x); check "tampered cursor rejected" 400 "$code"
code=$(curl -s -o /dev/null -w '%{http_code}' -H "$K" "$R/search?q=small"); check "search" 200 "$code"
if [ -n "$CSR" ]; then code=$(curl -s -o /dev/null -w '%{http_code}' -H "$K" "$R/children?path=ContentServer/$CSR"); check "content server folder hidden from REST (404)" 404 "$code"; fi
code=$(curl -s -o /dev/null -w '%{http_code}' "$R/children"); check "no API key -> 401" 401 "$code"

if [ -n "$CSR" ]; then
echo "== SAP Content Server interface (unsigned test repository $CSR) =="
D="E2E$(date +%s)"
body=$(curl -s "$CS?serverInfo&pVersion=0047&contRep=$CSR"); case "$body" in *'serverStatus="running"'*"contRep=\"$CSR\""*) ok "serverInfo";; *) bad serverInfo "$body";; esac
code=$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H "Content-Type: application/pdf" --data-binary @$TMP/small.bin "$CS?create&pVersion=0047&contRep=$CSR&docId=$D&compId=data"); check "create (PUT)" 201 "$code"
code=$(curl -s -o /dev/null -w '%{http_code}' -X PUT --data-binary @$TMP/small.bin "$CS?create&pVersion=0047&contRep=$CSR&docId=$D&compId=data"); check "create again -> 403 (exists)" 403 "$code"
curl -s -D $TMP/h -o $TMP/info "$CS?info&pVersion=0047&contRep=$CSR&docId=$D"; grep -qi '^X-numberComps: 1' $TMP/h && ok "info: 1 component" || bad "info" "$(cat $TMP/h | head -3)"
curl -s -o $TMP/g1 "$CS?get&pVersion=0047&contRep=$CSR&docId=$D&compId=data"; check "get identical" "$(sha $TMP/small.bin)" "$(sha $TMP/g1)"
curl -s -o $TMP/g2 "$CS?get&pVersion=0047&contRep=$CSR&docId=$D&compId=data&fromOffset=10&toOffset=19"; check "get range 10-19" "$(tail -c +11 $TMP/small.bin | head -c 10 | sha256sum | cut -d' ' -f1)" "$(sha $TMP/g2)"
code=$(curl -s -o /dev/null -w '%{http_code}' -X PUT --data-binary "hello;" "$CS?update&pVersion=0047&contRep=$CSR&docId=$D&compId=note"); check "update (new component)" 200 "$code"
code=$(curl -s -o /dev/null -w '%{http_code}' -X PUT --data-binary " world" "$CS?append&pVersion=0047&contRep=$CSR&docId=$D&compId=note"); check "append" 200 "$code"
check "appended content" "hello; world" "$(curl -s "$CS?get&pVersion=0047&contRep=$CSR&docId=$D&compId=note")"
check "search in component" "1;7;" "$(curl -s "$CS?search&pVersion=0047&contRep=$CSR&docId=$D&compId=note&pattern=world")"
curl -s -o $TMP/dg "$CS?docGet&pVersion=0047&contRep=$CSR&docId=$D" ; [ "$(wc -c < $TMP/dg)" -gt 150000 ] && ok "docGet (multipart)" || bad docGet "$(wc -c < $TMP/dg) bytes"
code=$(curl -s -o /dev/null -w '%{http_code}' "$CS?info&pVersion=0047&contRep=${SIGNED_CS_REPO:-Z1}&docId=$D"); check "signature-required repository rejects unsigned request (401)" 401 "$code"
code=$(curl -s -o /dev/null -w '%{http_code}' "$CS?delete&pVersion=0047&contRep=$CSR&docId=$D&compId=note"); check "delete component" 200 "$code"
code=$(curl -s -o /dev/null -w '%{http_code}' "$CS?delete&pVersion=0047&contRep=$CSR&docId=$D"); check "delete document" 200 "$code"
code=$(curl -s -o /dev/null -w '%{http_code}' "$CS?get&pVersion=0047&contRep=$CSR&docId=$D"); check "deleted document -> 404" 404 "$code"
fi

echo "== Parallel clients (10 concurrent: upload, download, verify, delete) =="
one() { i=$1; f=$TMP/p$i.bin; head -c $((100000 + i*1000)) /dev/urandom > $f
  id=$(curl -s -H "$K" --data-binary @$f "$R/documents?folder=e2e-test/par&fileName=p$i.bin&conflict=replace" | idof) || return 1
  curl -s -H "$K" -o $f.dl "$R/documents/$id/content"; cmp -s $f $f.dl || { echo "MISMATCH $i"; return 1; }
  curl -s -o /dev/null -H "$K" -X DELETE "$R/documents/$id"; echo "OK $i"; }
export -f one idof; export R K TMP
out=$(seq 1 10 | xargs -P10 -I{} bash -c 'one {}')
n=$(echo "$out" | grep -c '^OK'); check "10 parallel round trips all ok" 10 "$n"
printf "  (took %.1f s)\n" "$(echo "$end - $start" | bc 2>/dev/null || python3 -c "print($end-$start)")"

echo "== Cleanup =="
for id in $SID $BID $XID; do curl -s -o /dev/null -H "$K" -X DELETE "$R/documents/$id"; done
fid=$(curl -s -H "$K" "$R/lookup?path=e2e-test" | idof); [ -n "$fid" ] && [ "${#fid}" -gt 10 ] && curl -s -o /dev/null -w "  e2e-test folder removed: HTTP %{http_code}\n" -H "$K" -X DELETE "$R/documents/$fid"
rm -rf $TMP
echo; echo "RESULT: $PASS passed, $FAIL failed"; [ $FAIL -eq 0 ]
