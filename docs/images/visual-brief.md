# ZAKS Drive visual identity

## Assets

| File | Use |
|---|---|
| `logo.svg` | The symbol (a Z drawn as a connection between two nodes) on a rounded navy square. Master for the GitHub avatar, Docker Hub, favicons. Readable at 64 px |
| `logo-512.png` | Raster export of the symbol, 512 x 512 |
| `social-preview.svg`, `social-preview.png` | GitHub social preview, 1280 x 640 |

Both the symbol and the banner are original artwork for this project. They use no SAP, Microsoft or SharePoint logos, marks or lookalikes: those products appear only as plain text in the banner's flow diagram.

## Name

"ZAKS Drive" with a space in prose; `zaks-drive` in technical identifiers. Tagline: *Open-Source Enterprise Content Gateway*. Banner headline: *Your SAP documents. Your SharePoint storage.*

## Palette

| Role | Hex | Use |
|---|---|---|
| Ink | `#0B1F33` | Background, symbol tile |
| Ink light | `#0E2A44` | Gradient end |
| Accent | `#2F9E8F` | Nodes, arrows, tagline |
| Paper | `#F4F7FA` | Text and the Z stroke |
| Muted | `#8DA2B5` | Secondary text |

Avoid SAP blue and gold and Microsoft's four-colour mark, so the project does not look affiliated.

## Rules

- Keep the symbol on its navy tile when used on light backgrounds. Minimum size 24 px.
- Do not add a cloud, arrow or drive icon to the symbol.
- The trademark disclaimer lives in the README, not on the banner.

## Regenerating the images

```bash
rsvg-convert -w 1280 -h 640 social-preview.svg -o social-preview.png
rsvg-convert -w 512 -h 512 logo.svg -o logo-512.png
```

Fonts: the SVGs name Helvetica/Arial and fall back to the system sans-serif (the PNGs were rendered with DejaVu Sans). Uploading the social preview and the avatar to GitHub is an owner action (Settings, Social preview; the account or organisation avatar).
