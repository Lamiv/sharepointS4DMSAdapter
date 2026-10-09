"""Checks that the Entra app in deploy/.env has every access the adapter needs.

Usage (Python 3, no extra packages):
    python scripts/check_access.py deploy/.env <host> <site-path> <library> [folder]
    python scripts/check_access.py deploy/.env contoso.sharepoint.com sites/SAP_DOCS DOCS SAP_DMS

For each access the adapter relies on, the report shows:
    REQUIRED   what must be granted (Entra permission / SharePoint grant)
    TESTED     the exact Graph call made
    RESULT     PASS, FAIL (with the cause and the fix) or SKIP (a check it depends on failed)

The client secret is never printed. Test files are created in <folder> and
deleted again. Exit code 0 = everything passed, 1 = something failed.
"""
import base64
import json
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

GRAPH = "https://graph.microsoft.com/v1.0"
OK_ROLES = {
    "Sites.Selected": "needs a per-site grant (POST /sites/{id}/permissions, role write)",
    "Sites.ReadWrite.All": "all sites in the tenant",
    "Sites.FullControl.All": "all sites in the tenant (more than needed)",
}


def load_env(path):
    env = {}
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, v = line.split("=", 1)
                env[k.strip()] = v.strip().strip('"').strip("'")
    return env


def call(method, url, data=None, headers=None, attempts=4):
    """One Graph call. Network drops (status 0), throttling (429) and 5xx are
    retried, as the adapter itself does; other statuses are returned as-is."""
    for attempt in range(attempts):
        req = urllib.request.Request(url, data=data, method=method, headers=headers or {})
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                return r.status, r.read()
        except urllib.error.HTTPError as e:
            status, body = e.code, e.read()
            wait = int(e.headers.get("Retry-After", "0") or 0) if status in (429, 503) else 0
            if status not in (429, 500, 502, 503, 504) or attempt == attempts - 1:
                return status, body
            time.sleep(max(wait, 1 + attempt))
        except Exception as e:  # connection reset, DNS, timeout
            if attempt == attempts - 1:
                return 0, str(e).encode()
            time.sleep(1 + attempt)


def jerr(raw):
    try:
        e = json.loads(raw).get("error", {})
        if isinstance(e, dict):
            return f"{e.get('code')}: {e.get('message')}"
        return f"{e}: {json.loads(raw).get('error_description', '').splitlines()[0]}"
    except Exception:
        return raw[:200].decode("utf-8", "replace")


class Report:
    def __init__(self):
        self.rows = []

    def add(self, name, required, tested, result, detail="", fix=""):
        self.rows.append((name, required, tested, result, detail, fix))
        print(f"\n[{result}] {name}")
        print(f"    required: {required}")
        print(f"    tested  : {tested}")
        if detail:
            print(f"    detail  : {detail}")
        if fix and result == "FAIL":
            print(f"    fix     : {fix}")

    def summary(self):
        print("\n" + "=" * 78)
        print("SUMMARY")
        print("=" * 78)
        width = max(len(r[0]) for r in self.rows)
        for name, _, _, result, _, _ in self.rows:
            print(f"  {result:<4}  {name:<{width}}")
        failed = [r for r in self.rows if r[3] == "FAIL"]
        skipped = [r for r in self.rows if r[3] == "SKIP"]
        print("-" * 78)
        print(f"  passed {sum(r[3] == 'PASS' for r in self.rows)}, failed {len(failed)}, skipped {len(skipped)}")
        if failed:
            print(f"\n  First failure to fix: {failed[0][0]}\n    {failed[0][5]}")
        else:
            print("\n  All required access is in place.")
        return 1 if failed else 0


def main():
    if len(sys.argv) < 5:
        sys.exit(__doc__)
    env_path, host, site_path, library = sys.argv[1:5]
    folder = (sys.argv[5] if len(sys.argv) > 5 else "").strip("/")
    env = load_env(env_path)
    rep = Report()
    stop_reason = {}

    def skip(name, required, tested, because):
        rep.add(name, required, tested, "SKIP", f"depends on: {because}")

    # 1. credentials -------------------------------------------------------
    missing = [k for k in ("GRAPH_TENANT_ID", "GRAPH_CLIENT_ID", "GRAPH_CLIENT_SECRET") if not env.get(k)]
    rep.add("1. Credentials present in .env",
            "GRAPH_TENANT_ID, GRAPH_CLIENT_ID, GRAPH_CLIENT_SECRET (secret *Value*, not its ID)",
            f"read {env_path} (secret length {len(env.get('GRAPH_CLIENT_SECRET', ''))} chars; never printed)",
            "FAIL" if missing else "PASS",
            f"empty: {', '.join(missing)}" if missing else "",
            "Fill in the missing values in deploy/.env.")
    if missing:
        stop_reason["creds"] = True

    # 2. token -------------------------------------------------------------
    token = None
    claims = {}
    if stop_reason:
        skip("2. Access token from Microsoft Entra ID", "valid tenant ID, client ID and unexpired secret",
             "POST login.microsoftonline.com/<tenant>/oauth2/v2.0/token", "1")
    else:
        form = urllib.parse.urlencode({
            "grant_type": "client_credentials", "client_id": env["GRAPH_CLIENT_ID"],
            "client_secret": env["GRAPH_CLIENT_SECRET"], "scope": "https://graph.microsoft.com/.default"}).encode()
        st, raw = call("POST", f"https://login.microsoftonline.com/{env['GRAPH_TENANT_ID']}/oauth2/v2.0/token", form,
                       {"Content-Type": "application/x-www-form-urlencoded"})
        if st == 200:
            token = json.loads(raw)["access_token"]
            p = token.split(".")[1]
            claims = json.loads(base64.urlsafe_b64decode(p + "=" * (-len(p) % 4)))
            rep.add("2. Access token from Microsoft Entra ID", "valid tenant ID, client ID and unexpired secret",
                    "POST login.microsoftonline.com/<tenant>/oauth2/v2.0/token (client credentials)", "PASS",
                    f"app {claims.get('appid')} in tenant {claims.get('tid')}")
        else:
            stop_reason["token"] = True
            rep.add("2. Access token from Microsoft Entra ID", "valid tenant ID, client ID and unexpired secret",
                    "POST login.microsoftonline.com/<tenant>/oauth2/v2.0/token (client credentials)", "FAIL",
                    f"HTTP {st} {jerr(raw)}",
                    "invalid_client: use the secret VALUE (not the secret ID) and check it has not expired; "
                    "unauthorized_client / tenant errors: check the tenant and client IDs.")

    # 3. roles -------------------------------------------------------------
    roles = claims.get("roles") or []
    if "token" in stop_reason or "creds" in stop_reason:
        skip("3. Application permission in the token", "a Microsoft Graph APPLICATION permission with admin consent: "
             + " | ".join(OK_ROLES), "decode the token's 'roles' claim", "2")
    else:
        usable = [r for r in roles if r in OK_ROLES or r in ("Files.ReadWrite.All",)]
        rep.add("3. Application permission in the token",
                "Graph APPLICATION permission (type 'Application', admin consent granted): Sites.Selected "
                "(+ site grant), or Sites.ReadWrite.All, or Files.ReadWrite.All for a OneDrive userId",
                "decode the token's 'roles' claim",
                "PASS" if usable else "FAIL",
                f"roles in token: {roles or 'NONE'}",
                "Entra admin center > App registrations > your app > API permissions: add the permission as type "
                "APPLICATION (not Delegated) and click 'Grant admin consent'. Wait ~1 minute, then rerun.")
        if not usable:
            stop_reason["roles"] = True
        if "Sites.Selected" in roles and not any(r in roles for r in ("Sites.ReadWrite.All", "Sites.FullControl.All")):
            print("    note    : Sites.Selected alone grants no site access; step 4 passes only after the site grant.")

    auth = {"Authorization": f"Bearer {token}"} if token else {}
    blocked = "token" in stop_reason or "creds" in stop_reason or "roles" in stop_reason

    # 4. site --------------------------------------------------------------
    site_id = None
    req4 = ("read access to the site: Sites.ReadWrite.All, or Sites.Selected AND a per-site grant for this app "
            "(POST /sites/{id}/permissions with role 'write')")
    t4 = f"GET /sites/{host}:/{site_path}"
    if blocked:
        skip("4. Read the SharePoint site", req4, t4, "3")
    else:
        st, raw = call("GET", f"{GRAPH}/sites/{host}:/{site_path}?$select=id,displayName,webUrl", headers=auth)
        if st == 200:
            d = json.loads(raw)
            site_id = d["id"]
            rep.add("4. Read the SharePoint site", req4, t4, "PASS", f"{d.get('displayName')} | {d.get('webUrl')}")
        else:
            fix = {401: "the token has no usable permission: see step 3",
                   403: "the app has no access to THIS site: grant it with POST /sites/{site-id}/permissions "
                        '{"roles":["write"],"grantedToIdentities":[{"application":{"id":"<client-id>"}}]} '
                        "(signed in as a SharePoint admin), or use Sites.ReadWrite.All",
                   404: "the site path is wrong (check host and path; no trailing library name)"}.get(
                st, "see the Graph error above")
            rep.add("4. Read the SharePoint site", req4, t4, "FAIL", f"HTTP {st} {jerr(raw)}", fix)

    # 5. libraries ---------------------------------------------------------
    drive_id = None
    req5 = "read access to the site's document libraries (same as step 4)"
    t5 = f"GET /sites/{{id}}/drives, find library '{library}'"
    if not site_id:
        skip("5. Find the document library", req5, t5, "4")
    else:
        st, raw = call("GET", f"{GRAPH}/sites/{urllib.parse.quote(site_id)}/drives?$select=id,name,webUrl", headers=auth)
        if st != 200:
            rep.add("5. Find the document library", req5, t5, "FAIL", f"HTTP {st} {jerr(raw)}",
                    "the app can read the site but not list libraries: check the grant or permission")
        else:
            libs = json.loads(raw)["value"]
            names = [d["name"] for d in libs]
            match = next((d for d in libs if d["name"].lower() == library.lower()), None)
            if match:
                drive_id = match["id"]
                rep.add("5. Find the document library", req5, t5, "PASS",
                        f"'{match['name']}' found | libraries on the site: {names}")
            else:
                rep.add("5. Find the document library", req5, t5, "FAIL", f"libraries on the site: {names}",
                        "set driveName in config.yaml to one of the names above (the display name, not the URL segment)")

    # 6-10 write/read/delete ----------------------------------------------
    base = f"{GRAPH}/drives/{urllib.parse.quote(drive_id)}" if drive_id else None
    stamp = int(time.time())
    fname = f"adapter-access-test-{stamp}.txt"
    path = f"{folder}/{fname}" if folder else fname
    payload = b"adapter access test " + str(stamp).encode()
    item_id = None
    wr = "WRITE access to the library: Sites.Selected grant with role 'write' (not 'read'), or Sites.ReadWrite.All"

    t6 = f"PUT /drives/{{id}}/root:/{path}:/content (creates '{folder}' if missing)"
    if not drive_id:
        skip("6. Write: upload a small file (creates the folder)", wr, t6, "5")
    else:
        st, raw = call("PUT", f"{base}/root:/{urllib.parse.quote(path)}:/content", payload,
                       {**auth, "Content-Type": "text/plain"})
        if st in (200, 201):
            item_id = json.loads(raw)["id"]
            rep.add("6. Write: upload a small file (creates the folder)", wr, t6, "PASS", f"HTTP {st}, item {item_id[:12]}...")
        else:
            rep.add("6. Write: upload a small file (creates the folder)", wr, t6, "FAIL", f"HTTP {st} {jerr(raw)}",
                    "the app only has READ access: grant role 'write' on the site (delete the read grant or PATCH it "
                    "to write), or use Sites.ReadWrite.All" if st == 403 else "see the Graph error above")

    t7 = "GET /drives/{id}/items/{id}/content and compare bytes"
    if not item_id:
        skip("7. Read: download the file and compare", "READ access to the library", t7, "6")
    else:
        st, raw = call("GET", f"{base}/items/{item_id}/content", headers=auth)
        ok = st == 200 and raw == payload
        rep.add("7. Read: download the file and compare", "READ access to the library", t7,
                "PASS" if ok else "FAIL", f"HTTP {st}, {len(raw)} bytes, content {'identical' if ok else 'DIFFERENT'}",
                "unexpected: content differs or download blocked; see the HTTP status")

    t8 = "POST /drives/{id}/items/{id}/createUploadSession, then cancel it"
    if not item_id:
        skip("8. Resumable upload session (files over 4 MiB)", wr, t8, "6")
    else:
        st, raw = call("POST", f"{base}/items/{item_id}/createUploadSession", b'{"item":{"@microsoft.graph.conflictBehavior":"replace"}}',
                       {**auth, "Content-Type": "application/json"})
        if st == 200:
            call("DELETE", json.loads(raw)["uploadUrl"])
            rep.add("8. Resumable upload session (files over 4 MiB)", wr, t8, "PASS", "session created and cancelled")
        else:
            rep.add("8. Resumable upload session (files over 4 MiB)", wr, t8, "FAIL", f"HTTP {st} {jerr(raw)}",
                    "needs the same write access as step 6; large documents depend on it")

    t9 = "GET /drives/{id}/items/{id}/versions"
    if not item_id:
        skip("9. Version history (SAP update/append)", "READ access to the library", t9, "6")
    else:
        st, raw = call("GET", f"{base}/items/{item_id}/versions", headers=auth)
        rep.add("9. Version history (SAP update/append)", "READ access to the library", t9,
                "PASS" if st == 200 else "FAIL", f"HTTP {st}" + ("" if st == 200 else f" {jerr(raw)}"),
                "see the Graph error above")

    t10 = "DELETE /drives/{id}/items/{id} (removes the test file)"
    if not item_id:
        skip("10. Delete: remove the test file", "DELETE access (included in 'write')", t10, "6")
    else:
        st, raw = call("DELETE", f"{base}/items/{item_id}", headers=auth)
        rep.add("10. Delete: remove the test file", "DELETE access (included in 'write')", t10,
                "PASS" if st == 204 else "FAIL", f"HTTP {st}" + ("" if st == 204 else f" {jerr(raw)}"),
                "delete the file adapter-access-test-*.txt by hand; the adapter needs delete rights for SAP deletes")

    # 11. leftovers from earlier runs ------------------------------------
    t11 = "GET the folder's children, DELETE items named adapter-access-test-*"
    if not drive_id or not folder:
        skip("11. Clean up test files from earlier runs", "DELETE access", t11, "5")
    else:
        st, raw = call("GET", f"{base}/root:/{urllib.parse.quote(folder)}:/children?$select=id,name", headers=auth)
        if st == 404:
            rep.add("11. Clean up test files from earlier runs", "DELETE access", t11, "PASS", "folder not found, nothing to clean")
        elif st != 200:
            rep.add("11. Clean up test files from earlier runs", "DELETE access", t11, "FAIL", f"HTTP {st} {jerr(raw)}",
                    "delete adapter-access-test-*.txt by hand")
        else:
            old = [i for i in json.loads(raw)["value"] if i["name"].startswith("adapter-access-test-")]
            bad = [i["name"] for i in old if call("DELETE", f"{base}/items/{i['id']}", headers=auth)[0] != 204]
            rep.add("11. Clean up test files from earlier runs", "DELETE access", t11, "FAIL" if bad else "PASS",
                    f"removed {len(old) - len(bad)} leftover test file(s)" + (f"; could not remove {bad}" if bad else ""),
                    "delete those files by hand")

    if drive_id:
        root = folder or '""'
        print("\nConfig that matches what was tested (deploy/config.yaml):")
        print(f"  repositories:\n    - id: DMS\n      siteUrl: https://{host}/{site_path}\n      driveName: {library}"
              f"\n      rootPath: {root}")
    return rep.summary()


if __name__ == "__main__":
    sys.exit(main())
