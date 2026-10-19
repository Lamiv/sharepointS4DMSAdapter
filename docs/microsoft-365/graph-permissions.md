# Microsoft Graph permissions

ZAKS Drive needs **application** permissions (type *Application*, with admin consent). Pick the narrowest option that fits how you store documents.

| Where documents are stored | Permission | Scope of access | Config |
|---|---|---|---|
| **A SharePoint site (recommended)** | `Sites.Selected` **plus a site grant with role `write`** | Only the sites explicitly granted | `siteUrl` + `driveName` |
| A SharePoint site (simpler, broader) | `Sites.ReadWrite.All` | Every site collection in the tenant | `siteUrl` + `driveName` |
| A user's OneDrive for Business | `Files.ReadWrite.All` | Files of all users in the tenant | `userId` |

`Sites.Selected` alone grants **no** access: until the site itself is shared with the app, SharePoint answers 403 ([`SP_ACCESS_DENIED`](../error-codes.md#sp_access_denied)). A site grant with role `read` cannot upload or delete. Keeping `Sites.ReadWrite.All` next to `Sites.Selected` still gives broad access, so remove it when you choose the narrow option.

ZAKS Drive does not use delegated permissions and never calls Graph on behalf of an SAP or Fiori user.

## Granting a site to the app (`Sites.Selected`)

This is a setup step done by an administrator with a *separate* administrative client, for example [Graph Explorer](https://developer.microsoft.com/graph/graph-explorer) signed in as a SharePoint or Global administrator. The caller needs permission to manage site permissions (Microsoft documents `Sites.FullControl.All`; see [selected-permissions overview](https://learn.microsoft.com/en-us/graph/permissions-selected-overview)). The runtime application never needs it.

1. Find the site ID:

   ```http
   GET https://graph.microsoft.com/v1.0/sites/<tenant>.sharepoint.com:/sites/<site>?$select=id
   ```

2. Grant the application write access:

   ```http
   POST https://graph.microsoft.com/v1.0/sites/<site-id>/permissions
   Content-Type: application/json

   {
     "roles": ["write"],
     "grantedToIdentities": [{
       "application": { "id": "<client-id>", "displayName": "ZAKS Drive" }
     }]
   }
   ```

   A `201 Created` answer means the grant exists.

## Checking what the token carries

The access token lists its application permissions in the `roles` claim. `zaks-drive check` shows it, the start-up log prints it (`graph application permissions`), and `/readyz` and the error [`SP_UNAUTHORIZED`](../error-codes.md#sp_unauthorized) tell you when it is empty:

| `roles` shows | Meaning |
|---|---|
| nothing / `NONE` | The permission is missing, is *Delegated*, or admin consent was not granted |
| `Sites.Selected` only | Consent is fine; the site must still be granted (403 until then) |
| `Sites.ReadWrite.All` or `Files.ReadWrite.All` | Broad access; no site grant needed |

## Which operations need which access

| Operation | Access |
|---|---|
| Find the site and library at start-up | read |
| Upload, replace, create folders | write |
| Download, list, search, versions | read |
| Delete | write (delete) |

The `write` role covers all of them. ZAKS Drive's own per-key permissions (`read`, `write`, `delete`) are enforced in addition, before SharePoint is called. See [security](../operations/security.md).

## Not implemented

Certificate-based client authentication to Entra (instead of a client secret) is not implemented; see the [roadmap](../roadmap.md).
