import { defineConfig } from 'vitepress'

// Hermes 文档站配置。
// 设计要点：
// - 内容 markdown 与 .vitepress 同处 docs-site/（单一真源），node_modules 可达。
// - base 可经 DOCS_BASE 覆盖：GitHub Pages 子路径 /云主机子目录部署时设置，默认 '/'。
// - cleanUrls: false，生成 /xxx.html，nginx 无需 SPA fallback 即可直接访问静态文件。
export default defineConfig({
  title: 'Hermes',
  description: '基于 Zeus 服务治理的 K8s Ingress 控制器 —— 约束明确，可直接投产',
  lang: 'zh-CN',
  lastUpdated: true,
  cleanUrls: false,
  base: process.env.DOCS_BASE ?? '/',
  ignoreDeadLinks: true,
  head: [['meta', { name: 'theme-color', content: '#0d9488' }]],
  themeConfig: {
    logo: '/logo.svg',
    search: { provider: 'local' },
    nav: [
      { text: '首页', link: '/' },
      { text: '指南', link: '/quickstart' },
      { text: '进阶', link: '/grpc' },
      { text: 'GitHub', link: 'https://github.com/go-ingress/ingress' }
    ],
    sidebar: [
      {
        text: '开始',
        items: [
          { text: '快速开始', link: '/quickstart' },
          { text: '路由指南', link: '/routing' }
        ]
      },
      {
        text: '使用',
        items: [
          { text: 'Annotations 参考', link: '/annotations' },
          { text: '运维与可观测', link: '/operations' },
          { text: '从 nginx 迁移', link: '/migration-from-nginx' }
        ]
      },
      {
        text: '进阶',
        items: [{ text: 'gRPC 路由（实验性）', link: '/grpc' }]
      },
      {
        text: '深入',
        items: [
          { text: '架构', link: '/architecture' },
          { text: '设计文档', link: '/DESIGN' }
        ]
      }
    ],
    socialLinks: [
      { icon: 'github', link: 'https://github.com/go-ingress/ingress' }
    ],
    footer: {
      message: '基于 MIT 协议发布',
      copyright: 'Copyright © 2026 Hermes'
    },
    outline: { level: [2, 3] },
    docFooter: { prev: '上一页', next: '下一页' },
    lastUpdatedText: '最后更新'
  }
})
