package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lamiv/zaks-drive/internal/apperr"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConfigErrorsNameTheSettingAndTheVariable(t *testing.T) {
	t.Setenv("GRAPH_TENANT_ID", "")
	t.Setenv("GRAPH_CLIENT_ID", "id")
	t.Setenv("GRAPH_CLIENT_SECRET", "")
	t.Setenv("SHAREPOINT_SITE_URL", "")
	t.Setenv("ADAPTER_API_KEY_S4", "")
	p := writeCfg(t, `
server: { interfaces: [rest, contentrepo, ftp] }
graph:
  tenantId: ${GRAPH_TENANT_ID}
  clientId: ${GRAPH_CLIENT_ID}
  clientSecret: ${GRAPH_CLIENT_SECRET}
auth:
  apiKeys:
    - { name: s4, key: "${ADAPTER_API_KEY_S4}" }
repositories:
  - id: DMS
    siteUrl: ${SHAREPOINT_SITE_URL}
  - id: PAGE
    siteUrl: https://contoso.sharepoint.com/sites/x/Shared%20Documents/Forms/AllItems.aspx
contentServer:
  allowedNetworks: [10.0.0.0/99]
  repositories:
    - { contRep: Z123, repository: NOPE, signature: maybe }
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected errors")
	}
	text := apperr.Format(err)
	for _, want := range []string{
		"CFG_GRAPH_CREDENTIALS_MISSING", "graph.tenantId (GRAPH_TENANT_ID), graph.clientSecret (GRAPH_CLIENT_SECRET)",
		"${GRAPH_TENANT_ID}", // the empty variable behind the setting
		"CFG_INTERFACE_UNKNOWN", `"ftp"`,
		"CFG_APIKEY_INVALID", `"s4"`, "${ADAPTER_API_KEY_S4}",
		"CFG_REPO_LOCATION_MISSING", "${SHAREPOINT_SITE_URL}", "deploy/.env",
		"CFG_SITE_URL_INVALID", "page address inside a library", "SHAREPOINT_LIBRARY",
		"CFG_NETWORK_INVALID", "10.0.0.0/99",
		"CFG_CONTREP_INVALID", "Z123", "CFG_CONTREP_REPOSITORY_UNKNOWN", `"NOPE"`, "DMS, PAGE", "CFG_SIGNATURE_INVALID", `"maybe"`,
		"fix:", "docs/error-codes.md#cfg_graph_credentials_missing",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if t.Failed() {
		t.Log("\n" + text)
	}
}

func TestConfigFileProblems(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if ae, ok := apperr.As(err); !ok || ae.Code != "CFG_FILE_NOT_FOUND" || !strings.Contains(ae.Detail, "missing.yaml") {
		t.Errorf("missing file: %v", err)
	}
	_, err = Load(writeCfg(t, "server: [unclosed"))
	if ae, ok := apperr.As(err); !ok || ae.Code != "CFG_YAML_INVALID" {
		t.Errorf("bad yaml: %v", err)
	}
}

// The SharePoint location is meant to be set in deploy/.env.
func TestSharePointLocationFromEnvironment(t *testing.T) {
	t.Setenv("GRAPH_TENANT_ID", "t")
	t.Setenv("GRAPH_CLIENT_ID", "c")
	t.Setenv("GRAPH_CLIENT_SECRET", "s")
	t.Setenv("ADAPTER_API_KEY_S4", "k")
	t.Setenv("SHAREPOINT_SITE_URL", "https://contoso.sharepoint.com/sites/SAP_DOCS")
	t.Setenv("SHAREPOINT_LIBRARY", "DOCS")
	t.Setenv("SHAREPOINT_ROOT_PATH", "/SAP_DMS/")
	cfg, err := Load(writeCfg(t, `
server: { interfaces: [rest] }
auth: { apiKeys: [ { name: s4, key: "${ADAPTER_API_KEY_S4}" } ] }
repositories:
  - id: DMS
    siteUrl: ${SHAREPOINT_SITE_URL}
    driveName: ${SHAREPOINT_LIBRARY}
    rootPath: ${SHAREPOINT_ROOT_PATH}
`))
	if err != nil {
		t.Fatal(apperr.Format(err))
	}
	r := cfg.Repositories[0]
	if r.SiteURL != "https://contoso.sharepoint.com/sites/SAP_DOCS" || r.DriveName != "DOCS" || r.RootPath != "SAP_DMS" {
		t.Errorf("repository from .env: %+v", r)
	}
}
