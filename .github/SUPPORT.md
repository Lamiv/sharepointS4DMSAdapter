# Support

ZAKS Drive is an open-source project maintained on a best-effort basis. There is no service level agreement and no commercial support.

## Before asking

1. Run `zaks-drive check` and read its output; each failure names the fix.
2. Look the error code up in [docs/error-codes.md](../docs/error-codes.md).
3. Check the troubleshooting guides: [operations](../docs/operations/troubleshooting.md), [SAP](../docs/sap/troubleshooting.md).

## Where to ask

- **Questions, ideas and architecture:** GitHub Discussions, once the repository owner enables them; until then an issue is fine. Significant proposals: an [RFC](../docs/rfcs/README.md).
- **Bugs and requests:** open a [GitHub issue](https://github.com/Lamiv/zaks-drive/issues/new/choose) with the version (`zaks-drive version`), the error code, and the relevant log lines.
- **Security problems:** do not open a public issue. Follow [SECURITY.md](SECURITY.md).
- **Questions about SAP or Microsoft products themselves:** use SAP's and Microsoft's own support channels.

## What to include, and what never to include

Include: version, how you deployed it, the error code and message, `X-Request-ID`, the log lines for the failing request, the SAP report or transaction involved.

Never include: `deploy/.env`, client secrets, API keys, tokens, certificates' private keys, or customer documents.
