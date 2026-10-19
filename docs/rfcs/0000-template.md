# RFC NNNN: Title

- **Status:** draft
- **Author:** your GitHub name
- **Created:** YYYY-MM-DD
- **Discussion:** link to the pull request (and any earlier discussion or issue)

> An RFC is a proposal for discussion. It is not a promise that the change will be built, scheduled or released. See [docs/rfcs/README.md](README.md).

## Summary

One paragraph: what is proposed.

## Motivation

What problem does this solve, and for whom? What can people not do today? Evidence that the problem is real (an issue, a deployment, an SAP scenario).

## Design

How it works, in enough detail for someone else to implement it. Configuration keys, API changes, SAP protocol behaviour, storage layout, error codes.

## Alternatives

What else was considered? What happens if we do nothing?

## Compatibility

- Existing deployments and configuration:
- API routes and request or response contracts:
- Storage layout and persistent data:
- SAP Content Server protocol behaviour:
- Is this a breaking change? If yes, the migration path:

## Security

New attack surface, credentials, data exposure, and what is logged ([logging policy](../operations/security.md#logging-policy)).

## Testing

How this is tested without Microsoft 365 or SAP credentials. What needs a real system, and who can test it.

## Migration

How existing users adopt it, and how they roll back.

## Open questions

What is undecided.

## Outcome (filled in by a maintainer)

Decision: accepted / rejected / deferred. Reasons. Date. Implemented in: version or pull request.
