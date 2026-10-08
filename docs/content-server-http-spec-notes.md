# SAP Content Server HTTP 4.5 Interface – Spec Notes

How this was gathered: help.sap.com pages are rendered with JavaScript, so the content was pulled from the portal's JSON API instead:
`https://help.sap.com/http.svc/pagecontent?deliverableInfo=1&deliverable_id=40405694&buildNo=916&file_path=<loio>.html`
(deliverable "Knowledge Provider (BC-SRV-KPR)", product ABAP_PLATFORM_NEW, version 202510.001 = "2025 FPS01 (Feb 2026)", build 916).
All 35 pages in the tree under `4d0551f0eaa85c4be10000000a42189e` were downloaded and read in full.

Base URL for every source below: `https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/<ID>.html?version=202510.001`. Each section names the `<ID>` it came from.

Quotes are verbatim, including SAP's own typos and inconsistencies.

## Page tree (title – ID)

- SAP Content Server HTTP 4.5 Interface – 4d0551f0eaa85c4be10000000a42189e
  - Introduction – 4d060de30dd02136e10000000a42189e
    - Definitions of Terms – 4d02383939f22136e10000000a42189e
    - Implementation – 4d05510feaa85c4be10000000a42189e
    - Security – 4d013d0264a82102e10000000a42189e
      - secKey – 4d007cc099aa1d6ee10000000a42189c
      - Security Level / Access Permissions – 4d0553c8eaa85c4be10000000a42189e
  - Syntax Description – 4d0651c40e33606be10000000a42189e
    - General – 4d02362a39f22136e10000000a42189e
    - URL Encoding – 4d06496c0dd02136e10000000a42189e
    - Code in the Response Body – 4d053835eaa85c4be10000000a42189e
    - Functions – 4d01230f64a82102e10000000a42189e
      - Access Functions – 4d0651360e33606be10000000a42189e
        - info – 4d00723399aa1d6ee10000000a42189c
        - get – 4d06fb6f43e05dc6e10000000a42189c
        - docGet – 4d063b424132468de10000000a42189c
        - create – 4d01210264a82102e10000000a42189e
          - create with HTTP-PUT – 4d0578e0eaa85c4be10000000a42189e
          - create with HTTP-POST multipart/form-data – 4d057599eaa85c4be10000000a42189e
        - mCreate – 4d00755b99aa1d6ee10000000a42189c
        - append – 4d0611060dd02136e10000000a42189e
        - Update – 4d007a0b99aa1d6ee10000000a42189c
          - update with HTTP-PUT – 4d064f8a0e33606be10000000a42189e
          - update with HTTP-POST multipart/form-data – 4d007a8c99aa1d6ee10000000a42189c
        - delete – 4d057a8eeaa85c4be10000000a42189e
        - search – 4d0078f299aa1d6ee10000000a42189c
        - attrSearch – 4d01181264a82102e10000000a42189e
      - Administration Functions – 4d05279beaa85c4be10000000a42189e
        - putCert – 4d0077df99aa1d6ee10000000a42189c
        - serverInfo – 4d00798f99aa1d6ee10000000a42189c
    - Error Codes – 4d060e8c0dd02136e10000000a42189e
  - Cache Server and Interface Version 4.6 – 4d02395139f22136e10000000a42189e
  - Appendix – 4d0235fe39f22136e10000000a42189e
    - Parameters and Keywords – 4d0538d8eaa85c4be10000000a42189e
    - Migrating Existing Archives – 4d0536fdeaa85c4be10000000a42189e

The tree has no separate pages for "character sets", "response formats" or "status codes". Those topics are covered by Parameters and Keywords, Code in the Response Body, and Error Codes.

---

## General rules

- **URL syntax** (General, 4d02362a…): `http://servername:port/script?command&parameters`. "The URL may not contain any blank spaces."
- **Parameters** (Parameters and Keywords, 4d0538d8…): "A parameter appears no more than once in a URL."
- **Time zone** (Definitions of Terms, 4d02383939…): "UTC … is used for all expressions of time in this specification."
- **Date and time formats** (Parameters and Keywords):
  - dateC, dateM, compDateC, compDateM, serverDate and contRepDate use `YYYY-MM-DD`.
  - timeC, timeM, compTimeC, compTimeM, serverTime and contRepTime use `HH:MM:SS`.
  - expiration: "Expiry time of a signed URL, in UTC format: YYYYMMDDHHMMSS."
- **URL encoding** (4d06496c…):
  - Only ASCII characters (0x00–0x7F) may appear. 0x00–0x1F and 0x7F must be %-encoded.
  - "Unsafe" characters must be encoded: space `< > " # % { } | \ ^ ~ [ ] \``.
  - "Reserved characters must also be encoded": `; / ? : @ = &`.
  - secKey is binary, so it is Base64-encoded and then URL-encoded (example: `g3AhQg==` becomes `g3AhQg%3D%3D`).
- **ASCII response bodies** (Code in the Response Body, 4d053835…):
  - Format: `key1="value1";key2="value2";...keyn="value2";CRLF`
  - "Only printable ASCII characters may be used. If a value contains an inverted comma, this must be entered in addition to the inverted commas already inserted around the value." In other words, a quote inside a value is doubled.
- **Errors** (Functions, 4d01230f…, and Error Codes, 4d060e8c…): "If an error occurs, the content server must deliver an ASCII string describing the error. The error must be entered in the header field X-ErrorDescription."
- **serverStatusDescription** (Parameters and Keywords): "Header field in which the content server enters an explanatory text if an error occurs." Note that the serverInfo page describes it as a body keyword instead.
- **charset** (Parameters and Keywords):
  - "Describes the character set used to encode the component content (for example, ISO-8859-1; see also RFC 2046). Other values can be defined, but must have an X- placed before them. The character set is transferred as a Content-Type parameter."
  - `version` is also sent as a Content-Type parameter.
- **compStatus / docStatus** take the values `online|offline`. contRepStatus and serverStatus take `running|stopped|error`.
- **compId conventions** (Parameters and Keywords and Migrating Existing Archives, 4d0536fd…):
  - Standard IDs are `data`, `descr` and `note`. Multi-file documents use `data1`, `data2`, … and then have no `data` component.
  - MIME types: note = `application/x-note`, descr = `application/x-alf-descr`.
- **Content-Disposition** (Parameters and Keywords): "Content-Disposition can be transferred with compId as an additional parameter if documents are being transferred as multipart/form-data … If Content-Disposition is used in this way, it must be consistent with X-compId. Note: You can ignore this parameter."
- **Access modes** (Security Level, 4d0553c8…):
  - r = Read, c = Create, u = Change, d = Delete. Combinations such as `ud` and `rd` are allowed.
  - "If the access mode is "change", any component of a document may be deleted."
- **docProt** (Security Level, 4d0553c8…):
  - A combination of r/c/u/d that is set at create time and "applies to all components of a document".
  - Leaving docProt out means the server default applies. `docProt=` with an empty value "specifies explicitly that the security level has not been set" (create POST page).

## Security / secKey

Sources: secKey (4d007cc0…), URL Encoding (4d06496c…), Security Level (4d0553c8…).

- **Always signed**: contRep, accessMode, authId, expiration. Additional parameters are listed per function (the "Sign" column).
- **Function name**: "The name of the function itself is not signed."
- **Parameter order**: "The parameters to be signed can appear in the URL in any order. However, the order in which the parameters are transferred to the signing module must be the same as the order in the URL." So the message follows the parameters' order in the URL. It is not a fixed order.
- **Values only, no separators.** Worked example (URL Encoding):
  - URL: `get&pVersion=0046&contRep=K1&docId=361A…45EC&accessMode=r&authId=pawdf054_BCE_26&expiration=19981104091537`
  - "the parameter values are summarized to form a message (without separators), in accordance with the sequence in the URL"
  - Resulting message: `K1361A524A3ECB5459E0000800099245ECrpawdf054_BCE_2619981104091537`
- **authId and expiration** are both included.
- **pVersion and secKey are not signed.** They have no X in any Sign column.
- **Optional parameters**: "Optional parameters can clearly only be signed if they are used."
- **s-mandatory parameters**: "s-mandatory parameters must appear in the URL if a signature is used." If a secKey is present, accessMode, authId and expiration must also be present.
- **Encoding of values before signing**: not stated. The example uses plain values. It is unclear whether URL-decoded or raw values are signed.
- **Expired URLs**: "If the expiry time is exceeded, the content server must report HTTP status code 401." Expiry is UTC in `yyyymmddhhmmss`.
- **Signature technology**:
  - "SSF module uses the Digital Signature Standard (DSS) to digitally sign the hash value according to PKCS#7."
  - Summary table: Format "PKCS#7 "Signed Data""; Public key procedure DSS; Key length 512–1024 bits; Public exponent 2^16+1; Public key format X.509 v3 certificate; MD algorithm "MD5 or RIPEMD-160".
  - "secKey … is about 500 bytes long."
  - The spec never says "detached". It says the content server rebuilds the message from the URL and compares hashes, which implies the signed content is not embedded, but this is not stated explicitly.
- **Certificate identification**:
  - authId = "unique identification of the client (such as the SAP system)".
  - putCert transfers "The client certificate and a client identification (authId)".
  - Example authId values: `pawdf054_BCE_26` and `CN%3DKPR`.
- **When to check**:
  - The server decides from the document's docProt and the operation's access type whether to check the secKey. If no check is needed, the s-mandatory parameters "become obsolete".
  - "Old data … have the highest security level."
- **delete reusing a get URL**: a get URL signed with `accessMode=rd` can be reused for delete by changing the command and dropping compId. "The same parameters are signed for both get and delete." This is consistent with compId not being signed for get.

## Per-function details

Unless noted otherwise, accessMode, authId and expiration are s-mandatory and signed, and secKey is optional and unsigned.

### info (4d00723399aa1d6ee10000000a42189c)

- **Method**: HTTP GET. **Access mode**: r.
- **Parameters**:

  | Parameter | Required | Default | Signed |
  |---|---|---|---|
  | contRep | Mandatory | | X |
  | docId | Mandatory | | X |
  | compId | optional | | **not signed** |
  | pVersion | Mandatory | | |
  | resultAs | optional | ascii | |

- **Status codes**: 200 OK; 400 unknown function/parameter; 401 security breach; 404 document or component not found; 409 "Administrative data inaccessible"; 500.
- **Response headers**:
  - Content-Type, boundary, Content-Length (total body).
  - X-dateC (YYYY-MM-DD, UTC), X-timeC (HH:MM:SS), X-dateM, X-timeM.
  - X-numberComps.
  - **X-contentRep**. Both the table and the example use this name. docGet uses X-contRep instead.
  - X-docId, X-docStatus, X-pVersion.
- **Part headers** (one part per component):
  - Content-Type, with charset and version as Content-Type parameters.
  - **Content-Length: "Actual body size in the response, always 0 here"**.
  - **X-Content-Length: "Size of component in bytes"**.
  - X-compId, X-compDateC, X-compTimeC, X-compDateM, X-compTimeM, X-compStatus, X-pVersion.
- **resultAs=ascii (default)**: "The server sends a response in multipart/form-data format (see RFC 1867)… Each part represents one component. Each component has a component header and a component body. These have the length 0 because no component data is transferred… Therefore, the component parameter Content-Length is always set to 0. If you want to set the component length, you can do so using the parameter X-Content-Length."
- **Example part**:
  ```
  --A495ukjfasdfddrg4hztzu898aA0jklmAxcvla12319981147528895
  Content-Type: application/x-alf; charset=
  Content-Length: 0
  X-compId: descr
  X-Content-Length: 2591
  X-compDateC: 1998-10-07
  X-compTimeC: 07:55:57
  X-compDateM: 1998-10-07
  X-compTimeM: 07:55:57
  X-compStatus: online
  X-pVersion: 0045
  ```
- **Empty document**:
  ```
  --boundary
  --boundary--
  ```
- **resultAs=html**: "the server sends an HTML page. The structure of the HTML page is not specified."
- **With compId**: "info has the same effect as the command docGet, except that no component data is transferred". The response contains the document header plus that one component.

### get (4d06fb6f43e05dc6e10000000a42189c)

- **Method**: HTTP GET. **Access mode**: r.
- **Parameters**:

  | Parameter | Required | Default | Signed |
  |---|---|---|---|
  | contRep | Mandatory | | X |
  | docId | Mandatory | | X |
  | compId | optional | "see above" | **not signed** |
  | pVersion | Mandatory | | |
  | fromOffset | optional | 0 | |
  | toOffset | optional | -1 | |

- **Without compId**: return component "data" if it exists, otherwise "data1". Otherwise 404.
- **Offsets**:
  - Parameters and Keywords: fromOffset is "the beginning of a byte range within the component. The default is 0". toOffset "Specifies the end of a byte range within the component. The default -1 means that the search should continue to the end of the component. toOffset has priority over any content range that may be set."
  - The spec does **not** say whether toOffset is inclusive. Only the search page uses inclusive wording: "last character … smaller than or equal to toOffset".
- **Status codes**: 200 "OK, content unit of component is transferred"; 400; 401; 404; 409; 500. **206 is not mentioned.**
- **Response headers**:
  - Content-Type, with charset and version as Content-Type parameters ("must be transferred" if known).
  - Content-Length = total body.
  - The get page lists no X- headers.
- **Body**: the content unit, or the requested range of it.

### docGet (4d063b424132468de10000000a42189c)

- **Method**: GET. **Access mode**: r.
- **Parameters**: contRep (M, X), docId (M, X), pVersion (M).
- **Status codes**: 200, 400, 401, 404, 409, 500.
- **Body**: multipart/form-data, with the same part headers as info except "Content-Length and X-Content-Length have identical values".
  - Note: the spec's own example contradicts this (Content-Length 259 vs X-Content-Length 2591).
- **Response headers**:
  - The table lists **X-numComps** and **X-contRep**, unlike info's X-numberComps and X-contentRep.
  - The part-header table spells it "X-compdateC".
  - The example ends with `...-` (a single dash), which looks like a typo.
- **Empty document**: boundary followed by closing boundary.

### create (4d01210264…, PUT 4d0578e0…, POST 4d057599…)

- **Overview**:
  - "The create function always creates an entire document. If a component already exists in the Content Repository, the function returns an error. … Function create creates exactly one document."
  - PUT carries one component. POST multipart/form-data carries 0 to n components.
- **Access mode**: c.
- **PUT parameters**:
  - contRep (URL, X), compId (M, URL, **X**), docId (M, URL, X), pVersion (M, URL).
  - Content-Type, charset, version: optional, Body.
  - Content Length: M, "Header body".
  - docProt: optional, default server setting, URL, **X**.
  - Example URL contains `&Content-Length=300`.
- **POST parameters**:
  - contRep (URL, X), compId (M, **Body**, not signed), docId (URL, X), pVersion (URL), docProt (URL, X).
  - Content-Type, charset, version and Content Length go in the part headers.
- **POST part rules**:
  - "The CompId is transferred in field X-compId. The component length is in the field Content-Length. The parameters charset and version can be appended to the Content-Type."
  - Content-Disposition is optional and ignorable, but if present it must match X-compId (Parameters and Keywords).
  - "For HTTP-POST, the Content-Length in the request header is the total length of the body and the Content-Length in each part header is the length of the individual content units. For an HTTP PUT, the Content-Length is always the total length of the body."
  - "If an error occurs when storing a component, the entire action is canceled."
  - A create with 0 components is allowed: a body of `--KoZIhvcNAQcB` followed by `--KoZIhvcNAQcB--`.
- **Example part**:
  ```
  X-compId: data
  Content-Type: application/msword; charset=ISO-8859-1; version=6
  Content-Length: 4242
  ```
- **Status codes** (listed on the POST page; the PUT page has no table): **201** created; 400; 401; **403 "Document already exists"**; 500. The Error Codes page says 403 = "Document or component already exists" (create, mCreate).
- **Timestamps**: the server must set dateC/compDateC and timeC/compTimeC.

### mCreate (4d00755b99aa1d6ee10000000a42189c)

- **Method**: HTTP POST multipart/form-data. **Access mode**: c. Single contRep.
- **Parts**:
  - Each part carries X-docId (mandatory) and X-compId (mandatory), plus Content-Type (with charset and version) and Content-Length.
  - "Components of the same document must be transferred one after the other."
- **Parameters**:
  - docId position: "Body (1. docId also in URL)". Signed: "X (1. docId)". So the first docId is in the URL and signed.
  - contRep, docProt, accessMode, authId and expiration are in the URL and signed.
- **Transactions**: each document is its own transaction. The call as a whole is not.
- **Overall status codes**: 201 all created; **250 "missing documents created"** (only on repeated mCreate); 400; 401; 500.
- **Per-document retCode**: 201, 403 (already exists), 500.
- **Response body** (always returned):
  ```
  docId="string";retCode="integerstring";errorDescription="string";CRLF
  ```
  One line per document. errorDescription is optional.

### append (4d0611060dd02136e10000000a42189e)

- **Method**: HTTP PUT. **Access mode**: u.
- **Parameters**: contRep (X), docId (X), compId (M, **X**), pVersion (M), Content Length (M, Body). The request body is the data to append.
- **Precondition**: the document and the component must exist.
- **Status codes**: 200 "data appended"; 400; 401; 404; 409; 500.
- **Timestamps**: the server sets dateM/compDateM and timeM/compTimeM.

### update (4d007a0b…, PUT 4d064f8a…, POST 4d007a8c…)

- **Access mode**: u.
- **PUT**:
  - "is used to create or overwrite a single component of an existing document". The component is created if missing, but the document must exist.
  - Parameters: contRep, compId and docId are all in the URL and signed. pVersion; Content-Type, charset and version in the body; Content Length mandatory.
  - The PUT page has no status table.
- **POST**:
  - "the entire document is always overwritten, not just single components."
  - **"Document components not already in the content repository are created if necessary. Components in the content repository that are not transferred when the update function is executed are considered obsolete and deleted."**
  - compId goes in the body (X-compId) and is not signed.
- **Status codes** (POST page): 200 "OK, document(s)/component(s) changed"; 400; 401; 404; 409; 500.
- **Timestamps**: the server sets dateM, compDateM, compDateC, timeM, compTimeM and compTimeC.

### delete (4d057a8eeaa85c4be10000000a42189e)

- **Method**: "Client sends an **HTTP-GET** Request." HTTP DELETE is not mentioned anywhere.
- **Access mode**: d.
- **Parameters**:

  | Parameter | Required | Default | Signed |
  |---|---|---|---|
  | contRep | M | | X |
  | docId | M | | X |
  | compId | optional | "all components" | **X** |
  | pVersion | M | | |

  The Security Level page says to drop compId when reusing a get URL.
- **Status codes**: 200 "OK, document/component(s) deleted"; 400; 401; 404; 409; 500.

### search (4d0078f299aa1d6ee10000000a42189c)

- **Method**: GET. **Access mode**: r.
- **Parameters**:

  | Parameter | Required | Default | Signed |
  |---|---|---|---|
  | contRep | M | | X |
  | docId | M | | X |
  | pattern | M | | |
  | compId | M | | |
  | pVersion | M | | |
  | caseSensitive (y/n) | optional | n | |
  | fromOffset | optional | 0 | |
  | toOffset | optional | -1 | |
  | numResults | optional | 1 | |

- **Search direction**: if fromOffset > toOffset, the search runs backwards. Match bounds are inclusive. The hit position is the offset of the first character of the match.
- **Response**: `number;offset;offset;...`. "There are no blank spaces … There is a semicolon between the values and at the end." Example: `2;122;222;`
- **Status codes**: 200, 400, 401, 404, 409, 500.

### attrSearch (4d01181264a82102e10000000a42189e)

- **Method**: GET. **Access mode**: r. Always searches compId=descr; compId is not passed.
- **Parameters**: contRep (X), docId (X), pattern (M), pVersion, caseSensitive (n), fromOffset (0), toOffset (-1), numResults (1).
- **Pattern syntax**: `offset+length+value`, with multiple patterns separated by `#`. A `#` used as a separator is not encoded.
  - Example: `pattern=3+5+12345#15+25+GmbH&numResults=5`
- **descr file format**:
  - Lines end in LF.
  - Each line is `<offsetInData> <lengthInData> <RECTYPE><params>`, where RECTYPE is DPRL, DKEY, DAIN or DEPL.
  - DKEY params: attribute name (40 bytes), offset in DAIN param (3), length (3).
  - DAIN params: the attribute values, padded with blanks.
- **Response**: `number;offset;length;...`. Example: `2;73;138;211;120;`
  - No hits: 200 with result `0`.
  - Pattern does not fit the DKEY definitions: 400.

### putCert (4d0077df99aa1d6ee10000000a42189c)

- **Method**: "The client sends an HTTP-Put-Request." **Access mode**: none.
- **Parameters**: authId (M), pVersion (M), contRep (M). All go in the URL; no secKey.
- **Body**: "The certificate is transferred in the request body." Also: "The client certificate (see secKey) is decoded in the message body and transferred in binary format". The encoding (DER or PEM) is not stated beyond "binary".
- **Recommendation**: a manual administrator approval step before access is granted.
- **Status codes**: 200; 400; **406 "Certificate not recognized"**; 500.

### serverInfo (4d00798f99aa1d6ee10000000a42189c)

- **Method**: GET. **Access mode**: none.
- **Parameters**: contRep (optional, default "All repositories"), pVersion (M), resultAs (optional, default ascii). Nothing is signed.
- **Status codes**: 200, 400, 500.
- **Server keywords** (table): serverStatus (running/stopped/error), serverVendorId, serverVersion, serverBuild, serverTime (HH:MM:SS UTC), serverDate (YYYY-MM-DD UTC), serverStatusDescription, pVersion.
- **Repository keywords** (table): contRep, contRepDescription, contRepStatus (running/stopped/error), contRepStatusDescription.
- "Note: These parameters are mandatory. The parameter list is designed so that it can be extended."
- **resultAs=ascii format, verbatim**:
  ```
  serverStatus="string";serverVendorId="string";serverTime="string";serverDate="string";serverErrorDescription="string";pVersion="0046";CRLF
  contRep="string";contRepDescription="string";contRepStatus= "string";pVersion="0046";CRLF
  ```
  - "If no value is entered, the value remains free. contRepDescription="";contRepStatus="string";..."
  - "The order of the key words does not matter, but there must not be any blank characters."
  - The example's space after `contRepStatus=` contradicts the no-blanks rule and looks like a typo.
  - The ascii example uses serverErrorDescription, which differs from serverStatusDescription in the table. It also omits serverVersion and serverBuild.
- **resultAs=html**: the structure is unspecified.

## Error codes summary (4d060e8c0dd02136e10000000a42189e)

| Code | Meaning | Used by |
|---|---|---|
| 200 | info/component delivered/changed/appended/deleted | info, get, docGet, update, append, delete, putCert, search, attrSearch |
| 201 | created | create, mCreate |
| 250 | missing documents created | mCreate |
| 400 | unknown function or parameter | all |
| 401 | security breach | all except putCert and serverInfo |
| 403 | document or component already exists | create, mCreate |
| 404 | document, component, or content repository not found | |
| 406 | certificate not recognized | putCert |
| 409 | document, component or administration data inaccessible | |
| 500 | internal error | all |

Error text goes in the `X-ErrorDescription` header.

## pVersion

- **Parameters and Keywords**: "Versions 0021, 0030 and 0031 were defined for the ArchiveLink interface. The HTTP Content Server interface begins with version 0045."
- **Cache Server and Interface Version 4.6** (4d02395139…):
  - 0046 adds support for signed URLs between the content server and the SAP cache server.
  - A third-party server in a mixed environment "needs to be enhanced so that it supports all SAP Content Server HTTP Interface 4.5 commands. The new command getCert must also be implemented."
  - getCert is not specified anywhere in this tree. The page refers to SAP Note 216419.
- **0047**: not mentioned anywhere in the tree.
  - A web search found only third-party docs saying to "enter 0047" for content server version 4.7 (https://docs.alfresco.com/sap/5.1/config).
  - An older copy of the same topic exists at https://help.sap.com/saphelp_em92/helpdata/en/4d/0551f0eaa85c4be10000000a42189e/content.htm (not read separately).

## Could NOT verify (not in the spec)

- Whether toOffset is inclusive, and whether get with a range should ever return 206. Only 200 is listed.
- Whether HTTP DELETE is accepted for delete. The spec says only GET.
- Status codes for create via PUT and update via PUT. Those pages have no table; the codes above come from the POST pages and the Error Codes page.
- What update PUT returns when the document does not exist (404 is presumed).
- Whether signed values are URL-decoded before concatenation. Also whether docProt is included when present; the Sign column says yes, but there is no worked example.
- PKCS#7 "detached" vs embedded content, and the exact digest/signature algorithms modern SAP systems use. The spec still says DSS with MD5/RIPEMD-160.
- The certificate encoding in the putCert body (DER vs Base64/PEM).
- getCert syntax, and any differences in 0047.
- The info/docGet header naming inconsistency: X-contentRep/X-numberComps (info) vs X-contRep/X-numComps (docGet). Emitting both is a safe option.
- The time-header descriptions for X-timeC/X-compTimeC omit "(UTC)", but the global rule says all times are UTC.
- The ASCII format of serverInfo for 4.5 vs 4.6. The example hardcodes pVersion="0046".

---

## Appendix A: Signed-parameter matrix

This matrix is derived from the "Sign" column of each function's parameter table (verbatim tables follow in Appendix B).

- X means the parameter is signed.
- "-" means it is listed but not signed.
- Blank means it is not a parameter of that function.

Every function that has a Sign column also marks accessMode, authId and expiration (all s-mandatory) as signed. secKey and pVersion are never signed.

| Function | Method (spec wording) | contRep | docId | compId | docProt | pattern | Other unsigned |
|---|---|---|---|---|---|---|---|
| info | "Client sends an HTTP-GET Request" | X | X | - (optional) | | | resultAs |
| get | "Client sends an HTTP-GET Request" | X | X | - (optional) | | | fromOffset, toOffset |
| docGet | "Client sends an HTTP-GET Request" | X | X | | | | |
| create PUT | HTTP-PUT (overview page; the subpage has no method sentence) | X | X | X (URL, M) | X (optional) | | Content-Type, charset, version, Content-Length |
| create POST | HTTP-POST multipart/form-data (overview page) | X | X | - (Body, M) | X (optional) | | same |
| mCreate | "Client sends an HTTP-POST Request" | X | "X (1. docId)" (first docId, in the URL) | - (Body, X-compId) | X (optional) | | same |
| append | "client sends an HTTP-PUT-Request" | X | X | X (URL, M) | | | Content-Length (Body) |
| update PUT | HTTP-PUT (Update overview page) | X | X | X (URL, M) | | | Content-Type, charset, version, Content-Length |
| update POST | HTTP-POST multipart/form-data (Update overview page) | X | X | - (Body, M) | | | same |
| delete | "Client sends an HTTP-GET Request" | X | X | X (optional; default "all components") | | | |
| search | "Client sends an HTTP-GET Request" | X | X | - (Mandatory) | | - (M) | caseSensitive, fromOffset, toOffset, numResults |
| attrSearch | "Client sends an HTTP-GET Request" | X | X | | | - (M) | caseSensitive, fromOffset, toOffset, numResults |
| putCert | "client sends an HTTP-Put-Request" | M, Sign column empty | | | | | authId (M), pVersion (M); "The URL does not contain a secKey." |
| serverInfo | "Client sends an HTTP-GET Request" | optional, not signed | | | | | pVersion (M), resultAs (optional, default ascii); no accessMode/authId/expiration/secKey in the table |

## Appendix B: Verbatim parameter, status and header tables

These were extracted programmatically from the page HTML (`<table>` elements), with whitespace collapsed. Empty cells mean the cell is empty on the page.

The three extra pages the coordinator asked for are covered:
- Functions (4d01230f…), with all of its children.
- Administration Functions (4d05279b…), with its children putCert and serverInfo.
- General (4d02362a…).

The URL Encoding page is actually 4d06496c…; it was also read (see "General rules" above).

### info
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d00723399aa1d6ee10000000a42189c.html?version=202510.001

Method sentence (verbatim): "Client sends an HTTP-GET Request ."


| Parameter | Optional/Mandatory | Default | Sign |
|---|---|---|---|
| contRep | Mandatory |  | X |
| docId | Mandatory |  | X |
| compId | optional |  |  |
| pVersion | Mandatory |  |  |
| resultAs | optional | ascii |  |
| accessMode | s-mandatory |  | X |
| authId | s-mandatory |  | X |
| expiration | s-mandatory |  | X |
| secKey | optional |  |  |


| HTTP Status Code | Meaning |
|---|---|
| 200 (OK) | OK, information sent |
| 400 (bad request) | Unknown function or unknown parameter |
| 401 (unauthorized) | Security breach |
| 404 (not found) | Document or component not found |
| 409 (conflict) | Administrative data inaccessible |
| 500 (Internal Server Error) | Internal error on Content Server |


| Keyword | Format | Meaning |
|---|---|---|
| Content Type | String | Content type (if known) |
| boundary | String | Separator between individual components |
| Content Length | Integer string | Total length of body actually transferred |
| X-dateC | YYYY-MM-DD | Date of creation (UTC) |
| X-timeC | HH:MM:SS | Time of creation |
| X-dateM | YYYY-MM-DD | Date of the last change (UTC) |
| X-timeM | HH:MM:SS | Time of the last change (UTC) |
| X-numberComps | Integer string | Number of components |
| X-contentRep | String | Content Repository |
| X-docId | String | Document ID |
| X-docStatus | String | Status |
| X-pVersion | String | Version |


| Keyword | Format | Meaning |
|---|---|---|
| Content Type | String | Content-Type (if known) |
| charset | String | Character set (if known) |
| version | String | Application version used to create the content of the component |
| Content Length | Integer string | Actual body size in the response, always 0 here |
| X-Content-Length | Integer string | Size of component in bytes |
| X-compId | String | Component ID |
| X-compDateC | YYYY-MM-DD | Date of creation (UTC) |
| X-compTimeC | HH:MM:SS | Time of creation |
| X-compDateM | YYYY-MM-DD | Date of the last change (UTC) |
| X-compTimeM | HH:MM:SS | Time of the last change (UTC) |
| X-compStatus | String | Component status |
| X-pVersion | String | Interface Version |


### get
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d06fb6f43e05dc6e10000000a42189c.html?version=202510.001

Method sentence (verbatim): "Client sends an HTTP-GET Request ."


| Parameter | Optional/Mandatory | Default | Sign |
|---|---|---|---|
| contRep | Mandatory |  | X |
| docId | Mandatory |  | X |
| compId | optional | see above |  |
| pVersion | Mandatory |  |  |
| fromOffset | optional | 0 |  |
| toOffset | optional | -1 |  |
| accessMode | s-mandatory |  | X |
| authId | s-mandatory |  | X |
| expiration | s-mandatory |  | X |
| secKey | optional |  |  |


| HTTP Status Code | Meaning |
|---|---|
| 200 (OK) | OK, content unit of component is transferred |
| 400 (bad request) | Unknown function or unknown parameter |
| 401 (unauthorized) | Security breach |
| 404 (not found) | Document or component not found |
| 409 (conflict) | Document or component inaccessible |
| 500 (Internal Server Error) | Internal error on Content Server |


| Keyword | Meaning |
|---|---|
| Content Type | Content Type |
| charset | The character set of the component (as a content type parameter). |
| version | The version of the component (as a content type parameter). |
| Content Length | Total length of body actually transferred |


### docGet
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d063b424132468de10000000a42189c.html?version=202510.001

Method sentence (verbatim): "Client sends an HTTP-GET Request ."


| Parameter | Optional/Mandatory | Default | Sign |
|---|---|---|---|
| contRep | Mandatory |  | X |
| docId | Mandatory |  | X |
| pVersion | Mandatory |  |  |
| accessMode | s-mandatory |  | X |
| authId | s-mandatory |  | X |
| expiration | s-mandatory |  | X |
| secKey | optional |  |  |


| HTTP Status Code | Meaning |
|---|---|
| 200 (OK) | OK, document is transferred |
| 400 (bad request) | Unknown function or unknown parameter |
| 401 (unauthorized) | Security breach |
| 404 (not found) | Document or component not found |
| 409 (conflict) | Document or component inaccessible |
| 500 (Internal Server Error) | Internal error on Content Server |


| Keyword | Format | Meaning |
|---|---|---|
| Content Type | String | Content type, always multipart/form-data |
| boundary | String | Separator between individual components |
| Content Length | Integer string | Total length of body actually transferred |
| X-dateC | YYYY-MM-DD | Date of creation (UTC) |
| X-timeC | HH:MM:SS | Time of creation |
| X-dateM | YYYY-MM-DD | Date of the last change (UTC) |
| X-timeM | HH:MM:SS | Time of the last change (UTC) |
| X-numComps | Integer string | Number of components |
| X-contRep | String | Content Repository |
| X-docId | String | Document ID |
| X-docStatus | String | Status |
| X-pVersion | String | Version |


| Keyword | Format | Meaning |
|---|---|---|
| Content Type | String | Content type (if known) |
| charset | String | Character set (if known) |
| version | String |  |
| Content Length | Integer string | Size of component in bytes |
| X-Content-Length | Integer string | Size of component in bytes |
| X-compId | String | Component ID |
| X-compdateC | YYYY-MM-DD | Date of creation (UTC) |
| X-compTimeC | HH:MM:SS | Time of creation |
| X-compDateM | YYYY-MM-DD | Date of the last change (UTC) |
| X-compTimeM | HH:MM:SS | Time of the last change (UTC) |
| X-compStatus | String | Component status |
| X-pVersion | String | Interface Version |


### create (overview)
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d01210264a82102e10000000a42189e.html?version=202510.001


### create with HTTP-PUT
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d0578e0eaa85c4be10000000a42189e.html?version=202510.001


| Parameter | Optional/Mandatory | Default | Position | Sign |
|---|---|---|---|---|
| contRep | Mandatory |  | URL | X |
| compId | Mandatory |  | URL | X |
| docId | Mandatory |  | URL | X |
| pVersion | Mandatory |  | URL |  |
| Content Type | optional |  | Body |  |
| charset | optional |  | Body |  |
| version | optional |  | Body |  |
| Content Length | Mandatory |  | Header body |  |
| docProt | optional | Server setting | URL | X |
| accessMode | s-mandatory |  | URL | X |
| authId | s-mandatory |  | URL | X |
| expiration | s-mandatory |  | URL | X |
| secKey | optional |  | URL |  |


### create with HTTP-POST multipart/form-data
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d057599eaa85c4be10000000a42189e.html?version=202510.001


| Parameter | Optional/Mandatory | Default | Position | Sign |
|---|---|---|---|---|
| contRep | Mandatory |  | URL | X |
| compId | Mandatory |  | Body |  |
| docId | Mandatory |  | URL | X |
| pVersion | Mandatory |  | URL |  |
| Content Type | optional |  | Body |  |
| charset | optional |  | Body |  |
| version | optional |  | Body |  |
| Content Length | Mandatory |  | Header body |  |
| docProt | optional | Server setting | URL | X |
| accessMode | s-mandatory |  | URL | X |
| authId | s-mandatory |  | URL | X |
| expiration | s-mandatory |  | URL | X |
| secKey | optional |  | URL |  |


| HTTP Status Code | Meaning |
|---|---|
| 201(created) | OK, document(s) created |
| 400 (bad request) | Unknown function or unknown parameter |
| 401 (unauthorized) | Security breach |
| 403 (forbidden) | Document already exists |
| 500 (Internal Server Error) | Internal error on Content Server |


### mCreate
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d00755b99aa1d6ee10000000a42189c.html?version=202510.001

Method sentence (verbatim): "Client sends an HTTP-POST Request ."


| Parameter | Header Field in Body | Optional/Mandatory | Default | Position | Sign |
|---|---|---|---|---|---|
| contRep |  | Mandatory |  | URL | X |
| compId | " X-compId " | Mandatory |  | Body |  |
| docId | " X-docId " | Mandatory |  | Body (1. docId also in URL) | X (1. docId ) |
| pVersion |  | Mandatory |  | URL |  |
| Content Type |  | optional |  | Body |  |
| charset |  | optional |  | Body |  |
| version |  | optional |  | Body |  |
| Content Length |  | Mandatory |  | Body |  |
| docProt |  | optional | Server setting | URL | X |
| accessMode |  | s-mandatory |  | URL | X |
| authId |  | s-mandatory |  | URL | X |
| expiration |  | s-mandatory |  | URL | X |
| secKey |  | optional |  | URL |  |


| General HTTP Status Codes | Meaning |
|---|---|
| 201(created) | OK, all documents were created |
| 250 (missing documents created) | OK, all missing documents were created Note This status can occur only in the case of repeated mcreate calls. |
| 400 (bad request) | Unknown function or unknown parameter |
| 401 (unauthorized) | Security breach |
| 500 (Internal Server Error) | Internal error on Content Server |


| Special HTTP Status Code | Meaning |
|---|---|
| 201(created) | OK, document created |
| 403 (forbidden) | Document already exists |
| 500 (Internal Server Error) | Internal error on Content Server |


| Keyword | Format | Meaning |
|---|---|---|
| docId | string | Document ID |
| retCode | Integer string | HTTP Status Code |
| errorDescription | string | Text explaining the error (optional) |


### append
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d0611060dd02136e10000000a42189e.html?version=202510.001

Method sentence (verbatim): "client sends an HTTP-PUT-Request ."


| Parameter | Optional/Mandatory | Default | Position | Sign |
|---|---|---|---|---|
| contRep | Mandatory |  | URL | X |
| docId | Mandatory |  | URL | X |
| compId | Mandatory |  | URL | X |
| pVersion | Mandatory |  | URL |  |
| accessMode | s-mandatory |  | URL | X |
| authId | s-mandatory |  | URL | X |
| expiration | s-mandatory |  | URL | X |
| secKey | optional |  | URL |  |
| Content Length | Mandatory |  | Body |  |


| HTTP Status Code | Meaning |
|---|---|
| 200 (OK) | OK, data appended |
| 400 (bad request) | Unknown function or unknown parameter |
| 401 (unauthorized) | Security breach |
| 404 (not found) | Document or component not found |
| 409 (conflict) | Document or component inaccessible |
| 500 (Internal Server Error) | Internal error on Content Server |


### update (overview)
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d007a0b99aa1d6ee10000000a42189c.html?version=202510.001


### update with HTTP-PUT
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d064f8a0e33606be10000000a42189e.html?version=202510.001


| Parameter | Optional/Mandatory | Default | Position | Sign |
|---|---|---|---|---|
| contRep | Mandatory |  | URL | X |
| compId | Mandatory |  | URL | X |
| docId | Mandatory |  | URL | X |
| pVersion | Mandatory |  | URL |  |
| Content Type | optional |  | Body |  |
| charset | optional |  | Body |  |
| version | optional |  | Body |  |
| Content Length | Mandatory |  | Body |  |
| accessMode | s-mandatory |  | URL | X |
| authId | s-mandatory |  | URL | X |
| expiration | s-mandatory |  | URL | X |
| secKey | optional |  | URL |  |


### update with HTTP-POST multipart/form-data
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d007a8c99aa1d6ee10000000a42189c.html?version=202510.001


| Parameter | Optional/Mandatory | Default | Position | Sign |
|---|---|---|---|---|
| contRep | Mandatory |  | URL | X |
| compId | Mandatory |  | Body |  |
| docId | Mandatory |  | URL | X |
| pVersion | Mandatory |  | URL |  |
| Content Type | optional |  | Body |  |
| charset | optional |  | Body |  |
| version | optional |  | Body |  |
| Content Length | Mandatory |  | Body |  |
| accessMode | s-mandatory |  | URL | X |
| authId | s-mandatory |  | URL | X |
| expiration | s-mandatory |  | URL | X |
| secKey | optional |  | URL |  |


| HTTP Status Code | Meaning |
|---|---|
| 200 (OK) | OK, document(s)/component(s) changed |
| 400 (bad request) | Unknown function or unknown parameter |
| 401 (unauthorized) | Security breach |
| 404 (not found) | Document or component not found |
| 409 (conflict) | Document or component inaccessible |
| 500 (Internal Server Error) | Internal error on Content Server |


### delete
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d057a8eeaa85c4be10000000a42189e.html?version=202510.001

Method sentence (verbatim): "Client sends an HTTP-GET Request ."


| Parameter | Optional/Mandatory | Default | Sign |
|---|---|---|---|
| contRep | Mandatory |  | X |
| docId | Mandatory |  | X |
| compId | optional | all components | X |
| pVersion | Mandatory |  |  |
| accessMode | s-mandatory |  | X |
| authId | s-mandatory |  | X |
| expiration | s-mandatory |  | X |
| secKey | optional |  |  |


| HTTP Status Code | Meaning |
|---|---|
| 200 (OK) | OK, document/component(s) deleted |
| 400 (bad request) | Unknown function or unknown parameter |
| 401 (unauthorized) | Security breach |
| 404 (not found) | Document or component not found |
| 409 (conflict) | Document or component inaccessible |
| 500 (Internal Server Error) | Internal error on Content Server |


### search
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d0078f299aa1d6ee10000000a42189c.html?version=202510.001

Method sentence (verbatim): "Client sends an HTTP-GET Request ."


| Parameter | Optional/Mandatory | Default | Sign |
|---|---|---|---|
| contRep | Mandatory |  | X |
| docId | Mandatory |  | X |
| pattern | Mandatory |  |  |
| compId | Mandatory |  |  |
| pVersion | Mandatory |  |  |
| caseSensitive | optional | n |  |
| fromOffset | optional | 0 |  |
| toOffset | optional | -1 |  |
| numResults | optional | 1 |  |
| accessMode | s-mandatory |  | X |
| authId | s-mandatory |  | X |
| expiration | s-mandatory |  | X |
| secKey | optional |  |  |


| HTTP Status Code | Meaning |
|---|---|
| 200 (OK) | OK, component was searched |
| 400 (bad request) | Unknown function or unknown parameter |
| 401 (unauthorized) | Security breach |
| 404 (not found) | Document or component not found |
| 409 (conflict) | Document or component inaccessible |
| 500 (Internal Server Error) | Internal error on Content Server |


### attrSearch
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d01181264a82102e10000000a42189e.html?version=202510.001

Method sentence (verbatim): "Client sends an HTTP-GET Request ."


| Offset and Length in the data file | Record type | Parameter |  |  |  |  |
|---|---|---|---|---|---|---|
| 0 | 72 | DPRL |  |  |  |  |
| 73 | 0 | DKEY | Client | 0 | 3 |  |
| 73 | 0 | DKEY | Company code | 3 | 5 |  |
| 73 | 0 | DKEY | Account number | 8 | 7 |  |
| 73 | 0 | DKEY | Customer name | 15 | 25 |  |
| 73 | 138 | DAIN | 001 | 0001 | 0147119 | BröselPlc |
| 211 | 120 | DAIN | 001 | 0002 | 0147129 | ObelixPlc |
| ... |  |  |  |  |  |  |
| 1147 | 1 | DEPL |  |  |  |  |


| Content | Length (bytes) |
|---|---|
| Offset in Data File | Variable |
| Separator (space) | 1 |
| Length in Data File | Variable |
| Separator (space) | 1 |
| Record type ("DKEY") | 4 |
| Attribute names | 40 |
| Offset in the DAIN Line Parameter | 3 |
| Length in the DAIN Line Parameter | 3 |


| Content | Length (bytes) |
|---|---|
| Offset in Data File | Variable |
| Separator (space) | 1 |
| Length in Data File | Variable |
| Separator (space) | 1 |
| Record type ("DAIN") | 4 |
| Parameter | Variable |


| Attribute Name | Offset in the DAIN Line Parameter | Length in the DAIN Line Parameter |
|---|---|---|
| Client | 0 | 3 |
| Company code | 3 | 5 |
| Account number | 8 | 7 |
| Customer name | 15 | 25 |


| Offset in Data File | Length in Data File | Attribute Name | Attribute Value |
|---|---|---|---|
| 73 | 138 | Client | "001" |
| Company code | "00001" |  |  |
| Account number | "0147119" |  |  |
| Customer name | "BröselPlc" |  |  |
| 211 | 120 | Client | "001" |
| Company code | "00002" |  |  |
| Account number | "0147129" |  |  |
| Customer name | "ObelixPlc" |  |  |
| ... | ... | ... | ... |


| Parameter | Optional/Mandatory | Default | Sign |
|---|---|---|---|
| contRep | Mandatory |  | X |
| docId | Mandatory |  | X |
| pattern | Mandatory |  |  |
| pVersion | Mandatory |  |  |
| caseSensitive | optional | n |  |
| fromOffset | optional | 0 |  |
| toOffset | optional | - 1 |  |
| numResults | optional | 1 |  |
| accessMode | s-mandatory |  | X |
| authId | s-mandatory |  | X |
| expiration | s-mandatory |  | X |
| secKey | optional |  |  |


| HTTP Status Code | Meaning |
|---|---|
| 200 (OK) | OK, component was searched |
| 400 (bad request) | Unknown function or unknown parameter |
| 401 (unauthorized) | Security breach |
| 404 (not found) | Document or component not found |
| 409 (conflict) | Document or component inaccessible |
| 500 (Internal Server Error) | Internal error on Content Server |


### putCert
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d0077df99aa1d6ee10000000a42189c.html?version=202510.001

Method sentence (verbatim): "client sends an HTTP-Put-Request ."


| Parameter | Optional/Mandatory | Sign |
|---|---|---|
| authId | Mandatory |  |
| pVersion | Mandatory |  |
| contRep | Mandatory |  |


| HTTP Status Code | Meaning |
|---|---|
| 200 (OK) | OK |
| 400 (bad request) | Unknown function or unknown parameter |
| 406 (not acceptable) | Certificate not recognized |
| 500 (Internal Server Error) | Internal error on Content Server |


### serverInfo
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d00798f99aa1d6ee10000000a42189c.html?version=202510.001

Method sentence (verbatim): "Client sends an HTTP-GET Request ."


| Parameter | Optional/Mandatory | Default | Sign |
|---|---|---|---|
| contRep | optional | All repositories |  |
| pVersion | Mandatory |  |  |
| resultAs | optional | ascii |  |


| HTTP Status Code | Meaning |
|---|---|
| 200 (OK) | OK |
| 400 (bad request) | Unknown function or unknown parameter |
| 500 (Internal Server Error) | Internal error on Content Server |


| Keyword | Format | Meaning |
|---|---|---|
| serverStatus |  | Status of content server (running/ stopped/ error) |
| serverVendorId |  | Manufacturer and software version |
| serverVersion |  | Version of server |
| serverBuild |  | Build of server |
| serverTime | HH:MM:SS | Content server time (UTC) |
| serverDate | YYYY-MM-DD | Content server date (UTC) |
| serverStatusDescription |  | Text describing server status |
| pVersion |  | Content server interface version |


| Keyword | Format | Meaning |
|---|---|---|
| contRep |  | Content Repository |
| contRepDescription |  | Text describing content of the repository content |
| contRepStatus |  | Status of content repository (running/ stopped/ error) |
| contRepStatusDescription |  | Text describing content repository status |


### Functions (overview)
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d01230f64a82102e10000000a42189e.html?version=202510.001


| Command | Effect | Access Mode |
|---|---|---|
| info | Retrieve information about the document | r |
| get | Fetch (within a range) a content unit of a component | r |
| docGet | Fetch the entire content of a document | r |
| create | Create a new document | c |
| mCreate | Creates a number of new documents | c |
| append | Append data to a content unit | u |
| Update | Modify an existing document | u |
| delete | Delete a document or a component | d |
| search | Search for a text pattern within a content unit | r |
| attrSearch | Search for one or more attributes within a document (search within a print list) | r |
| putCert | Transfer client (for example, the SAP system) certificate | - |
| serverInfo | Retrieve information about the content server and the corresponding content repositories. | - |


### Error Codes
Source: https://help.sap.com/docs/ABAP_PLATFORM_NEW/3ad3ba0715c5422eae08578d4c40328d/4d060e8c0dd02136e10000000a42189e.html?version=202510.001


| HTTP Status Code | Meaning | Used For |
|---|---|---|
| 200 (OK) | OK, information or component was delivered/transferred/changed/appended/deleted | info, get, docGet, update, append, delete, putCert, search, attrSearch |
| 201(created) | OK, component(s) created (if create was used) OK, all documents created (if mCreate was used) | create, mCreate |
| 250 (missing documents created) | OK, all missing documents were created | mCreate |
| 400 (bad request) | Unknown function or unknown parameter | All functions |
| 401 (unauthorized) | Security breach | info, get, docGet, create, update, append, delete, mCreate, search, attrSearch |
| 403 (forbidden) | Document or component already exists | create, mCreate |
| 404 (not found) | Document, component, or content repository not found | info, get, docGet, update, append, delete, search, attrSearch |
| 406 (not acceptable) | Certificate not recognized | putCert |
| 409 (conflict) | Document, component, or administration data inaccessible | info, get, docGet, append, update, delete, search, attrSearch |
| 500 (Internal Server Error) | Internal error on Content Server | All functions |
