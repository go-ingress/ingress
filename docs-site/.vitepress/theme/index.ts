import DefaultTheme from 'vitepress/theme'
import './styles.css'

// Hermes 主题：复用 VitePress 默认主题，仅通过 CSS 变量覆盖品牌色（teal）。
// 不引入任何 UI 组件库——文档站纯静态、零运行时依赖，构建产物小。
export default {
  extends: DefaultTheme,
  enhanceApp() {
    // 品牌色由 styles.css 的 :root 变量统一覆盖，无需 JS 注入。
  }
}
