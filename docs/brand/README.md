# Brand

Two overlapping sets, with the intersection between them.

The mark is the mechanism rather than a picture of one: a commit reaches
`method=intersected` exactly when the lines an agent produced are the lines
that were staged, and the overlap is that evidence.

| File | Use |
|---|---|
| `logo.svg` | light backgrounds — dark lens |
| `logo-dark.svg` | dark backgrounds — light lens |
| `logo-lined.svg` / `logo-lined-dark.svg` | 96px and above, lens carries code lines |
| `avatar.png` | 512×512 — GitHub org/repo avatar |
| `social-preview.png` | 1280×640 — **Settings → Social preview** |

## Palette

| | |
|---|---|
| `#a159ed` | the agent's lines |
| `#26c9ea` | the lines in the commit |
| `#0e1317` | ground |

Taken from `docs/architecture/the-intersection.png`, so the mark and the
diagram that explains it are the same drawing at two scales.

## Three decisions that are not arbitrary

**The lens is each theme's own extreme, not a mid-tone.** A periwinkle that
split the difference was tried and is worse in both themes than white-on-dark
and dark-on-light are in theirs. A lens the colour of the ground is worse
still: it reads as a *gap* between two shapes rather than an overlap, which
inverts the meaning. Measured at ~1.0 contrast — invisible.

That is why there are two files rather than one. An SVG loaded through
`<img src>` is a separate document and cannot inherit `currentColor` from the
page, and both the Docusaurus navbar and GitHub use `<img>`. The docs site
swaps them with `srcDark`; the favicon carries a `prefers-color-scheme` rule
inside the file, since browser chrome has no framework to theme it.

**Lines only at 96px and above.** They read as code at 128 and as noise at 16,
where they also stop the lens reading as a lens. Four constructions were
rendered at 16/32/64 before any was judged.

**Line weight is 1.5, not 3.2.** Thicker reads as bars rather than text.
Thinner than 1.5 starts disappearing at 96px, which is the floor.

## Regenerating

```sh
rsvg-convert -w 512  -h 512 avatar.svg         -o avatar.png
rsvg-convert -w 1280 -h 640 social-preview.svg -o social-preview.png
```

The SVGs are the source; the PNGs are committed because GitHub takes no SVG
for an avatar or a social preview.
