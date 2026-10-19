# Register the Microsoft Entra application

ZAKS Drive reaches SharePoint through Microsoft Graph as an **application**: it signs in with its own identity (the *client credentials* flow), not as a person. It never impersonates the SAP user. Who may call ZAKS Drive is decided separately, by [API keys / JWT and SAP signatures](../operations/security.md).

This is done once, by an Entra / Microsoft 365 administrator, in the [Entra admin center](https://entra.microsoft.com).

## What you need

| Role | Why |
|---|---|
| Someone allowed to register applications | Tenant policy may restrict this to administrators |
| An authorised administrator to **grant admin consent** | Granting Microsoft Graph *application* permissions needs a role such as **Privileged Role Administrator** (or Global Administrator). Application Administrator or Cloud Application Administrator alone cannot consent to Graph application roles. See [Microsoft's consent requirements](https://learn.microsoft.com/en-us/entra/identity/enterprise-apps/grant-admin-consent) |
| For `Sites.Selected`: an administrator who can grant site permissions | See [Graph permissions](graph-permissions.md) |

## Steps

1. **App registrations → New registration.** Name it, for example, `ZAKS Drive`. Choose *single tenant*. No redirect URI is needed.
2. On the **Overview** page note the **Directory (tenant) ID** and the **Application (client) ID**. The *Object ID* is not used.
3. **Certificates & secrets → Client secrets → New client secret.** Copy the **Value** immediately. It is shown only once.
   > The **Value** is the secret. The *Secret ID* (a GUID) is **not**. Putting the Secret ID into `GRAPH_CLIENT_SECRET` is the most common setup error and gives [`TOKEN_FAILED`](../error-codes.md#token_failed) with `invalid_client`.
   Note the expiry date: the secret must be replaced before it expires ([rotation](../operations/security.md#secret-management)).
4. **API permissions → Add a permission → Microsoft Graph → Application permissions.** Add the permission from [Graph permissions](graph-permissions.md). It must be of type **Application**, not Delegated.
5. **Grant admin consent for \<tenant\>.** The *Status* column must show a green tick. Adding a permission without consent has no effect: the access token then carries no permissions and every SharePoint call fails with [`SP_UNAUTHORIZED`](../error-codes.md#sp_unauthorized).
6. Put the three values into `deploy/.env`:

   ```dotenv
   GRAPH_TENANT_ID=<Directory (tenant) ID>
   GRAPH_CLIENT_ID=<Application (client) ID>
   GRAPH_CLIENT_SECRET=<the secret Value>
   ```

Hand these values over through a password manager or other secure channel, never by e-mail or chat. If a secret has been exposed, create a new one, update `.env`, recreate the container and delete the old secret.

## Verify

```bash
docker compose -f deploy/docker-compose.yml exec adapter /zaks-drive check
```

The check reports the token, the permissions found in it (the `roles` line) and each access step with PASS, FAIL or SKIP and the fix. Without a running container you can use the stand-alone script `python3 scripts/check_access.py deploy/.env <tenant>.sharepoint.com sites/<site> <library> <folder>`, which needs only Python 3.
