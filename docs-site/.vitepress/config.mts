import { defineConfig } from 'vitepress';

export default defineConfig({
  title: 'touchgrass',
  description: 'Self-hosted blue-green deploys, metrics, and error tracking',
  base: '/docs/',
  outDir: 'dist',
  srcExclude: ['README.md'],
  ignoreDeadLinks: true,
  themeConfig: {
    siteTitle: 'touchgrass docs',
    nav: [
      { text: 'Guide', link: '/quickstart' },
      { text: 'SDK', link: '/sdk' },
      { text: 'API reference', link: '/api/' },
    ],
    sidebar: [
      {
        text: 'Start here',
        items: [
          { text: 'Quickstart', link: '/quickstart' },
          { text: 'Concepts', link: '/concepts' },
        ],
      },
      {
        text: 'SDK',
        items: [
          { text: 'Node SDK', link: '/sdk' },
          { text: 'Ingest API', link: '/ingest' },
        ],
      },
      {
        text: 'Operate',
        items: [
          { text: 'Self-hosting', link: '/self-hosting' },
          { text: 'Troubleshooting', link: '/troubleshooting' },
        ],
      },
      {
        text: 'Reference',
        items: [{ text: 'REST API', link: '/api/' }],
      },
    ],
    socialLinks: [{ icon: 'github', link: 'https://github.com/oralecarlangelo/touchgrass' }],
    search: { provider: 'local' },
    footer: { message: 'Apache-2.0 · touchgrass' },
  },
});
