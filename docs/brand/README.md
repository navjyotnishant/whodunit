# Brand

Two overlapping sets, with the intersection solid.

The mark is the mechanism rather than a picture of one: a commit reaches
`method=intersected` exactly when the lines an agent produced are the lines
that were staged, and the overlap is that evidence.

| File | Use |
|---|---|
| `logo.svg` | the mark, 64×64 viewBox, scales anywhere |
| `avatar.png` | 512×512 — GitHub org/repo avatar |
| `social-preview.png` | 1280×640 — **Settings → Social preview** |

## Regenerating

```sh
rsvg-convert -w 512  -h 512 avatar.svg         -o avatar.png
rsvg-convert -w 1280 -h 640 social-preview.svg -o social-preview.png
```

The SVGs are the source; the PNGs are build output committed because GitHub
and social platforms will not take an SVG.

## Two things that are not arbitrary

**Solid lens against soft discs.** Two stroked circles read well at 64px and
collapse into a smear at 16, which is the size a favicon is actually seen at.
Weight contrast survives where stroke contrast does not — measured by
rendering three candidates at 16/32/64 rather than judging at full size.

**The social preview is a separate file from the docs card.** GitHub crops a
repo's preview harder than Slack or X do; an earlier version reused the docs
card and lost its tagline off the right edge in the repo-card view.
