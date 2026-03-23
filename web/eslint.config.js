import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import sonarjs from 'eslint-plugin-sonarjs'
import { defineConfig, globalIgnores } from 'eslint/config'

const DEFAULT_COGNITIVE_COMPLEXITY = 15
const TSX_COGNITIVE_COMPLEXITY = 18

export default defineConfig([
  globalIgnores(['dist', 'coverage']),
  // 共享配置
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
      sonarjs.configs.recommended,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    rules: {
      // 保持业务逻辑函数的默认门槛严格，避免把复杂度宽松扩散到整个前端层。
      'sonarjs/cognitive-complexity': ['error', DEFAULT_COGNITIVE_COMPLEXITY],
    },
  },
  // *.ts (工具/业务逻辑)
  {
    files: ['**/*.ts'],
    rules: {
      'max-lines-per-function': ['error', { max: 150, skipBlankLines: true, skipComments: true }],
    },
  },
  // *.tsx (组件): 250 行
  {
    files: ['**/*.tsx'],
    rules: {
      'max-lines-per-function': ['error', { max: 250, skipBlankLines: true, skipComments: true }],
      // 组件天然包含条件渲染和事件分支，比纯逻辑函数更容易触发认知复杂度；
      // 单独放宽，避免为过 lint 把页面容器拆成失去语义的碎片。
      'sonarjs/cognitive-complexity': ['error', TSX_COGNITIVE_COMPLEXITY],
    },
  },
  // ✅ 测试文件 override（放宽）
  {
    files: [
      '**/*.{test,spec}.{ts,tsx}',
      '**/__tests__/**/*.{ts,tsx}',
    ],
    rules: {
      'max-lines-per-function': ['warn', { max: 300, skipBlankLines: true, skipComments: true }],
      'sonarjs/cognitive-complexity': ['warn', 20],
    },
  },
])
