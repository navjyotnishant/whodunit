// Author: Navjyot Nishant
// Created: 2026-08-15
// Last updated: 2026-08-15
// Description: Docusaurus configuration for the whodunit documentation site.

import type * as Preset from '@docusaurus/preset-classic';
import type {Config} from '@docusaurus/types';
import {themes as prismThemes} from 'prism-react-renderer';

const config: Config = {
  title: 'whodunit',
  tagline: 'AI-attribution provenance in git trailers, so productivity claims tie to evidence',
  favicon: 'img/favicon.ico',

  // GitHub Pages serves a project site under /<repo>/, so baseUrl must
  // carry that prefix or every asset 404s once deployed while working
  // perfectly on localhost — the failure mode that makes this worth
  // stating rather than leaving to the reader of a config file.
  url: 'https://navjyotnishant.github.io',
  baseUrl: '/whodunit/',

  organizationName: 'navjyotnishant',
  projectName: 'whodunit',
  trailingSlash: false,

  // Fail the build on a broken internal link rather than shipping one.
  // A docs site whose links rot is worse than no docs site: it looks
  // maintained.
  onBrokenLinks: 'throw',

  markdown: {
    // Parse .md as MDX rather than CommonMark.
    //
    // future.v4 flips this default, and CommonMark has no admonition
    // syntax — so every ::: block renders as literal colons instead of a
    // callout, silently. The install page's warning about the binary
    // needing to be named `dun` on PATH shipped that way, which is the
    // one callout on the site that most needs to stand out.
    format: 'mdx',
    hooks: {
      onBrokenMarkdownLinks: 'throw',
    },
  },

  future: {
    v4: true,
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          routeBasePath: '/',
          editUrl: 'https://github.com/navjyotnishant/whodunit/tree/main/website/',
        },
        // No blog. This site documents a tool; a blog with no posts is a
        // dead nav item that says the project is abandoned.
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    image: 'img/social-card.png',
    colorMode: {
      defaultMode: 'dark',
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'whodunit',
      logo: {
        alt: 'whodunit',
        src: 'img/logo.svg',
      },
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docs',
          position: 'left',
          label: 'Documentation',
        },
        {
          href: 'https://github.com/navjyotnishant/whodunit',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Getting started',
          items: [
            {label: 'Install', to: '/getting-started/install'},
            {label: 'Your first commit', to: '/getting-started/first-commit'},
            {label: 'Reading the trailer', to: '/getting-started/the-trailer'},
          ],
        },
        {
          title: 'Reference',
          items: [
            {label: 'Agent capability matrix', to: '/reference/agent-capabilities'},
            {label: 'What the numbers mean', to: '/reference/what-the-numbers-mean'},
            {label: 'How attribution works', to: '/reference/how-attribution-works'},
          ],
        },
        {
          title: 'More',
          items: [
            {label: 'GitHub', href: 'https://github.com/navjyotnishant/whodunit'},
            {label: 'Releases', href: 'https://github.com/navjyotnishant/whodunit/releases'},
          ],
        },
      ],
      copyright: `Apache-2.0. Built ${new Date().getFullYear()}.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'json', 'sql', 'toml', 'powershell', 'ruby'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
