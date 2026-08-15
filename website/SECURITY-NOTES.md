# Dependency notes

The docs site is a Docusaurus install, so its dependency tree is Docusaurus's
rather than this project's. Nothing here is imported by `dun`; a
vulnerability in this tree affects the machine that *builds* the docs, not
anyone who installs the CLI.

## Overrides

`package.json` pins two transitive dependencies above what Docusaurus asks
for:

| Package | Docusaurus wants | Pinned to | Why |
|---|---|---|---|
| `serialize-javascript` | 6.0.2 | ^7.0.5 | RCE via `RegExp.flags` (high), CPU-exhaustion DoS (moderate) |
| `uuid` | 8.3.2 | ^11.1.1 | Missing buffer bounds check in v3/v5/v6 (moderate) |

Both verified after the override: the versions resolve as pinned and
`npm run build` succeeds.

## Known and unfixable: `image-size`

`@docusaurus/mdx-loader` depends on `image-size@^2.0.2`, which has two open
high-severity advisories — infinite loops in the ICNS, JXL and HEIF parsers,
both denial of service.

**There is no fixed version to upgrade to.** 2.0.2 is the latest published
release, and the newest Docusaurus (3.10.2, what we run) still requires it.
An override cannot point at a version that does not exist.

The 17 remaining `npm audit` findings are all this one package: every
`@docusaurus/*` entry is flagged because it depends on it, not for anything
in its own code.

**Why this is acceptable here.** The parsers reached by those advisories run
over images passed to the MDX loader at build time. The only images in this
site are the five dashboard PNGs in `static/img/`, committed by us. An
attacker would need to land a malicious ICNS, JXL or HEIF file in the
repository first — at which point they have commit access and a build-time
DoS is the least of it.

Re-check when Docusaurus publishes a release that moves off it:

```sh
npm view @docusaurus/mdx-loader@latest dependencies.image-size
```
